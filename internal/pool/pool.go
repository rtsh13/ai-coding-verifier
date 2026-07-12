// Package pool manages a fixed-capacity set of pre-warmed, reusable sandbox
// containers. Its whole purpose is to keep container creation off the per-job
// path: create a fleet of containers once, hand them out for jobs, and reuse them
// — so job assignment is near-instant rather than paying container start-up each
// time.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aicv/internal/dockercli"
)

// Backend is the slice of dockercli.Client the pool depends on. Keeping it an
// interface lets unit tests substitute a fake with no podman. A real
// *dockercli.Client satisfies it directly.
type Backend interface {
	Create(ctx context.Context, cfg dockercli.CreateConfig) (string, error)
	Start(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
}

// Config configures a Pool.
type Config struct {
	Image               string // sandbox image, e.g. "rust-sandbox"
	MinWarm             int    // containers to pre-create at New (<= MaxSize)
	MaxSize             int    // hard cap on live containers
	MaxJobsPerContainer int    // recycle a container after this many jobs (0 = unlimited)
	MemBytes            int64  // per-container memory cap (0 = unlimited)
	NanoCPUs            int64  // per-container CPU cap (0 = unlimited)
}

var (
	// ErrClosed is returned by Acquire once the pool has been closed.
	ErrClosed = errors.New("pool: closed")
	// ErrInvalidConfig is returned by New for a nonsensical size configuration.
	ErrInvalidConfig = errors.New("pool: MaxSize must be >= 1 and >= MinWarm")

	errAtCapacity = errors.New("pool: at capacity")
)

// Pool is a fixed-capacity pool of pre-warmed, reusable sandbox containers.
type Pool struct {
	backend Backend
	cfg     Config
	idle    chan *Container
	done    chan struct{}

	mu     sync.Mutex
	all    map[string]*Container // every live container, for Close
	count  int                   // live container count (capacity accounting)
	closed bool
}

// New creates a pool and pre-warms MinWarm containers.
func New(b Backend, cfg Config) (*Pool, error) {
	if cfg.MaxSize < 1 || cfg.MinWarm > cfg.MaxSize {
		return nil, ErrInvalidConfig
	}
	p := &Pool{
		backend: b,
		cfg:     cfg,
		idle:    make(chan *Container, cfg.MaxSize),
		done:    make(chan struct{}),
		all:     make(map[string]*Container),
	}
	for i := 0; i < cfg.MinWarm; i++ {
		c, err := p.grow(context.Background())
		if err != nil {
			_ = p.Close(context.Background())
			return nil, fmt.Errorf("pre-warm: %w", err)
		}
		c.state = Idle
		p.idle <- c
	}
	return p, nil
}

// Acquire returns a ready container, blocking until one is free if the pool is at
// capacity. It respects ctx: a deadline or cancellation aborts the wait.
func (p *Pool) Acquire(ctx context.Context) (*Container, error) {
	// A closed pool never hands out a container, even though the idle channel may
	// still hold pointers to already-removed ones.
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	// Fast path: reuse an already-idle container.
	select {
	case c := <-p.idle:
		c.state = Busy
		return c, nil
	default:
	}

	// Try to grow the pool if we're under the cap.
	c, err := p.grow(ctx)
	if err == nil {
		c.state = Busy
		return c, nil
	}
	if err != errAtCapacity {
		return nil, err // a real error (closed, or backend failure)
	}

	// At capacity: wait for a Release (or close / ctx cancellation).
	select {
	case c := <-p.idle:
		c.state = Busy
		return c, nil
	case <-p.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns a container to the pool. If the container has served its
// configured job limit it is recycled (removed) instead of being reused.
func (p *Pool) Release(c *Container) {
	c.jobs++
	c.state = Idle

	if p.cfg.MaxJobsPerContainer > 0 && c.jobs >= p.cfg.MaxJobsPerContainer {
		p.discard(c)
		return
	}

	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		p.discard(c)
		return
	}

	select {
	case p.idle <- c:
	default:
		// idle is sized to MaxSize and count <= MaxSize, so this should never
		// happen; discard defensively rather than block.
		p.discard(c)
	}
}

// Close removes every container and stops the pool. Acquire calls waiting or made
// after Close return ErrClosed. Idempotent.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	all := make([]*Container, 0, len(p.all))
	for _, c := range p.all {
		all = append(all, c)
	}
	p.all = map[string]*Container{}
	p.count = 0
	p.mu.Unlock()
	close(p.done)

	var firstErr error
	for _, c := range all {
		if err := p.backend.Remove(ctx, c.id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stats returns a snapshot of pool occupancy.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	idle := len(p.idle)
	return Stats{Idle: idle, Busy: p.count - idle, Total: p.count, Max: p.cfg.MaxSize}
}

// grow reserves a capacity slot and creates one container, or returns
// errAtCapacity if the pool is already full.
func (p *Pool) grow(ctx context.Context) (*Container, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	if p.count >= p.cfg.MaxSize {
		p.mu.Unlock()
		return nil, errAtCapacity
	}
	p.count++ // reserve the slot before the slow create, so concurrent callers see it
	p.mu.Unlock()

	c, err := p.create(ctx)
	if err != nil {
		p.mu.Lock()
		p.count--
		p.mu.Unlock()
		return nil, err
	}
	return c, nil
}

func (p *Pool) create(ctx context.Context) (*Container, error) {
	id, err := p.backend.Create(ctx, dockercli.CreateConfig{
		Image:    p.cfg.Image,
		Cmd:      []string{"sleep", "2147483647"},
		Network:  "none",
		MemBytes: p.cfg.MemBytes,
		NanoCPUs: p.cfg.NanoCPUs,
	})
	if err != nil {
		return nil, fmt.Errorf("create: %w", err)
	}
	if err := p.backend.Start(ctx, id); err != nil {
		_ = p.backend.Remove(ctx, id)
		return nil, fmt.Errorf("start: %w", err)
	}
	c := &Container{id: id, image: p.cfg.Image}
	p.mu.Lock()
	p.all[id] = c
	p.mu.Unlock()
	return c, nil
}

// discard removes a container and frees its capacity slot.
func (p *Pool) discard(c *Container) {
	p.mu.Lock()
	if _, ok := p.all[c.id]; ok {
		delete(p.all, c.id)
		p.count--
	}
	p.mu.Unlock()
	_ = p.backend.Remove(context.Background(), c.id)
}

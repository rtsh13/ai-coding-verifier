package pool

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
)

// fakeBackend is an in-memory stand-in for dockercli.Client. It records call
// counts and tracks the number of concurrently live containers so tests can
// assert the pool never exceeds its cap.
type fakeBackend struct {
	mu        sync.Mutex
	created   int
	removed   int
	live      int
	maxLive   int
	nextID    int
	createErr error
	startErr  error
}

func (f *fakeBackend) Create(_ context.Context, _ dockercli.CreateConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created++
	f.live++
	if f.live > f.maxLive {
		f.maxLive = f.live
	}
	f.nextID++
	return fmt.Sprintf("c%d", f.nextID), nil
}

func (f *fakeBackend) Start(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startErr
}

func (f *fakeBackend) Remove(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	f.live--
	return nil
}

func (f *fakeBackend) snap() (created, removed, live, maxLive int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created, f.removed, f.live, f.maxLive
}

func acquire(t *testing.T, p *Pool) *Container {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return c
}

func TestNew_PrewarmsMinWarm(t *testing.T) {
	f := &fakeBackend{}
	p, err := New(f, Config{Image: "img", MinWarm: 3, MaxSize: 5})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close(context.Background())

	if created, _, live, _ := f.snap(); created != 3 || live != 3 {
		t.Errorf("after prewarm: created=%d live=%d, want 3/3", created, live)
	}
	if s := p.Stats(); s.Idle != 3 || s.Total != 3 {
		t.Errorf("stats = %+v, want Idle 3 Total 3", s)
	}
}

func TestNew_InvalidConfig(t *testing.T) {
	for _, cfg := range []Config{{MaxSize: 0}, {MaxSize: 2, MinWarm: 3}} {
		if _, err := New(&fakeBackend{}, cfg); err != ErrInvalidConfig {
			t.Errorf("New(%+v) err = %v, want ErrInvalidConfig", cfg, err)
		}
	}
}

func TestAcquire_ReusesIdleContainer(t *testing.T) {
	f := &fakeBackend{}
	p, _ := New(f, Config{Image: "img", MinWarm: 1, MaxSize: 3})
	defer p.Close(context.Background())

	c1 := acquire(t, p)
	p.Release(c1)
	c2 := acquire(t, p)

	if c1.ID() != c2.ID() {
		t.Errorf("expected reuse; got different ids %s / %s", c1.ID(), c2.ID())
	}
	if created, _, _, _ := f.snap(); created != 1 {
		t.Errorf("created = %d, want 1 (container reused, not recreated)", created)
	}
}

func TestAcquire_GrowsThenBlocksAtCap(t *testing.T) {
	f := &fakeBackend{}
	p, _ := New(f, Config{Image: "img", MinWarm: 0, MaxSize: 2})
	defer p.Close(context.Background())

	_ = acquire(t, p)
	_ = acquire(t, p)
	if created, _, _, _ := f.snap(); created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}

	// Third acquire at capacity must block, then time out with ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(ctx); err != context.DeadlineExceeded {
		t.Errorf("Acquire at cap err = %v, want DeadlineExceeded", err)
	}
}

func TestAcquire_BlocksUntilRelease(t *testing.T) {
	f := &fakeBackend{}
	p, _ := New(f, Config{Image: "img", MinWarm: 0, MaxSize: 1})
	defer p.Close(context.Background())

	c1 := acquire(t, p)

	got := make(chan *Container, 1)
	go func() {
		c, err := p.Acquire(context.Background())
		if err != nil {
			t.Errorf("blocked Acquire: %v", err)
			got <- nil
			return
		}
		got <- c
	}()

	// Give the goroutine time to block on the empty pool.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("Acquire returned before Release; should have blocked")
	default:
	}

	p.Release(c1)
	select {
	case c2 := <-got:
		if c2 == nil || c2.ID() != c1.ID() {
			t.Errorf("released container not handed to waiter")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter never unblocked after Release")
	}
}

func TestConcurrent_NeverExceedsMaxSize(t *testing.T) {
	f := &fakeBackend{}
	const max = 4
	p, _ := New(f, Config{Image: "img", MinWarm: 2, MaxSize: max})
	defer p.Close(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := p.Acquire(ctx)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			time.Sleep(time.Millisecond) // simulate a job
			p.Release(c)
		}()
	}
	wg.Wait()

	if _, _, _, maxLive := f.snap(); maxLive > max {
		t.Errorf("maxLive = %d, want <= %d", maxLive, max)
	}
}

func TestClose_RemovesAllContainers(t *testing.T) {
	f := &fakeBackend{}
	p, _ := New(f, Config{Image: "img", MinWarm: 3, MaxSize: 5})

	_ = acquire(t, p) // one busy, two idle — Close must remove all three

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if created, removed, live, _ := f.snap(); removed != created || live != 0 {
		t.Errorf("after Close: created=%d removed=%d live=%d, want removed==created, live 0",
			created, removed, live)
	}
	if _, err := p.Acquire(context.Background()); err != ErrClosed {
		t.Errorf("Acquire after Close err = %v, want ErrClosed", err)
	}
}

func TestMaxJobsPerContainer_Recycles(t *testing.T) {
	f := &fakeBackend{}
	p, _ := New(f, Config{Image: "img", MinWarm: 1, MaxSize: 1, MaxJobsPerContainer: 2})
	defer p.Close(context.Background())

	c1 := acquire(t, p)
	p.Release(c1) // jobs=1, kept
	c2 := acquire(t, p)
	if c2.ID() != c1.ID() {
		t.Fatalf("expected same container on 2nd job")
	}
	p.Release(c2) // jobs=2 == limit -> recycled (removed)

	if created, removed, _, _ := f.snap(); created != 1 || removed != 1 {
		t.Errorf("after recycle: created=%d removed=%d, want 1/1", created, removed)
	}

	c3 := acquire(t, p) // pool empty -> fresh container
	if c3.ID() == c1.ID() {
		t.Errorf("expected a fresh container after recycle, got reused %s", c3.ID())
	}
	if created, _, _, _ := f.snap(); created != 2 {
		t.Errorf("created = %d, want 2 (recycled one replaced)", created)
	}
}

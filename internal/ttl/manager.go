// Package ttl provides a background sweep that enforces per-job wall-clock
// deadlines at container granularity. It is the backstop to the in-container
// timeout used by the sandbox: that kills a runaway *process*; this catches a
// job whose whole exec or container has hung, by force-killing it after its
// deadline. This is the "auto-GC" reaper — nothing overruns unnoticed.
package ttl

import (
	"context"
	"sync"
	"time"
)

// KillFunc terminates the job/container identified by id. Typically wired to the
// pool's discard or dockercli.Remove.
type KillFunc func(id string) error

// Manager tracks deadlines and reaps anything past them on a periodic sweep.
type Manager struct {
	kill  KillFunc
	sweep time.Duration

	mu        sync.Mutex
	deadlines map[string]time.Time
	now       func() time.Time // injectable for tests
	done      chan struct{}    // closed when the sweep goroutine exits
}

// New creates a Manager that reaps every sweep interval using kill.
func New(kill KillFunc, sweep time.Duration) *Manager {
	return &Manager{
		kill:      kill,
		sweep:     sweep,
		deadlines: make(map[string]time.Time),
		now:       time.Now,
	}
}

// Track records that id must finish within ttl; after that it is eligible to be
// reaped on the next sweep.
func (m *Manager) Track(id string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deadlines[id] = m.now().Add(ttl)
}

// Untrack cancels tracking for id (the job finished in time).
func (m *Manager) Untrack(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.deadlines, id)
}

// Tracked reports how many ids are currently being watched.
func (m *Manager) Tracked() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.deadlines)
}

// Start runs the sweep loop until ctx is cancelled. After cancelling ctx, call
// Wait to block until any in-flight sweep (and its kills) has finished.
func (m *Manager) Start(ctx context.Context) {
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		t := time.NewTicker(m.sweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.sweepOnce()
			}
		}
	}()
}

// Wait blocks until the sweep goroutine has exited, which happens after the ctx
// passed to Start is cancelled and any in-flight sweep completes. Safe to call
// even if Start was never called (returns immediately).
func (m *Manager) Wait() {
	if m.done != nil {
		<-m.done
	}
}

// sweepOnce reaps every id whose deadline has passed. Kills are invoked outside
// the lock so a slow kill doesn't stall Track/Untrack.
func (m *Manager) sweepOnce() {
	now := m.now()
	m.mu.Lock()
	var expired []string
	for id, deadline := range m.deadlines {
		if !now.Before(deadline) { // now >= deadline
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		delete(m.deadlines, id)
	}
	m.mu.Unlock()

	for _, id := range expired {
		_ = m.kill(id)
	}
}

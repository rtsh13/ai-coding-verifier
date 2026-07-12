package ttl

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock for deterministic sweep tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recorder captures killed ids.
type recorder struct {
	mu     sync.Mutex
	killed []string
}

func (r *recorder) kill(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killed = append(r.killed, id)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.killed)
}

func (r *recorder) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.killed {
		if k == id {
			return true
		}
	}
	return false
}

// newWithClock builds a Manager wired to a fake clock (white-box).
func newWithClock(kill KillFunc, clk *fakeClock) *Manager {
	m := New(kill, time.Hour) // sweep interval irrelevant; tests call sweepOnce
	m.now = clk.Now
	return m
}

func TestSweep_ReapsExpired(t *testing.T) {
	rec := &recorder{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newWithClock(rec.kill, clk)

	m.Track("job-a", 10*time.Second)
	clk.Advance(11 * time.Second)
	m.sweepOnce()

	if !rec.has("job-a") {
		t.Errorf("expired job not killed; killed=%v", rec.killed)
	}
	if m.Tracked() != 0 {
		t.Errorf("expired job still tracked after reap")
	}
}

func TestSweep_LeavesUnexpired(t *testing.T) {
	rec := &recorder{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newWithClock(rec.kill, clk)

	m.Track("job-a", 10*time.Second)
	clk.Advance(5 * time.Second) // still within deadline
	m.sweepOnce()

	if rec.count() != 0 {
		t.Errorf("unexpired job was killed; killed=%v", rec.killed)
	}
	if m.Tracked() != 1 {
		t.Errorf("unexpired job should still be tracked, Tracked=%d", m.Tracked())
	}
}

func TestUntrack_PreventsKill(t *testing.T) {
	rec := &recorder{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newWithClock(rec.kill, clk)

	m.Track("job-a", 10*time.Second)
	m.Untrack("job-a")
	clk.Advance(11 * time.Second)
	m.sweepOnce()

	if rec.count() != 0 {
		t.Errorf("untracked job was killed; killed=%v", rec.killed)
	}
}

func TestSweep_KillsEachOnce(t *testing.T) {
	rec := &recorder{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newWithClock(rec.kill, clk)

	m.Track("job-a", 1*time.Second)
	clk.Advance(2 * time.Second)
	m.sweepOnce()
	m.sweepOnce() // a second sweep must not kill it again (already untracked)

	if rec.count() != 1 {
		t.Errorf("want exactly one kill, got %d: %v", rec.count(), rec.killed)
	}
}

func TestStart_SweepsThenStopsOnCancel(t *testing.T) {
	rec := &recorder{}
	m := New(rec.kill, 5*time.Millisecond) // real clock, fast sweep
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	m.Track("x", 0) // already at deadline
	waitFor(t, time.Second, func() bool { return rec.has("x") })

	cancel()
	time.Sleep(25 * time.Millisecond) // let the goroutine observe cancellation

	before := rec.count()
	m.Track("y", 0) // expired, but the sweeper should be stopped now
	time.Sleep(40 * time.Millisecond)
	if rec.count() != before {
		t.Errorf("sweeper kept running after cancel; killed=%v", rec.killed)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

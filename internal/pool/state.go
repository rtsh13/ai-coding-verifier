package pool

// State is the lifecycle state of a pooled container. With one job per container,
// a container is either idle (available in the pool) or busy (checked out).
type State int

const (
	Idle State = iota
	Busy
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Busy:
		return "busy"
	default:
		return "unknown"
	}
}

// Stats is a snapshot of pool occupancy.
type Stats struct {
	Idle  int
	Busy  int
	Total int
	Max   int
}

// Overflow reports whether the pool is saturated: at max size with none idle, so
// the next Acquire must wait for a Release.
func (s Stats) Overflow() bool {
	return s.Total >= s.Max && s.Idle == 0
}

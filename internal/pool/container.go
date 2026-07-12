package pool

// Container is a pooled sandbox container. It satisfies sandbox.Container via its
// ID method, so it can be passed straight to sandbox.Run.
//
// A Container is only ever touched by one goroutine at a time: while it sits in
// the pool's idle channel no one holds it, and once Acquire hands it out exactly
// one caller owns it until Release. That single-owner property is why the mutable
// fields (state, jobs) need no lock of their own.
type Container struct {
	id    string
	image string
	state State
	jobs  int // number of jobs run in this container so far
}

// ID returns the runtime container id (satisfies sandbox.Container).
func (c *Container) ID() string { return c.id }

// Image is the image the container was created from.
func (c *Container) Image() string { return c.image }

// State is the container's current pool state.
func (c *Container) State() State { return c.state }

// Jobs is how many jobs have run in this container so far.
func (c *Container) Jobs() int { return c.jobs }

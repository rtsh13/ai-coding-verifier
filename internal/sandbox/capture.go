package sandbox

import (
	"math"
	"strconv"
	"time"
)

// wrapWithTimeout wraps a command so the runtime enforces a wall-clock limit
// *inside* the container, using busybox `timeout -s KILL`. SIGKILL (not the
// default SIGTERM) is used so a job cannot catch and ignore the signal. It
// returns the wrapped command and the whole-second limit actually applied
// (busybox timeout takes integer seconds). A non-positive ttl leaves the command
// unwrapped and returns 0.
func wrapWithTimeout(cmd []string, ttl time.Duration) ([]string, int) {
	if ttl <= 0 {
		return cmd, 0
	}
	secs := int(math.Ceil(ttl.Seconds()))
	if secs < 1 {
		secs = 1
	}
	wrapped := append([]string{"timeout", "-s", "KILL", strconv.Itoa(secs)}, cmd...)
	return wrapped, secs
}

// timedOut reports whether an exec was killed by the timeout wrapper rather than
// exiting on its own. busybox timeout propagates 128+signal (137 for SIGKILL,
// 143 for SIGTERM); we corroborate with elapsed time so a signal death well
// before the limit (e.g. an OOM kill) is not misread as a timeout.
func timedOut(exitCode, ttlSecs int, dur time.Duration) bool {
	if ttlSecs <= 0 {
		return false
	}
	limit := time.Duration(ttlSecs) * time.Second
	killedBySignal := exitCode == 137 || exitCode == 143
	return killedBySignal && dur >= limit-500*time.Millisecond
}

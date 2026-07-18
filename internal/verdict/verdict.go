// Package verdict reduces a pipeline Result to a single, public outcome label —
// the verifier's answer to "what happened?". It is intentionally separate from
// (and additive to) the RL-era internal/reward package: the live systems path
// uses this, and the old reward scalar is left untouched for now.
package verdict

import "github.com/aicv/internal/pipeline"

// Outcome is the four-way classification of a submission's fate.
type Outcome int

const (
	Passed       Outcome = iota // compiled and all tests passed
	CompileError                // did not compile
	RuntimeError                // compiled but crashed / was killed at run time
	TestFailed                  // compiled and ran, but assertions failed
)

func (o Outcome) String() string {
	switch o {
	case Passed:
		return "passed"
	case CompileError:
		return "compile_error"
	case RuntimeError:
		return "runtime_error"
	case TestFailed:
		return "test_failed"
	default:
		return "unknown"
	}
}

// Classify maps a pipeline Result to its Outcome. Order matters: a submission
// that never compiled is a CompileError regardless of anything else (a compile
// that timed out still "did not compile"); otherwise a pass is a pass; a
// compiled submission that timed out or died by signal is a RuntimeError; and
// anything else that compiled but did not pass is a plain TestFailed.
func Classify(r pipeline.Result) Outcome {
	switch {
	case !r.Compiled:
		return CompileError
	case r.Passed:
		return Passed
	case r.TimedOut || r.Crashed:
		return RuntimeError
	default:
		return TestFailed
	}
}

package pipeline

import "time"

// Stage identifies how far a submission got through the two-stage pipeline
// before its outcome was determined. This is the independent error-attribution
// signal: a compile-time failure and a runtime/test failure carry different
// semantic meaning for the reward.
type Stage int

const (
	StageCompile Stage = iota // failed (or is being judged) at compilation
	StageExecute              // compiled successfully; judged at execution/tests
)

func (s Stage) String() string {
	switch s {
	case StageCompile:
		return "compile"
	case StageExecute:
		return "execute"
	default:
		return "unknown"
	}
}

// Lang is the target language of a submission.
type Lang int

const (
	Rust Lang = iota // primary evaluation language
	Go               // pipeline-validation language
)

func (l Lang) String() string {
	switch l {
	case Rust:
		return "rust"
	case Go:
		return "go"
	default:
		return "unknown"
	}
}

// Result is the outcome of running a submission through compile then execute.
// It is a pure data record produced by the pipeline and consumed by the
// verifier (raw compiler output) and the reward layer (Stage/Compiled/Passed).
type Result struct {
	Stage         Stage
	Compiled      bool
	Passed        bool
	CompilerRaw   string // human-readable compiler stderr
	CompilerJSON  string // cargo --message-format=json output; "" for Go
	RuntimeStdout string
	RuntimeStderr string
	ExitCode      int
	TimedOut      bool
	// Crashed is true when a compiled program terminated abnormally at run time
	// (a panic, or a kill by signal) rather than exiting cleanly with a failed
	// assertion. It is what separates a "runtime error" from a "failed test",
	// the distinction StepCoder's baseline reward draws. The pipeline sets it.
	Crashed  bool
	Duration time.Duration
}

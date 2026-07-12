package verdict

import (
	"testing"

	"github.com/aicv/internal/pipeline"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		res  pipeline.Result
		want Outcome
	}{
		{
			name: "passed",
			res:  pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: true},
			want: Passed,
		},
		{
			name: "compile error",
			res:  pipeline.Result{Stage: pipeline.StageCompile, Compiled: false},
			want: CompileError,
		},
		{
			name: "compile timeout is still a compile error",
			res:  pipeline.Result{Stage: pipeline.StageCompile, Compiled: false, TimedOut: true},
			want: CompileError,
		},
		{
			name: "runtime crash (signal death)",
			res:  pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: false, Crashed: true},
			want: RuntimeError,
		},
		{
			name: "runtime timeout at execute",
			res:  pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: false, TimedOut: true},
			want: RuntimeError,
		},
		{
			name: "failed assertion",
			res:  pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: false},
			want: TestFailed,
		},
	}
	for _, c := range cases {
		if got := Classify(c.res); got != c.want {
			t.Errorf("%s: Classify = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOutcome_String(t *testing.T) {
	cases := map[Outcome]string{
		Passed:       "passed",
		CompileError: "compile_error",
		RuntimeError: "runtime_error",
		TestFailed:   "test_failed",
		Outcome(99):  "unknown",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}

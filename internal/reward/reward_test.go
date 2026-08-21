package reward

import (
	"math"
	"testing"

	"github.com/aicv/internal/pipeline"
	"github.com/aicv/internal/verifier"
)

// Result builders for the four outcomes

func passR() pipeline.Result {
	return pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: true}
}

// testFailR: compiled, ran to completion, but assertions failed (clean exit).
func testFailR() pipeline.Result {
	return pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: false, Crashed: false}
}

// runtimeErrR: compiled, but panicked / was killed at run time.
func runtimeErrR() pipeline.Result {
	return pipeline.Result{Stage: pipeline.StageExecute, Compiled: true, Passed: false, Crashed: true}
}

func compileFailR() pipeline.Result {
	return pipeline.Result{Stage: pipeline.StageCompile, Compiled: false, Passed: false}
}

func errDiags(n int) []verifier.Diagnostic {
	d := make([]verifier.Diagnostic, n)
	for i := range d {
		d[i] = verifier.Diagnostic{Level: "error", Code: "E0308"}
	}
	return d
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Arm A: Coarse (StepCoder's exact four levels)

func TestCoarse_FourLevels(t *testing.T) {
	cases := []struct {
		name string
		r    pipeline.Result
		want float64
	}{
		{"pass", passR(), 1.0},
		{"failed test", testFailR(), -0.3},
		{"runtime error", runtimeErrR(), -0.6},
		{"compile error", compileFailR(), -1.0},
	}
	for _, c := range cases {
		got := Compute(c.r, errDiags(2), Coarse).Value
		if !approx(got, c.want) {
			t.Errorf("Coarse %s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCoarse_CompileErrorIsFlat_IgnoresErrorCount(t *testing.T) {
	// The whole point of the baseline: every compile failure is -1.0 regardless
	// of how many errors there are. This is what Structured improves on.
	one := Compute(compileFailR(), errDiags(1), Coarse).Value
	many := Compute(compileFailR(), errDiags(9), Coarse).Value
	if !approx(one, -1.0) || !approx(many, -1.0) {
		t.Errorf("Coarse compile errors should both be -1.0, got %v and %v", one, many)
	}
}

// Arm B: Structured (identical to Coarse except the compile bucket)

func TestStructured_SharesAnchorsWithCoarse(t *testing.T) {
	// Pass, failed-test, and runtime-error must be identical across arms; only
	// the compile stage is manipulated.
	for _, r := range []pipeline.Result{passR(), testFailR(), runtimeErrR()} {
		coarse := Compute(r, nil, Coarse).Value
		structured := Compute(r, nil, Structured).Value
		if !approx(coarse, structured) {
			t.Errorf("non-compile outcome differs between arms: coarse=%v structured=%v", coarse, structured)
		}
	}
}

func TestStructured_CompileFail_FewerErrorsScoreHigher(t *testing.T) {
	one := Compute(compileFailR(), errDiags(1), Structured).Value
	five := Compute(compileFailR(), errDiags(5), Structured).Value
	if !(one > five) {
		t.Errorf("want fewer errors to score higher: one=%v five=%v", one, five)
	}
	// Band is [-1.0, -0.65): remaining(n=1)=0.8 -> -1 + 0.35*0.8 = -0.72;
	// remaining(n=5)=0 -> -1.0.
	if !approx(one, -0.72) {
		t.Errorf("compile-fail 1 error = %v, want -0.72", one)
	}
	if !approx(five, -1.0) {
		t.Errorf("compile-fail 5 errors = %v, want -1.0", five)
	}
}

func TestStructured_CompileAlwaysWorseThanRuntime(t *testing.T) {
	// Even the best-scoring compile failure must stay strictly below a runtime
	// error, preserving StepCoder's stage ordering.
	runtime := Compute(runtimeErrR(), nil, Structured).Value
	for n := 0; n <= 10; n++ {
		c := Compute(compileFailR(), errDiags(n), Structured).Value
		if !(c < runtime) {
			t.Errorf("compile(n=%d)=%v not < runtime=%v", n, c, runtime)
		}
	}
}

func TestStructured_BeatsCoarseOnCompile(t *testing.T) {
	// A nearly-compiling submission should be rewarded more by the structured
	// arm than by the flat coarse arm. This is the ablation's core hypothesis.
	structured := Compute(compileFailR(), errDiags(1), Structured).Value
	coarse := Compute(compileFailR(), errDiags(1), Coarse).Value
	if !(structured > coarse) {
		t.Errorf("structured(%v) should exceed coarse(%v) for a near-compiling submission", structured, coarse)
	}
}

func TestStructured_Monotonic_CompileLtRuntimeLtTestLtPass(t *testing.T) {
	c := Compute(compileFailR(), errDiags(1), Structured).Value
	r := Compute(runtimeErrR(), nil, Structured).Value
	tf := Compute(testFailR(), nil, Structured).Value
	p := Compute(passR(), nil, Structured).Value
	if !(c < r && r < tf && tf < p) {
		t.Errorf("want compile(%v) < runtime(%v) < testFail(%v) < pass(%v)", c, r, tf, p)
	}
}

func TestStructured_NeverBelowMinusOne(t *testing.T) {
	if got := Compute(compileFailR(), errDiags(50), Structured); got.Value < -1.0 {
		t.Errorf("value = %v, want >= -1.0", got.Value)
	}
}

func TestStructured_BreakdownRecordsErrorCount(t *testing.T) {
	got := Compute(compileFailR(), errDiags(3), Structured)
	if got.Breakdown["error_count"] != 3 {
		t.Errorf("Breakdown[error_count] = %v, want 3", got.Breakdown["error_count"])
	}
}

func TestModeIsRecorded(t *testing.T) {
	if Compute(passR(), nil, Coarse).Mode != Coarse {
		t.Error("Mode not recorded for Coarse")
	}
	if Compute(passR(), nil, Structured).Mode != Structured {
		t.Error("Mode not recorded for Structured")
	}
}

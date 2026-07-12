// Package reward turns a pipeline Result (and, in structured mode, its parsed
// diagnostics) into a scalar reward for the GRPO training loop.
//
// It implements the two arms of the dissertation's ablation, both on the SAME
// [-1, 1] range so the comparison is not confounded by scale:
//
//   - Coarse (Arm A): StepCoder's exact four-level reward, applied to a compiled
//     language. This is the published prior-art baseline, used as-is so the
//     ablation is not measured against a strawman.
//   - Structured (Arm B): identical to Coarse for pass / failed-test / runtime
//     error, but the single compile-error level is subdivided into a gradient by
//     how many errors remain (and, later, by error category). The compile stage
//     is the only manipulated variable, so any difference in training isolates
//     the value of structured compile diagnostics — the project's contribution.
package reward

import (
	"github.com/aicv/internal/pipeline"
	"github.com/aicv/internal/verifier"
)

// Mode selects which reward arm to compute.
type Mode int

const (
	Coarse     Mode = iota // Arm A: StepCoder's four fixed levels
	Structured             // Arm B: Coarse + graded compile bucket
)

// RewardResult is the reward plus an audit trail. Breakdown is kept so the exact
// shaping is inspectable and reproducible in the evaluation chapter.
type RewardResult struct {
	Value     float64
	Mode      Mode
	Breakdown map[string]float64
}

// StepCoder's four reward levels (Dou et al., 2024), used verbatim for Arm A and
// as the shared anchors for Arm B.
const (
	rewardPass       = 1.0
	rewardTestFailed = -0.3
	rewardRuntimeErr = -0.6
	rewardCompileErr = -1.0
)

// compileBandTop is the best score a compile failure may reach under Structured.
// It sits strictly below rewardRuntimeErr so every compile failure remains worse
// than any runtime error, preserving StepCoder's stage ordering. The compile
// gradient therefore lives in [rewardCompileErr, compileBandTop].
const compileBandTop = -0.65

// errorSaturation (K) is the error count at or above which a non-compiling
// submission receives the worst compile score. Provisional default; the
// structured formula is the project's single most important design decision and
// is documented verbatim in the methodology.
const errorSaturation = 5

// outcome is the four-way classification both arms share.
type outcome int

const (
	outCompileError outcome = iota
	outRuntimeError
	outTestFailed
	outPassed
)

func classify(r pipeline.Result) outcome {
	switch {
	case !r.Compiled:
		return outCompileError
	case r.Passed:
		return outPassed
	case r.TimedOut || r.Crashed:
		return outRuntimeError
	default:
		return outTestFailed
	}
}

// Compute scores a Result under the given mode. diags is only consulted in
// Structured mode and only when the submission failed to compile.
func Compute(r pipeline.Result, diags []verifier.Diagnostic, mode Mode) RewardResult {
	o := classify(r)

	// The three non-compile outcomes are identical across both arms.
	if o != outCompileError {
		v := anchorValue(o)
		return RewardResult{Value: v, Mode: mode, Breakdown: map[string]float64{
			"outcome": float64(o),
		}}
	}

	// Compile failure: Coarse gives the flat StepCoder level; Structured grades
	// it by remaining error count.
	if mode == Coarse {
		return RewardResult{Value: rewardCompileErr, Mode: Coarse, Breakdown: map[string]float64{
			"outcome": float64(outCompileError),
		}}
	}
	n := errorCount(diags)
	remaining := 1.0 - clamp01(float64(n)/float64(errorSaturation))
	value := rewardCompileErr + (compileBandTop-rewardCompileErr)*remaining
	return RewardResult{Value: value, Mode: Structured, Breakdown: map[string]float64{
		"outcome":     float64(outCompileError),
		"error_count": float64(n),
		"remaining":   remaining,
	}}
}

func anchorValue(o outcome) float64 {
	switch o {
	case outPassed:
		return rewardPass
	case outTestFailed:
		return rewardTestFailed
	case outRuntimeError:
		return rewardRuntimeErr
	default:
		return rewardCompileErr
	}
}

// errorCount counts error-level diagnostics (warnings do not block compilation
// and so do not affect the reward).
func errorCount(diags []verifier.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Level == "error" {
			n++
		}
	}
	return n
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

package pipeline

import (
	"slices"
	"testing"
)

func TestIsCrash(t *testing.T) {
	cases := map[int]bool{
		0:   false, // clean exit
		1:   false, // generic failure (e.g. a tool error)
		101: false, // cargo test: assertions failed / panic in test
		128: true,  // killed by signal
		137: true,  // SIGKILL (128+9)
		139: true,  // SIGSEGV (128+11)
	}
	for code, want := range cases {
		if got := isCrash(code); got != want {
			t.Errorf("isCrash(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestCommands_RustIsCargoGoIsGo(t *testing.T) {
	if got := compileCommand(Rust); !slices.Contains(got, "cargo") || !slices.Contains(got, "--no-run") {
		t.Errorf("Rust compile = %v, want cargo test --no-run", got)
	}
	if got := compileCommand(Go); !slices.Contains(got, "go") || !slices.Contains(got, "build") {
		t.Errorf("Go compile = %v, want go build", got)
	}
	if got := executeCommand(Rust); !slices.Contains(got, "cargo") || !slices.Contains(got, "test") {
		t.Errorf("Rust execute = %v, want cargo test", got)
	}
}

func TestRandID_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := randID()
		if id == "" || seen[id] {
			t.Fatalf("randID collision or empty at %d: %q", i, id)
		}
		seen[id] = true
	}
}

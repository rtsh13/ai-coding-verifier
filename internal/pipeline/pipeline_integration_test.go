//go:build integration

package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
	"github.com/aicv/internal/pool"
	"github.com/aicv/internal/verifier"
)

const testImage = "rust-sandbox"

func newPool(t *testing.T) (*dockercli.Client, *pool.Pool) {
	t.Helper()
	cli, err := dockercli.New()
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cli.Ping(pctx); err != nil {
		t.Skipf("podman not reachable (set DOCKER_HOST): %v", err)
	}
	p, err := pool.New(cli, pool.Config{Image: testImage, MinWarm: 1, MaxSize: 2})
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}
	t.Cleanup(func() {
		_ = p.Close(context.Background())
		_ = cli.Close()
	})
	return cli, p
}

// rustProject wraps a main.rs body into a minimal dependency-free cargo project.
func rustProject(body string) map[string][]byte {
	return map[string][]byte{
		"Cargo.toml":  []byte("[package]\nname = \"sol\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"),
		"src/main.rs": []byte(body),
	}
}

const passingRust = `pub fn add(a: i32, b: i32) -> i32 { a + b }
fn main() { println!("{}", add(1, 2)); }
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn adds() { assert_eq!(add(2, 2), 4); }
}
`

const compileErrRust = `fn main() { let _x: i32 = "hello"; }`

const testFailRust = `pub fn add(a: i32, b: i32) -> i32 { a + b }
fn main() {}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn adds() { assert_eq!(add(2, 2), 5); }
}
`

func run(t *testing.T, p *pool.Pool, cli *dockercli.Client, body string) Result {
	t.Helper()
	res, err := Run(context.Background(), p, cli, Job{
		Lang:  Rust,
		Files: rustProject(body),
		TTL:   90 * time.Second,
	})
	if err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	return res
}

func TestPipeline_Passing(t *testing.T) {
	cli, p := newPool(t)
	res := run(t, p, cli, passingRust)
	if !res.Compiled {
		t.Errorf("Compiled = false; compiler said: %s", res.CompilerRaw)
	}
	if !res.Passed {
		t.Errorf("Passed = false; runtime: %s / %s", res.RuntimeStdout, res.RuntimeStderr)
	}
	if res.Stage != StageExecute {
		t.Errorf("Stage = %s, want execute", res.Stage)
	}
}

func TestPipeline_CompileError_AttributedToCompile(t *testing.T) {
	cli, p := newPool(t)
	res := run(t, p, cli, compileErrRust)

	if res.Compiled {
		t.Errorf("Compiled = true, want false")
	}
	if res.Stage != StageCompile {
		t.Errorf("Stage = %s, want compile", res.Stage)
	}
	if res.RuntimeStdout != "" {
		t.Errorf("RuntimeStdout = %q, want empty (execution must not run)", res.RuntimeStdout)
	}
	// The captured JSON must be parseable by the verifier into an E0308.
	diags, err := verifier.ParseRust(res.CompilerJSON)
	if err != nil {
		t.Fatalf("verifier.ParseRust: %v", err)
	}
	var found bool
	for _, d := range diags {
		if d.Code == "E0308" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an E0308 diagnostic; got %d diagnostics", len(diags))
	}
}

func TestPipeline_TestFailure_AttributedToExecute(t *testing.T) {
	cli, p := newPool(t)
	res := run(t, p, cli, testFailRust)

	if !res.Compiled {
		t.Errorf("Compiled = false, want true (it compiles; only the test fails)")
	}
	if res.Passed {
		t.Errorf("Passed = true, want false")
	}
	if res.Stage != StageExecute {
		t.Errorf("Stage = %s, want execute", res.Stage)
	}
	if res.Crashed {
		t.Errorf("Crashed = true; a failing assertion is not a crash")
	}
}

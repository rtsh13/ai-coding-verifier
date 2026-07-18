package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aicv/pkg/api"
)

func TestParseLang(t *testing.T) {
	cases := map[string]api.Lang{"rust": api.Rust, "rs": api.Rust, "": api.Rust, "go": api.Go, "golang": api.Go}
	for in, want := range cases {
		got, err := parseLang(in)
		if err != nil || got != want {
			t.Errorf("parseLang(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseLang("cobol"); err == nil {
		t.Error("parseLang(cobol) should error")
	}
}

func TestJobFromPath_SingleFileWraps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sol.rs")
	if err := os.WriteFile(p, []byte("fn main() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := jobFromPath(p, api.Rust, 30*time.Second)
	if err != nil {
		t.Fatalf("jobFromPath: %v", err)
	}
	if _, ok := job.Files["Cargo.toml"]; !ok {
		t.Error("single .rs file should be wrapped with a Cargo.toml")
	}
	if string(job.Files["src/main.rs"]) != "fn main() {}" {
		t.Errorf("src/main.rs = %q", job.Files["src/main.rs"])
	}
	if job.TTL != 30*time.Second {
		t.Errorf("TTL = %v", job.TTL)
	}
}

func TestJobFromPath_DirSkipsTarget(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Cargo.toml"), "[package]")
	mustWrite(t, filepath.Join(dir, "src", "main.rs"), "fn main() {}")
	mustWrite(t, filepath.Join(dir, "target", "junk.o"), "binary")

	job, err := jobFromPath(dir, api.Rust, time.Minute)
	if err != nil {
		t.Fatalf("jobFromPath: %v", err)
	}
	if _, ok := job.Files["Cargo.toml"]; !ok {
		t.Error("missing Cargo.toml")
	}
	if _, ok := job.Files["src/main.rs"]; !ok {
		t.Error("missing src/main.rs")
	}
	for k := range job.Files {
		if strings.HasPrefix(k, "target/") {
			t.Errorf("target/ should be skipped, got %q", k)
		}
	}
}

func TestReadJobSpecs(t *testing.T) {
	in := `{"id":"a","lang":"rust","files":{"src/main.rs":"fn main(){}"},"ttl_secs":10}

{"id":"b","lang":"go","files":{"main.go":"package main"}}
`
	specs, err := readJobSpecs(strings.NewReader(in))
	if err != nil {
		t.Fatalf("readJobSpecs: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2 (blank line skipped)", len(specs))
	}
	job, err := specs[0].job(time.Minute)
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	if job.Lang != api.Rust || job.TTL != 10*time.Second {
		t.Errorf("spec a -> lang %v ttl %v", job.Lang, job.TTL)
	}
	// spec b has no ttl_secs -> default applies
	jobB, _ := specs[1].job(time.Minute)
	if jobB.TTL != time.Minute {
		t.Errorf("spec b TTL = %v, want default 1m", jobB.TTL)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

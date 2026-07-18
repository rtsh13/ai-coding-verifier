package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSeccompProfile(t *testing.T) {
	// Empty path => empty string, no error (fall back to runtime default).
	if got, err := resolveSeccompProfile(""); err != nil || got != "" {
		t.Errorf(`resolveSeccompProfile("") = %q, %v; want "", nil`, got, err)
	}

	// Existing file => its absolute path.
	dir := t.TempDir()
	path := filepath.Join(dir, "seccomp.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSeccompProfile(path)
	if err != nil {
		t.Fatalf("resolveSeccompProfile(existing) err = %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolveSeccompProfile(existing) = %q, want an absolute path", got)
	}
	if got != path { // temp dir is already absolute
		t.Errorf("resolveSeccompProfile(existing) = %q, want %q", got, path)
	}

	// Missing file => error (fail fast rather than silently ignore).
	if _, err := resolveSeccompProfile(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("resolveSeccompProfile(missing) err = nil, want error")
	}
}

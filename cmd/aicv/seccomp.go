package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveSeccompProfile turns a user-supplied seccomp profile path into the
// absolute path the container runtime should load. An empty path returns an
// empty string (the runtime's default profile then applies). A non-empty path
// must exist — it is resolved to an absolute path (podman reads it host-side)
// or an error is returned, so a typo fails fast rather than silently leaving
// the sandbox on the default profile.
func resolveSeccompProfile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("seccomp profile: %w", err)
	}
	return abs, nil
}

package pipeline

// executeCommand returns the command that runs a submission's tests, assuming it
// already compiled at the compile stage (so this is fast — the build is cached).
func executeCommand(lang Lang) []string {
	switch lang {
	case Go:
		return []string{"go", "test", "./..."}
	default: // Rust
		return []string{"cargo", "test", "--offline"}
	}
}

// isCrash reports whether a runtime exit indicates an abnormal termination —
// killed by a signal (SIGSEGV, SIGABRT, SIGKILL, ...) — rather than a clean exit
// with failing assertions. Signal deaths surface as exit codes >= 128. This is
// what separates a "runtime error" from a "failed test" for the verdict.
func isCrash(exitCode int) bool {
	return exitCode >= 128
}

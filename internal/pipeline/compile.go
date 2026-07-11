package pipeline

// compileCommand returns the command that compiles a submission *including its
// tests* but without running them. For Rust it emits structured diagnostics as
// JSON on stdout (consumed by the verifier); `cargo test --no-run` is used so a
// test that fails to compile is caught at this stage. For Go the diagnostics are
// flat text on stderr. Both run fully offline.
func compileCommand(lang Lang) []string {
	switch lang {
	case Go:
		return []string{"go", "build", "./..."}
	default: // Rust
		return []string{"cargo", "test", "--no-run", "--offline", "--message-format=json"}
	}
}

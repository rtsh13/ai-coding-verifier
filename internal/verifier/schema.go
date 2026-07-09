package verifier

// Span identifies a location in source, mapped from a rustc/cargo diagnostic
// span or extracted from Go's flat-text compiler output.
type Span struct {
	File      string
	LineStart int
	LineEnd   int
	ColStart  int
	ColEnd    int
	ByteStart int
	ByteEnd   int
	Label     string
	Primary   bool
}

// Diagnostic is a structured, language-agnostic compiler diagnostic.
//
// For Rust it is a faithful projection of rustc's JSON diagnostic (error code,
// labelled spans, machine-applicable suggestions, and the child-diagnostic
// chain that explains the reasoning). For Go, which exposes no machine-readable
// taxonomy, Code is always empty and the diagnostic is best-effort parsed from
// flat text.
type Diagnostic struct {
	Code        string // e.g. "E0308"; "" when the language exposes no code (Go)
	Level       string // "error" | "warning"
	Message     string
	Spans       []Span
	Suggestions []string // machine-applicable fixes, flattened from spans + children
	Children    []Diagnostic
}

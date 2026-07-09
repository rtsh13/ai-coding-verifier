package verifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "diagnostics", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// primarySpan returns the first primary span in d, or a zero Span with ok=false.
func primarySpan(d Diagnostic) (Span, bool) {
	for _, s := range d.Spans {
		if s.Primary {
			return s, true
		}
	}
	return Span{}, false
}

func TestParseRust_TypeMismatch_E0308(t *testing.T) {
	diags, err := ParseRust(readFixture(t, "e0308_type_mismatch.json"))
	if err != nil {
		t.Fatalf("ParseRust: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Code != "E0308" {
		t.Errorf("Code = %q, want E0308", d.Code)
	}
	if d.Level != "error" {
		t.Errorf("Level = %q, want error", d.Level)
	}
	if d.Message != "mismatched types" {
		t.Errorf("Message = %q, want \"mismatched types\"", d.Message)
	}
	ps, ok := primarySpan(d)
	if !ok {
		t.Fatalf("no primary span")
	}
	if !strings.HasSuffix(ps.File, "main.rs") {
		t.Errorf("primary span File = %q, want *main.rs", ps.File)
	}
	if ps.LineStart != 1 || ps.ColStart != 27 {
		t.Errorf("primary span at %d:%d, want 1:27", ps.LineStart, ps.ColStart)
	}
	if !strings.Contains(ps.Label, "expected `i32`") {
		t.Errorf("primary span Label = %q, want it to mention expected `i32`", ps.Label)
	}
}

func TestParseRust_MovedValue_E0382_HasChildSuggestion(t *testing.T) {
	diags, err := ParseRust(readFixture(t, "e0382_moved_value.json"))
	if err != nil {
		t.Fatalf("ParseRust: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Code != "E0382" {
		t.Errorf("Code = %q, want E0382", d.Code)
	}
	if !strings.Contains(d.Message, "borrow of moved value") {
		t.Errorf("Message = %q, want it to mention borrow of moved value", d.Message)
	}
	if len(d.Children) == 0 {
		t.Errorf("want at least one child diagnostic (the move-chain explanation)")
	}
	// The machine-applicable fix (`.clone()`) lives in a child span; it must be surfaced.
	var found bool
	for _, s := range d.Suggestions {
		if strings.Contains(s, ".clone()") {
			found = true
		}
	}
	if !found {
		t.Errorf("Suggestions = %v, want one containing .clone()", d.Suggestions)
	}
}

func TestParseRust_BorrowConflict_E0502(t *testing.T) {
	diags, err := ParseRust(readFixture(t, "e0502_borrow_conflict.json"))
	if err != nil {
		t.Fatalf("ParseRust: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "E0502" {
		t.Fatalf("want single E0502 diagnostic, got %+v", diags)
	}
}

func TestParseRust_PassingBuild_NoDiagnostics(t *testing.T) {
	diags, err := ParseRust(readFixture(t, "passing_build.json"))
	if err != nil {
		t.Fatalf("ParseRust: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("want 0 diagnostics for a clean build, got %d: %+v", len(diags), diags)
	}
}

func TestParseRust_EmptyInput(t *testing.T) {
	diags, err := ParseRust("")
	if err != nil {
		t.Fatalf("ParseRust(\"\"): %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("want 0 diagnostics for empty input, got %d", len(diags))
	}
}

func TestParseGo_FlatText(t *testing.T) {
	stderr := "./main.go:12:15: cannot use \"hello\" (untyped string constant) as int value in assignment"
	diags, err := ParseGo(stderr)
	if err != nil {
		t.Fatalf("ParseGo: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Code != "" {
		t.Errorf("Go diagnostics carry no error code; Code = %q, want empty", d.Code)
	}
	ps, ok := primarySpan(d)
	if !ok {
		t.Fatalf("no primary span")
	}
	if ps.LineStart != 12 || ps.ColStart != 15 {
		t.Errorf("span at %d:%d, want 12:15", ps.LineStart, ps.ColStart)
	}
	if !strings.Contains(d.Message, "cannot use") {
		t.Errorf("Message = %q, want it to contain the error text", d.Message)
	}
}

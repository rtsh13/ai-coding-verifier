package verifier

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// raw cargo/rustc JSON shapes (only the fields we consume)

type rustLine struct {
	Reason  string       `json:"reason"`
	Message *rustMessage `json:"message"`
}

type rustMessage struct {
	Code     *rustCode     `json:"code"`
	Level    string        `json:"level"`
	Message  string        `json:"message"`
	Spans    []rustSpan    `json:"spans"`
	Children []rustMessage `json:"children"`
}

type rustCode struct {
	Code string `json:"code"`
}

type rustSpan struct {
	FileName             string  `json:"file_name"`
	ByteStart            int     `json:"byte_start"`
	ByteEnd              int     `json:"byte_end"`
	LineStart            int     `json:"line_start"`
	LineEnd              int     `json:"line_end"`
	ColumnStart          int     `json:"column_start"`
	ColumnEnd            int     `json:"column_end"`
	IsPrimary            bool    `json:"is_primary"`
	Label                string  `json:"label"`
	SuggestedReplacement *string `json:"suggested_replacement"`
}

// ParseRust converts the newline-delimited JSON emitted by
// `cargo build --message-format=json` (equivalently rustc --error-format=json)
// into structured diagnostics. Only top-level errors and warnings are returned;
// the trailing "failure-note" records (the "run rustc --explain" hints) and
// clean builds yield no diagnostics.
func ParseRust(cargoJSON string) ([]Diagnostic, error) {
	var out []Diagnostic
	for _, line := range strings.Split(cargoJSON, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var rl rustLine
		if err := json.Unmarshal([]byte(line), &rl); err != nil {
			// A malformed line is not fatal: skip it and keep parsing the rest.
			continue
		}
		if rl.Reason != "compiler-message" || rl.Message == nil {
			continue
		}
		if rl.Message.Level != "error" && rl.Message.Level != "warning" {
			continue
		}
		out = append(out, convertRust(*rl.Message))
	}
	return out, nil
}

func convertRust(m rustMessage) Diagnostic {
	d := Diagnostic{
		Level:   m.Level,
		Message: m.Message,
	}
	if m.Code != nil {
		d.Code = m.Code.Code
	}
	for _, s := range m.Spans {
		d.Spans = append(d.Spans, Span{
			File:      s.FileName,
			LineStart: s.LineStart,
			LineEnd:   s.LineEnd,
			ColStart:  s.ColumnStart,
			ColEnd:    s.ColumnEnd,
			ByteStart: s.ByteStart,
			ByteEnd:   s.ByteEnd,
			Label:     s.Label,
			Primary:   s.IsPrimary,
		})
	}
	for _, c := range m.Children {
		d.Children = append(d.Children, convertRust(c))
	}
	d.Suggestions = collectSuggestions(m)
	return d
}

// collectSuggestions flattens every machine-applicable replacement from a
// message and its descendants (rustc often puts the actual fix, e.g. `.clone()`,
// on a child "help" diagnostic rather than the top-level error).
func collectSuggestions(m rustMessage) []string {
	var s []string
	for _, sp := range m.Spans {
		if sp.SuggestedReplacement != nil && *sp.SuggestedReplacement != "" {
			s = append(s, *sp.SuggestedReplacement)
		}
	}
	for _, c := range m.Children {
		s = append(s, collectSuggestions(c)...)
	}
	return s
}

var goDiagRe = regexp.MustCompile(`^(.*?):(\d+):(\d+):\s*(.*)$`)

// ParseGo is a best-effort parser for Go's flat, unstructured compiler output.
// Go exposes no machine-readable error taxonomy, so Code is always empty and
// only file/line/column and the message text are recovered.
func ParseGo(stderr string) ([]Diagnostic, error) {
	var out []Diagnostic
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		m := goDiagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		out = append(out, Diagnostic{
			Level:   "error",
			Message: m[4],
			Spans: []Span{{
				File:      m[1],
				LineStart: lineNo,
				LineEnd:   lineNo,
				ColStart:  col,
				ColEnd:    col,
				Primary:   true,
			}},
		})
	}
	return out, nil
}

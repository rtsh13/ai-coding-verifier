// Package dataset converts task_suite records into known-answer evaluation
// cases: for each problem it assembles a compilable Rust project and tags the
// expected verdict, so a bench run can score the verifier's correctness.
//
// It currently supports humaneval-x records, which carry both a canonical
// solution (expected to pass) and a buggy solution (expected to fail) and use
// the standard #[cfg(test)] harness the pipeline runs. mbpp/multipl-e records
// ship no reference solution and a main()-based harness, so they are not
// convertible into known-answer cases here.
package dataset

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Record is the subset of a task_suite JSONL record this package consumes.
type Record struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Prompt string `json:"prompt"`
	Tests  string `json:"tests"`
	Raw    struct {
		CanonicalSolution string `json:"canonical_solution"`
		BuggySolution     string `json:"buggy_solution"`
	} `json:"raw"`
}

// Expected is the verdict a case should produce.
type Expected string

const (
	ExpectPass Expected = "pass" // a correct solution: the verifier must pass it
	ExpectFail Expected = "fail" // a broken solution: the verifier must NOT pass it
)

// Case is one known-answer job: a complete project plus its expected verdict.
type Case struct {
	ID       string
	Files    map[string]string
	Expected Expected
}

// Convert produces the known-answer cases for a record. For a humaneval-x record
// it returns up to two cases (canonical → pass, buggy → fail). It returns nil for
// sources it cannot turn into ground truth.
func Convert(r Record) []Case {
	if r.Source != "humaneval-x" {
		return nil
	}
	var cases []Case
	if s := r.Raw.CanonicalSolution; s != "" {
		cases = append(cases, Case{ID: r.ID + "#canonical", Files: assemble(r, s), Expected: ExpectPass})
	}
	if s := r.Raw.BuggySolution; s != "" {
		cases = append(cases, Case{ID: r.ID + "#buggy", Files: assemble(r, s), Expected: ExpectFail})
	}
	return cases
}

// assemble builds a cargo project: prompt (imports + signature) + solution body +
// tests, with a Cargo.toml declaring whatever external crates the source uses.
func assemble(r Record, body string) map[string]string {
	src := r.Prompt + body + r.Tests
	return map[string]string{
		"src/main.rs": src,
		"Cargo.toml":  cargoToml(DetectCrates(src)),
	}
}

var (
	useStmt  = regexp.MustCompile(`(?m)^\s*use\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	builtins = map[string]bool{"std": true, "core": true, "alloc": true, "crate": true, "self": true, "super": true}
)

// DetectCrates returns the external crate names referenced by `use` statements in
// src (excluding std/core/alloc and path keywords), sorted and de-duplicated.
func DetectCrates(src string) []string {
	seen := map[string]bool{}
	var crates []string
	for _, m := range useStmt.FindAllStringSubmatch(src, -1) {
		c := m[1]
		if builtins[c] || seen[c] {
			continue
		}
		seen[c] = true
		crates = append(crates, c)
	}
	sort.Strings(crates)
	return crates
}

func cargoToml(crates []string) string {
	var b strings.Builder
	b.WriteString("[package]\nname = \"submission\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\n")
	for _, c := range crates {
		// "*" resolves to the single vendored version under offline source
		// replacement; pin here if a problem ever needs a specific version.
		fmt.Fprintf(&b, "%s = \"*\"\n", c)
	}
	return b.String()
}

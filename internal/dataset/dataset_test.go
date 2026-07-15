package dataset

import (
	"reflect"
	"strings"
	"testing"
)

func heRecord() Record {
	r := Record{
		ID:     "humaneval-x/Rust/1",
		Source: "humaneval-x",
		Prompt: "fn main(){}\nuse regex::Regex;\nuse md5;\nfn add(a:i32,b:i32)->i32{\n",
		Tests:  "\n#[cfg(test)]\nmod tests { #[test] fn t(){ assert_eq!(super::add(2,2),4); } }\n",
	}
	r.Raw.CanonicalSolution = "    a + b\n}\n"
	r.Raw.BuggySolution = "    a - b\n}\n"
	return r
}

func TestConvert_HumanEvalX_TwoCases(t *testing.T) {
	cases := Convert(heRecord())
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2 (canonical + buggy)", len(cases))
	}
	if cases[0].Expected != ExpectPass || !strings.HasSuffix(cases[0].ID, "#canonical") {
		t.Errorf("case 0 = %+v, want canonical/pass", cases[0])
	}
	if cases[1].Expected != ExpectFail || !strings.HasSuffix(cases[1].ID, "#buggy") {
		t.Errorf("case 1 = %+v, want buggy/fail", cases[1])
	}
}

func TestConvert_AssemblesFullSource(t *testing.T) {
	c := Convert(heRecord())[0] // canonical
	src := c.Files["src/main.rs"]
	// prompt + body + tests, in order.
	if !strings.Contains(src, "fn add(a:i32,b:i32)->i32{") || !strings.Contains(src, "a + b") ||
		!strings.Contains(src, "#[cfg(test)]") {
		t.Errorf("assembled source missing a piece:\n%s", src)
	}
	// prompt must come before body must come before tests.
	if strings.Index(src, "->i32{") > strings.Index(src, "a + b") {
		t.Errorf("body placed before signature")
	}
	if strings.Index(src, "a + b") > strings.Index(src, "#[cfg(test)]") {
		t.Errorf("tests placed before body")
	}
}

func TestConvert_CargoTomlDeclaresDetectedCrates(t *testing.T) {
	toml := Convert(heRecord())[0].Files["Cargo.toml"]
	if !strings.Contains(toml, "regex = ") || !strings.Contains(toml, "md5 = ") {
		t.Errorf("Cargo.toml should declare regex and md5:\n%s", toml)
	}
	if strings.Contains(toml, "std = ") {
		t.Errorf("std must not be declared as a dependency:\n%s", toml)
	}
}

func TestDetectCrates_ExternalOnly(t *testing.T) {
	src := "use std::collections::HashMap;\nuse regex::Regex;\nuse md5;\nuse rand::Rng;\nuse crate::foo;\n"
	got := DetectCrates(src)
	want := []string{"md5", "rand", "regex"} // sorted; std and crate excluded
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DetectCrates = %v, want %v", got, want)
	}
}

func TestConvert_UnsupportedSourceSkipped(t *testing.T) {
	r := heRecord()
	r.Source = "multipl-e-mbpp"
	if got := Convert(r); got != nil {
		t.Errorf("mbpp should not be convertible; got %d cases", len(got))
	}
}

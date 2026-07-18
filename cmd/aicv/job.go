package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aicv/pkg/api"
)

// minimalCargoToml wraps a bare .rs file into a compilable, dependency-free
// project.
const minimalCargoToml = "[package]\nname = \"submission\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"

// parseLang maps a language name to an api.Lang.
func parseLang(s string) (api.Lang, error) {
	switch strings.ToLower(s) {
	case "rust", "rs", "":
		return api.Rust, nil
	case "go", "golang":
		return api.Go, nil
	default:
		return api.Rust, fmt.Errorf("unknown language %q (want rust or go)", s)
	}
}

// jobFromPath builds a Job from either a project directory or a single .rs file.
func jobFromPath(path string, lang api.Lang, ttl time.Duration) (api.Job, error) {
	info, err := os.Stat(path)
	if err != nil {
		return api.Job{}, err
	}
	files := map[string][]byte{}
	if info.IsDir() {
		files, err = readProjectDir(path)
		if err != nil {
			return api.Job{}, err
		}
	} else {
		body, err := os.ReadFile(path)
		if err != nil {
			return api.Job{}, err
		}
		files["Cargo.toml"] = []byte(minimalCargoToml)
		files["src/main.rs"] = body
	}
	return api.Job{Lang: lang, Files: files, TTL: ttl}, nil
}

// readProjectDir reads every file under dir into a relative-path->content map,
// skipping build output (target/) and VCS metadata.
func readProjectDir(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "target" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found under %s", dir)
	}
	return files, nil
}

// jobSpec is one line of a bench JSONL file: a self-contained job.
type jobSpec struct {
	ID      string            `json:"id"`
	Lang    string            `json:"lang"`
	Files   map[string]string `json:"files"`
	TTLSecs int               `json:"ttl_secs"`
}

// job converts a spec into an api.Job (applying a default TTL).
func (s jobSpec) job(defaultTTL time.Duration) (api.Job, error) {
	lang, err := parseLang(s.Lang)
	if err != nil {
		return api.Job{}, err
	}
	if len(s.Files) == 0 {
		return api.Job{}, fmt.Errorf("job %q has no files", s.ID)
	}
	files := make(map[string][]byte, len(s.Files))
	for k, v := range s.Files {
		files[k] = []byte(v)
	}
	ttl := defaultTTL
	if s.TTLSecs > 0 {
		ttl = time.Duration(s.TTLSecs) * time.Second
	}
	return api.Job{Lang: lang, Files: files, TTL: ttl}, nil
}

// readJobSpecs parses a bench JSONL stream, one jobSpec per non-empty line.
func readJobSpecs(r io.Reader) ([]jobSpec, error) {
	var specs []jobSpec
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024) // allow large lines
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var s jobSpec
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		specs = append(specs, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return specs, nil
}

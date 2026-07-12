// Command aicv is the terminal driver for the verifier: `aicv verify <path>`
// checks a single submission, and `aicv bench <jsonl>` runs a whole workload
// through the pool and reports outcomes and latencies.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/aicv/internal/verdict"
	"github.com/aicv/internal/verifier"
	"github.com/aicv/pkg/api"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		cmdVerify(os.Args[2:])
	case "bench":
		cmdBench(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  aicv verify [--lang rust|go] [--image NAME] [--ttl SECS] <path>
  aicv bench  [--image NAME] [--concurrency N] [--ttl SECS] [--out FILE] <jsonl>`)
}

func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	langName := fs.String("lang", "rust", "submission language")
	image := fs.String("image", "rust-sandbox", "sandbox image")
	ttlSecs := fs.Int("ttl", 60, "per-job wall-clock limit, seconds")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fatal("verify: expected exactly one <path>")
	}

	lang, err := parseLang(*langName)
	must(err)
	job, err := jobFromPath(fs.Arg(0), lang, time.Duration(*ttlSecs)*time.Second)
	must(err)

	env := mustEnv(api.Config{Image: *image, MinWarm: 1, MaxSize: 2})
	v, err := env.Verify(context.Background(), job)
	// Close explicitly: the os.Exit below would skip a deferred Close and leak
	// containers.
	_ = env.Close(context.Background())
	must(err)

	printVerdict(fs.Arg(0), v)
	if v.Outcome != verdict.Passed {
		os.Exit(1)
	}
}

func printVerdict(name string, v api.Verdict) {
	fmt.Printf("%s\n  outcome:  %s  (%s)\n", name, v.Outcome, v.Duration.Round(time.Millisecond))
	if len(v.Diagnostics) > 0 {
		fmt.Println("  diagnostics:")
		for _, d := range v.Diagnostics {
			printDiag(d)
		}
	}
	if v.Outcome != verdict.Passed && v.Stdout != "" {
		fmt.Printf("  stdout:\n%s\n", indent(v.Stdout))
	}
}

func printDiag(d verifier.Diagnostic) {
	loc := ""
	for _, s := range d.Spans {
		if s.Primary {
			loc = fmt.Sprintf(" %s:%d:%d", s.File, s.LineStart, s.ColStart)
			break
		}
	}
	code := d.Code
	if code == "" {
		code = d.Level
	}
	fmt.Printf("    [%s]%s — %s\n", code, loc, d.Message)
}

func cmdBench(args []string) {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	image := fs.String("image", "rust-sandbox", "sandbox image")
	concurrency := fs.Int("concurrency", 4, "parallel jobs / pool size")
	ttlSecs := fs.Int("ttl", 60, "default per-job wall-clock limit, seconds")
	out := fs.String("out", "", "write per-job results as JSONL to this file (default: stdout)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fatal("bench: expected exactly one <jsonl>")
	}

	f, err := os.Open(fs.Arg(0))
	must(err)
	specs, err := readJobSpecs(f)
	_ = f.Close()
	must(err)
	if len(specs) == 0 {
		fatal("bench: no jobs in " + fs.Arg(0))
	}

	// Open the output sink before starting the env so an open error can't leak
	// containers (a fatal exit skips the deferred Close).
	w := os.Stdout
	if *out != "" {
		wf, err := os.Create(*out)
		must(err)
		defer wf.Close()
		w = wf
	}

	env := mustEnv(api.Config{Image: *image, MinWarm: *concurrency, MaxSize: *concurrency})
	defer env.Close(context.Background())

	results := runBench(env, specs, *concurrency, time.Duration(*ttlSecs)*time.Second)
	enc := json.NewEncoder(w)
	for _, r := range results {
		_ = enc.Encode(r)
	}
	printSummary(os.Stderr, results)
}

type benchResult struct {
	ID         string `json:"id"`
	Outcome    string `json:"outcome"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	Error      string `json:"error,omitempty"`
}

// runBench verifies every spec using a worker pool of the given size.
func runBench(env *api.Env, specs []jobSpec, workers int, defaultTTL time.Duration) []benchResult {
	if workers < 1 {
		workers = 1
	}
	results := make([]benchResult, len(specs))
	idxs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idxs {
				results[i] = verifyOne(env, specs[i], defaultTTL)
			}
		}()
	}
	for i := range specs {
		idxs <- i
	}
	close(idxs)
	wg.Wait()
	return results
}

func verifyOne(env *api.Env, spec jobSpec, defaultTTL time.Duration) benchResult {
	r := benchResult{ID: spec.ID}
	job, err := spec.job(defaultTTL)
	if err != nil {
		r.Outcome, r.Error = "error", err.Error()
		return r
	}
	start := time.Now()
	v, err := env.Verify(context.Background(), job)
	r.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		r.Outcome, r.Error = "error", err.Error()
		return r
	}
	r.Outcome = v.Outcome.String()
	r.TimedOut = v.TimedOut
	return r
}

func printSummary(w *os.File, results []benchResult) {
	counts := map[string]int{}
	durs := make([]int64, 0, len(results))
	for _, r := range results {
		counts[r.Outcome]++
		if r.Error == "" {
			durs = append(durs, r.DurationMs)
		}
	}
	fmt.Fprintf(w, "\n%d jobs\n", len(results))
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-14s %d\n", k, counts[k])
	}
	if len(durs) > 0 {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		var sum int64
		for _, d := range durs {
			sum += d
		}
		idx := (95 * len(durs)) / 100
		if idx >= len(durs) {
			idx = len(durs) - 1
		}
		fmt.Fprintf(w, "  latency        mean %dms  p95 %dms  max %dms\n",
			sum/int64(len(durs)), durs[idx], durs[len(durs)-1])
	}
}

func indent(s string) string {
	out := ""
	for _, line := range splitLines(s) {
		out += "    " + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func mustEnv(cfg api.Config) *api.Env {
	env, err := api.NewEnv(cfg)
	if err != nil {
		fatal("could not start verifier: " + err.Error())
	}
	return env
}

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "aicv: "+msg)
	os.Exit(1)
}

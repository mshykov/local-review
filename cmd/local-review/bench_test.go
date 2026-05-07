package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mshykov/local-review/internal/bench"
)

// TestBench_ReplayEndToEnd exercises the cobra command path against
// the in-repo dataset and fixtures. CI runs `local-review bench
// --replay bench/fixtures` against the same files; this test catches
// "the dataset disagrees with its fixtures" regressions before they
// hit a remote workflow.
func TestBench_ReplayEndToEnd(t *testing.T) {
	repoRoot := findRepoRoot(t)
	dataset := filepath.Join(repoRoot, "bench", "dataset")
	fixtures := filepath.Join(repoRoot, "bench", "fixtures")

	if _, err := os.Stat(dataset); err != nil {
		t.Skipf("bench dataset not present at %s: %v", dataset, err)
	}
	if _, err := os.Stat(fixtures); err != nil {
		t.Skipf("bench fixtures not present at %s: %v", fixtures, err)
	}

	cases, err := bench.LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(cases) < 1 {
		t.Fatalf("expected at least one case in dataset, got %d", len(cases))
	}

	llms, err := pickBenchLLMs(benchFlags{replayDir: fixtures})
	if err != nil {
		t.Fatalf("pickBenchLLMs: %v", err)
	}

	rep, err := bench.Run(context.Background(), cases, bench.Options{
		LLMs:      llms,
		Source:    bench.SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("bench.Run: %v", err)
	}
	rep.Dataset = dataset

	// The text report should mention each LLM and the dataset path.
	var buf bytes.Buffer
	if err := bench.WriteText(&buf, rep); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	for _, want := range []string{"claude", "codex", "gemini", "Bench:"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("text report missing %q\n%s", want, buf.String())
		}
	}

	// The JSON report should round-trip cleanly.
	var jbuf bytes.Buffer
	if err := bench.WriteJSON(&jbuf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var decoded bench.Report
	if err := json.Unmarshal(jbuf.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON did not round-trip: %v", err)
	}
	if decoded.CaseCount != len(cases) {
		t.Errorf("decoded case_count=%d want %d", decoded.CaseCount, len(cases))
	}
	if len(decoded.LLMReports) != len(llms) {
		t.Errorf("decoded llm count=%d want %d", len(decoded.LLMReports), len(llms))
	}

	// At least one LLM should achieve perfect F1 on at least one case.
	// Without this assertion the test passes even if the parser is
	// silently broken and every match returns zeros.
	foundPerfect := false
	for _, lr := range decoded.LLMReports {
		for _, cs := range lr.Cases {
			if cs.TruePositives > 0 && cs.FalsePositives == 0 && cs.FalseNegatives == 0 {
				foundPerfect = true
				break
			}
		}
		if foundPerfect {
			break
		}
	}
	if !foundPerfect {
		t.Errorf("expected at least one (LLM, case) pair with perfect TP+0FP+0FN; got %s", buf.String())
	}
}

// findRepoRoot walks up from the test binary's CWD until it finds a
// go.mod file. Necessary because `go test ./cmd/local-review/...`
// runs with CWD set to the package directory, not the repo root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s upward", dir)
		}
		dir = parent
	}
}

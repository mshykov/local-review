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
	"github.com/mshykov/local-review/internal/cli"
)

// TestBench_ReplayEndToEnd exercises the cobra command path against
// the in-repo dataset and fixtures. CI runs `local-review bench
// --replay bench/fixtures` against the same files; this test catches
// "the dataset disagrees with its fixtures" regressions before they
// hit a remote workflow.
//
// Body is decomposed into setup → run → assert helpers so each step
// stays under SonarCloud's cognitive-complexity budget and the
// failure modes are obvious from the helper names.
func TestBench_ReplayEndToEnd(t *testing.T) {
	dataset, fixtures := benchDatasetPaths(t)
	cases, llms := loadBenchTestInputs(t, dataset, fixtures)
	rep := runBenchReplayForTest(t, cases, llms, fixtures, dataset)

	textOut := assertTextReport(t, rep)
	assertJSONRoundTrip(t, rep, len(cases), len(llms))
	assertAtLeastOnePerfectCase(t, rep, textOut)
}

func TestPickBenchLLMs_ReplayOnlyDedupes(t *testing.T) {
	llms, err := pickBenchLLMs(benchFlags{
		replayDir: "fixtures",
		only:      "claude, codex,claude,gemini,codex",
	})
	if err != nil {
		t.Fatalf("pickBenchLLMs: %v", err)
	}
	got := make([]string, 0, len(llms))
	for _, l := range llms {
		got = append(got, l.Name)
	}
	want := []string{"claude", "codex", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("llm count=%d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("llms[%d]=%q want %q (%v)", i, got[i], want[i], got)
		}
	}
}

// benchDatasetPaths returns the on-disk paths to the in-repo dataset
// and fixtures, skipping the test entirely if either is missing
// (e.g., a stripped checkout for embedded-binary builds).
func benchDatasetPaths(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := findRepoRoot(t)
	dataset := filepath.Join(repoRoot, "bench", "dataset")
	fixtures := filepath.Join(repoRoot, "bench", "fixtures")
	if _, err := os.Stat(dataset); err != nil {
		t.Skipf("bench dataset not present at %s: %v", dataset, err)
	}
	if _, err := os.Stat(fixtures); err != nil {
		t.Skipf("bench fixtures not present at %s: %v", fixtures, err)
	}
	return dataset, fixtures
}

// loadBenchTestInputs loads the cases and LLM stubs the same way the
// CLI does, with t.Fatal on any setup failure.
func loadBenchTestInputs(t *testing.T, dataset, fixtures string) ([]bench.Case, []cli.LLM) {
	t.Helper()
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
	return cases, llms
}

// runBenchReplayForTest runs bench.Run in replay mode and returns
// the report stamped with the dataset path (matching what the CLI
// does before emitting).
func runBenchReplayForTest(t *testing.T, cases []bench.Case, llms []cli.LLM, fixtures, dataset string) bench.Report {
	t.Helper()
	rep, err := bench.Run(context.Background(), cases, bench.Options{
		LLMs:      llms,
		Source:    bench.SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("bench.Run: %v", err)
	}
	rep.Dataset = dataset
	return rep
}

// assertTextReport writes the text-format report to a buffer,
// confirms each LLM name and the "Bench:" header appear, and
// returns the buffer contents for callers that want it for
// diagnostic dumps.
func assertTextReport(t *testing.T, rep bench.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := bench.WriteText(&buf, rep); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	for _, want := range []string{"claude", "codex", "gemini", "Bench:"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("text report missing %q\n%s", want, buf.String())
		}
	}
	return buf.String()
}

// assertJSONRoundTrip writes the JSON report, decodes it back, and
// confirms the case + LLM counts match the inputs we ran.
func assertJSONRoundTrip(t *testing.T, rep bench.Report, wantCases, wantLLMs int) {
	t.Helper()
	var jbuf bytes.Buffer
	if err := bench.WriteJSON(&jbuf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var decoded bench.Report
	if err := json.Unmarshal(jbuf.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON did not round-trip: %v", err)
	}
	if decoded.CaseCount != wantCases {
		t.Errorf("decoded case_count=%d want %d", decoded.CaseCount, wantCases)
	}
	if len(decoded.LLMReports) != wantLLMs {
		t.Errorf("decoded llm count=%d want %d", len(decoded.LLMReports), wantLLMs)
	}
}

// assertAtLeastOnePerfectCase ensures at least one (LLM, case) pair
// has TP>0, FP=0, FN=0 — the parser-not-silently-broken canary.
// Without this, the test would pass even if every match returned
// zeros across the board.
func assertAtLeastOnePerfectCase(t *testing.T, rep bench.Report, dumpOnFail string) {
	t.Helper()
	for _, lr := range rep.LLMReports {
		for _, cs := range lr.Cases {
			if cs.TruePositives > 0 && cs.FalsePositives == 0 && cs.FalseNegatives == 0 {
				return
			}
		}
	}
	t.Errorf("expected at least one (LLM, case) pair with perfect TP+0FP+0FN; got %s", dumpOnFail)
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

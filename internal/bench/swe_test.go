package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

// TestScoreSWE_CaseInsensitiveSubstringMatch is the v1 scorer's core
// contract: a finding that mentions ANY of the expected_keywords
// (case-insensitive substring) counts the task as caught. The
// matched-keywords list captures which fired so reviewers can spot
// over-generic keywords.
func TestScoreSWE_CaseInsensitiveSubstringMatch(t *testing.T) {
	c := SWEBenchCase{
		ID:               "t1",
		ExpectedKeywords: []string{"off-by-one", "missing item", "boundary"},
	}
	// Mixed-case finding, mentions one keyword.
	s := ScoreSWE(c, "## Major Issues\n\n- The loop has an OFF-BY-ONE error on line 42.\n")
	if !s.Caught {
		t.Errorf("expected caught=true; got %+v", s)
	}
	if len(s.MatchedKeywords) != 1 || s.MatchedKeywords[0] != "off-by-one" {
		t.Errorf("expected matched=[off-by-one]; got %v", s.MatchedKeywords)
	}
}

// TestScoreSWE_MissesWhenNoKeywordPresent verifies the negative
// case: a review that talks about the right file but never names
// the bug class is NOT a catch.
func TestScoreSWE_MissesWhenNoKeywordPresent(t *testing.T) {
	c := SWEBenchCase{
		ID:               "t2",
		ExpectedKeywords: []string{"off-by-one", "missing item"},
	}
	// Finding mentions the file but not the bug class.
	s := ScoreSWE(c, "## Info\n\n- paginator.py looks fine to me.\n")
	if s.Caught {
		t.Errorf("expected caught=false; got %+v", s)
	}
	if len(s.MatchedKeywords) != 0 {
		t.Errorf("expected no matched keywords; got %v", s.MatchedKeywords)
	}
}

// TestScoreSWE_MultipleKeywordsRecordAllMatches confirms the
// matched_keywords list shows every keyword that fired, in
// case.yaml order. Useful for diagnosing over-generic keywords
// that might be false-positive matching on adjacent findings.
func TestScoreSWE_MultipleKeywordsRecordAllMatches(t *testing.T) {
	c := SWEBenchCase{
		ID:               "t3",
		ExpectedKeywords: []string{"off-by-one", "integer division", "math.ceil"},
	}
	out := "Found an off-by-one error here; should use math.ceil instead of integer division."
	s := ScoreSWE(c, out)
	if !s.Caught || len(s.MatchedKeywords) != 3 {
		t.Errorf("expected all three keywords to match; got %+v", s)
	}
}

// TestLoadSWEBenchDataset_HappyPath verifies the loader walks a
// directory of `<id>/case.yaml + diff.patch` pairs and returns
// parsed cases sorted by id.
func TestLoadSWEBenchDataset_HappyPath(t *testing.T) {
	root := t.TempDir()
	mkSWECase(t, root, "alpha", `id: alpha
title: alpha task
language: python
expected_keywords:
  - "alpha-keyword"
`, "diff body alpha")
	mkSWECase(t, root, "beta", `id: beta
title: beta task
language: python
expected_keywords:
  - "beta-keyword"
`, "diff body beta")

	cases, err := LoadSWEBenchDataset(root)
	if err != nil {
		t.Fatalf("LoadSWEBenchDataset: %v", err)
	}
	if len(cases) != 2 || cases[0].ID != "alpha" || cases[1].ID != "beta" {
		t.Errorf("expected alpha,beta sorted; got %+v", cases)
	}
	if cases[0].Diff != "diff body alpha" {
		t.Errorf("diff body for alpha not loaded; got %q", cases[0].Diff)
	}
}

// TestLoadSWEBenchDataset_RejectsEmptyKeywords covers the loud-
// failure invariant: a case.yaml with no expected_keywords would
// silently mark every task as missed (no keyword can fire), which
// would look like a reviewer regression but is actually a
// dataset-curation bug. Loader fails the whole load instead.
func TestLoadSWEBenchDataset_RejectsEmptyKeywords(t *testing.T) {
	root := t.TempDir()
	mkSWECase(t, root, "bad", `id: bad
title: missing keywords
language: python
expected_keywords: []
`, "diff body")
	_, err := LoadSWEBenchDataset(root)
	if err == nil {
		t.Fatal("expected error on empty expected_keywords; got nil")
	}
	if !strings.Contains(err.Error(), "expected_keywords") {
		t.Errorf("error should mention expected_keywords; got %v", err)
	}
}

// TestLoadSWEBenchDataset_RejectsWhitespaceOnlyKeywords confirms
// the loader trims keywords and treats an all-whitespace list the
// same way as a literally-empty one.
func TestLoadSWEBenchDataset_RejectsWhitespaceOnlyKeywords(t *testing.T) {
	root := t.TempDir()
	mkSWECase(t, root, "bad", `id: bad
title: whitespace keywords
language: python
expected_keywords:
  - "   "
  - ""
`, "diff body")
	_, err := LoadSWEBenchDataset(root)
	if err == nil {
		t.Fatal("expected error on whitespace-only expected_keywords; got nil")
	}
}

// TestLoadSWEBenchDataset_SkipsPartialEntries verifies the
// "missing diff.patch OR case.yaml = skip silently" contract.
// Same shape as LoadDataset's partial-entry handling — keeps a
// half-curated task from killing the whole bench run.
func TestLoadSWEBenchDataset_SkipsPartialEntries(t *testing.T) {
	root := t.TempDir()
	mkSWECase(t, root, "complete", `id: complete
title: complete task
expected_keywords:
  - "anything"
`, "diff")
	// Partial entry: yaml only, no diff.
	partialDir := filepath.Join(root, "partial")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "case.yaml"), []byte("id: partial\ntitle: x\nexpected_keywords: [foo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadSWEBenchDataset(root)
	if err != nil {
		t.Fatalf("expected partial entry to be skipped, got error: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "complete" {
		t.Errorf("expected only complete case loaded; got %+v", cases)
	}
}

// TestRunSWE_RejectsUplift covers the v1 mutual-exclusion guard:
// --uplift's treatment-vs-baseline framing doesn't map onto
// binary catch/miss scoring, so we refuse the combination loudly
// instead of producing a misleading number.
func TestRunSWE_RejectsUplift(t *testing.T) {
	_, err := RunSWE(context.Background(), []SWEBenchCase{{ID: "x", ExpectedKeywords: []string{"foo"}}}, Options{
		LLMs:   []cli.LLM{{Name: "claude"}},
		Source: SourceLive,
		Uplift: true,
	})
	if err == nil {
		t.Error("expected error when --uplift is set in swe-bench mode")
	}
}

// TestRunSWE_RejectsRepeatGreaterThanOne covers the other v1
// mutual-exclusion guard: --repeat > 1 is a consistency-measure
// concept that doesn't have a defined v1 semantics for catch
// scoring (caught-if-any vs caught-if-all is contentious enough
// to defer).
func TestRunSWE_RejectsRepeatGreaterThanOne(t *testing.T) {
	_, err := RunSWE(context.Background(), []SWEBenchCase{{ID: "x", ExpectedKeywords: []string{"foo"}}}, Options{
		LLMs:   []cli.LLM{{Name: "claude"}},
		Source: SourceLive,
		Repeat: 3,
	})
	if err == nil {
		t.Error("expected error when --repeat > 1 in swe-bench mode")
	}
}

// TestRunSWE_ReplayModeEndToEnd exercises the full replay path:
// fixture markdown that contains the keyword → caught; fixture
// without the keyword → missed; missing fixture → error frame.
// Catch rate denominator includes the error frame, per the
// "broken reviewer catches no bugs" invariant.
func TestRunSWE_ReplayModeEndToEnd(t *testing.T) {
	fixtures := t.TempDir()
	// Caught fixture
	mkSWEFixture(t, fixtures, "task-1", "claude", "## Major\n- off-by-one error in paginator")
	// Missed fixture
	mkSWEFixture(t, fixtures, "task-2", "claude", "## Info\n- looks fine")
	// task-3 has no fixture — should error.

	cases := []SWEBenchCase{
		{ID: "task-1", ExpectedKeywords: []string{"off-by-one"}},
		{ID: "task-2", ExpectedKeywords: []string{"off-by-one"}},
		{ID: "task-3", ExpectedKeywords: []string{"off-by-one"}},
	}
	rep, err := RunSWE(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("RunSWE: %v", err)
	}
	if len(rep.LLMReports) != 1 {
		t.Fatalf("expected one LLM report; got %d", len(rep.LLMReports))
	}
	lr := rep.LLMReports[0]
	if lr.Tasks != 3 {
		t.Errorf("Tasks = %d, want 3", lr.Tasks)
	}
	if lr.CaughtCount != 1 {
		t.Errorf("CaughtCount = %d, want 1 (only task-1)", lr.CaughtCount)
	}
	if lr.Errors != 1 {
		t.Errorf("Errors = %d, want 1 (task-3 fixture missing)", lr.Errors)
	}
	// Catch rate uses Tasks (3), not Tasks-Errors (2), as denominator:
	// 1 / 3 ≈ 0.3333. A reviewer that crashes catches no bugs.
	if got := lr.CatchRate; got < 0.33 || got > 0.34 {
		t.Errorf("CatchRate = %v, want ~0.3333 (1/3, including error frame in denominator)", got)
	}
}

// TestWriteTextSWE_Format confirms the text renderer produces the
// columns we promise in the docstring: LLM, Tasks, Caught, Missed,
// Errors, Catch rate.
func TestWriteTextSWE_Format(t *testing.T) {
	rep := SWEBenchReport{
		Dataset:   "bench/swe-bench-lite",
		CaseCount: 3,
		Mode:      "replay",
		Generated: time.Now(),
		LLMReports: []SWEBenchLLMReport{
			{
				LLM:         "claude",
				Tasks:       3,
				CaughtCount: 2,
				Errors:      0,
				CatchRate:   2.0 / 3.0,
				Cases: []SWEBenchScore{
					{CaseID: "task-1", Caught: true, MatchedKeywords: []string{"off-by-one"}},
					{CaseID: "task-2", Caught: true, MatchedKeywords: []string{"missing"}},
					{CaseID: "task-3", Caught: false},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteTextSWE(&buf, rep); err != nil {
		t.Fatalf("WriteTextSWE: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Bench (swe-bench-lite):",
		"dataset=bench/swe-bench-lite",
		"LLM", "Tasks", "Caught", "Missed", "Errors", "Catch rate",
		"claude",
		"67%", // 2/3 ≈ 0.667 → "67%"
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestWriteMarkdownSWE_Format confirms the markdown renderer
// produces the section header + table + per-task detail we
// promise in the docstring. The catch-rate column reads "67%" not
// "0.67" so a committed RESULTS.md scans cleanly.
func TestWriteMarkdownSWE_Format(t *testing.T) {
	rep := SWEBenchReport{
		Dataset:   "bench/swe-bench-lite",
		CaseCount: 3,
		Mode:      "replay",
		Generated: time.Now(),
		LLMReports: []SWEBenchLLMReport{
			{
				LLM:         "claude",
				Tasks:       3,
				CaughtCount: 2,
				Errors:      0,
				CatchRate:   2.0 / 3.0,
				Cases: []SWEBenchScore{
					{CaseID: "task-1", Caught: true},
					{CaseID: "task-2", Caught: true},
					{CaseID: "task-3", Caught: false},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdownSWE(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdownSWE: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"## SWE-bench-lite catch rate",
		"| LLM | Tasks | Caught | Missed | Errors | Catch rate |",
		"| claude | 3 | 2 | 1 | 0 | 67% |",
		"### Per-task detail",
		"| task-1 |",
		"✓", // caught marker
		"✗", // missed marker
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestWriteJSONSWE_RoundTrip ensures the report serialises to JSON
// and deserialises back without losing the headline fields.
// Catches accidental tag drift on future schema changes.
func TestWriteJSONSWE_RoundTrip(t *testing.T) {
	rep := SWEBenchReport{
		Dataset:   "bench/swe-bench-lite",
		CaseCount: 2,
		Mode:      "replay",
		Generated: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		LLMReports: []SWEBenchLLMReport{
			{
				LLM:         "claude",
				Tasks:       2,
				CaughtCount: 1,
				CatchRate:   0.5,
				Cases: []SWEBenchScore{
					{CaseID: "t1", LLM: "claude", Caught: true, MatchedKeywords: []string{"k"}},
					{CaseID: "t2", LLM: "claude", Caught: false},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSONSWE(&buf, rep); err != nil {
		t.Fatalf("WriteJSONSWE: %v", err)
	}
	var back SWEBenchReport
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.LLMReports[0].CatchRate != 0.5 || back.LLMReports[0].CaughtCount != 1 {
		t.Errorf("round-trip lost data: %+v", back.LLMReports[0])
	}
}

// mkSWECase writes <root>/<id>/{case.yaml, diff.patch} for tests.
func mkSWECase(t *testing.T, root, id, caseYAML, diffBody string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case.yaml"), []byte(caseYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), []byte(diffBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkSWEFixture writes <root>/<id>/<llm>.md for replay-mode tests.
func mkSWEFixture(t *testing.T, root, id, llm, body string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, llm+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

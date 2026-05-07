package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mshykov/local-review/internal/cli"
)

func TestRun_ReplayMode_EndToEnd(t *testing.T) {
	dataset := t.TempDir()
	mkCase(t, dataset, "go-bug-1", `id: go-bug-1
title: nil deref
language: go
expected:
  - file: foo.go
    line: 10
    note: nil deref after err check
`)
	mkCase(t, dataset, "clean-1", `id: clean-1
title: a clean diff
language: go
clean: true
`)

	fixtures := t.TempDir()
	mkFixture(t, fixtures, "go-bug-1", "claude", "## Major Issues\n\n- foo.go:11 — possible nil deref\n")
	mkFixture(t, fixtures, "go-bug-1", "gemini", "## Warnings\n\n- bar.go:99 — irrelevant\n")
	mkFixture(t, fixtures, "clean-1", "claude", "No issues found.\n")
	mkFixture(t, fixtures, "clean-1", "gemini", "## Warnings\n\n- a.go:1 — spurious\n- a.go:2 — spurious\n")

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}, {Name: "gemini"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rep.CaseCount != 2 || rep.Mode != "replay" || len(rep.LLMReports) != 2 {
		t.Fatalf("report shape unexpected: %+v", rep)
	}

	claude := rep.LLMReports[0]
	if claude.LLM != "claude" {
		t.Fatalf("expected claude first (alphabetical), got %s", claude.LLM)
	}
	// claude caught the bug (within ±3 of line 10) and produced no
	// findings on the clean case → precision=recall=F1=1.0, noise=0.
	if claude.Precision != 1.0 || claude.Recall != 1.0 || claude.F1 != 1.0 {
		t.Errorf("claude scores: P=%v R=%v F1=%v", claude.Precision, claude.Recall, claude.F1)
	}
	if claude.NoiseRate != 0 {
		t.Errorf("claude noise: got %v want 0", claude.NoiseRate)
	}

	gemini := rep.LLMReports[1]
	// gemini missed the real bug AND produced 2 spurious findings on
	// the clean case → recall=0, noise=2.
	if gemini.Recall != 0 {
		t.Errorf("gemini recall: got %v want 0", gemini.Recall)
	}
	if gemini.NoiseRate != 2.0 {
		t.Errorf("gemini noise: got %v want 2.0", gemini.NoiseRate)
	}
}

func TestRun_ReplayMode_MissingFixtureRecordedAsError(t *testing.T) {
	dataset := t.TempDir()
	mkCase(t, dataset, "case-x", `id: case-x
title: x
language: go
expected:
  - file: x.go
    line: 1
`)

	fixtures := t.TempDir() // no fixture written

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(rep.LLMReports) != 1 || len(rep.LLMReports[0].Cases) != 1 {
		t.Fatalf("report shape: %+v", rep)
	}
	cs := rep.LLMReports[0].Cases[0]
	if cs.Error == "" {
		t.Errorf("missing fixture should record error on the CaseScore, got %+v", cs)
	}
}

func TestRun_NoLLMsIsError(t *testing.T) {
	if _, err := Run(context.Background(), []Case{{ID: "x"}}, Options{}); err == nil {
		t.Error("expected error when LLMs slice is empty")
	}
}

func TestRun_ReplayWithoutDirIsError(t *testing.T) {
	_, err := Run(context.Background(), []Case{{ID: "x"}}, Options{
		LLMs:   []cli.LLM{{Name: "claude"}},
		Source: SourceReplay,
	})
	if err == nil {
		t.Error("expected error when replay mode is selected without a fixtures dir")
	}
}

func mkCase(t *testing.T, root, id, yaml string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), []byte("placeholder diff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkFixture(t *testing.T, root, caseID, llmName, body string) {
	t.Helper()
	dir := filepath.Join(root, caseID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, llmName+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

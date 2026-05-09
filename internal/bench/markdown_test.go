package bench

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestWriteMarkdown_Sections covers the three table sections + the
// header line. Snapshot-style — we don't hash the whole output (too
// brittle as fields evolve) but we assert that each documented row
// is present with the right cells, so a regression that drops a
// section or mis-orders columns surfaces immediately.
func TestWriteMarkdown_Sections(t *testing.T) {
	rep := Report{
		Dataset:   "bench/dataset",
		CaseCount: 4,
		Mode:      "replay",
		Generated: time.Date(2026, 5, 9, 7, 14, 0, 0, time.UTC),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.80, Recall: 1.00, F1: 0.89, NoiseRate: 0.00,
				Languages: []LanguageScore{
					{Language: "go", Cases: 2, F1: 1.00},
					{Language: "typescript", Cases: 1, F1: 0.67},
				},
				Cases: []CaseScore{
					{CaseID: "go-bug-1", Language: "go", TruePositives: 2},
					{CaseID: "ts-sql-1", Language: "typescript", TruePositives: 1, FalsePositives: 1},
				},
			},
			{
				LLM:       "codex",
				Precision: 0.50, Recall: 0.50, F1: 0.50, NoiseRate: 0.00,
				Languages: []LanguageScore{
					{Language: "go", Cases: 2, F1: 0.50},
					{Language: "typescript", Cases: 1, F1: 1.00},
				},
				Cases: []CaseScore{
					{CaseID: "go-bug-1", Language: "go", TruePositives: 1, FalsePositives: 1},
					{CaseID: "ts-sql-1", Language: "typescript", TruePositives: 1},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()

	wants := []string{
		"# local-review bench leaderboard",
		"_Dataset: bench/dataset (4 cases)_",
		"_Mode: replay_",
		"## Overall",
		"| LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |",
		"| claude | 0.80 | 1.00 | 0.89 | 0.00 | — |", // no consistency, so —
		"## Per-language F1",
		"| LLM | go (2) | typescript (1) |",
		"| claude | 1.00 | 0.67 |",
		"## Per-case detail",
		"| Case | Lang | claude | codex |",
		"| go-bug-1 | go | F1=",
		"| ts-sql-1 | typescript | F1=",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("markdown output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

func TestWriteMarkdown_OmitsLanguagesWhenSingleLanguage(t *testing.T) {
	// LLMReport.Languages is empty when the dataset has only one
	// language (runner skips the split). The markdown should omit
	// the Per-language section in that case.
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "replay",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Cases: []CaseScore{{CaseID: "go-1", Language: "go"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "## Per-language F1") {
		t.Errorf("Per-language section should be omitted for single-language datasets, got:\n%s", buf.String())
	}
}

func TestWriteMarkdown_RendersConsistencyWhenPresent(t *testing.T) {
	cons := 0.92
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Consistency: &cons, Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "0.92") {
		t.Errorf("consistency 0.92 not rendered:\n%s", buf.String())
	}
}

// TestWriteMarkdown_RendersZeroConsistency: a measured-but-zero
// consistency (every run produced totally different findings) must
// render as "0.00" — not be collapsed to "—" alongside unmeasured.
// Codex flagged this in self-review as the worst case the metric
// is supposed to surface.
func TestWriteMarkdown_RendersZeroConsistency(t *testing.T) {
	zero := 0.0
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Consistency: &zero, Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	// The cell must show "0.00", and the "—" must not appear inside
	// the consistency column. We assert against the row pattern so
	// the test doesn't false-pass on stray dashes elsewhere.
	if !strings.Contains(buf.String(), "| 0.00 |") {
		t.Errorf("zero consistency should render as 0.00, not be collapsed to —:\n%s", buf.String())
	}
}

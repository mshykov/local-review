package multi

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
	"unicode/utf8"
)

func TestBuildMergeInput_TruncatesOversizeReviews(t *testing.T) {
	// A verbose or hallucinating reviewer can dump a multi-megabyte
	// payload that would blow the merger's context window if we
	// concatenated it verbatim. Pin the cap.
	huge := strings.Repeat("x", MaxReviewBytesForMerge*3)
	results := []ReviewResult{
		{LLM: "claude", Output: "small finding"},
		{LLM: "gemini", Output: huge},
	}
	in := BuildMergeInput(results, 2)
	if len(in.Reviews) != 2 {
		t.Fatalf("want 2 reviews, got %d", len(in.Reviews))
	}
	// Find the gemini one — order is preserved by appearance.
	var gem ReviewContent
	for _, r := range in.Reviews {
		if r.LLM == "gemini" {
			gem = r
		}
	}
	if len(gem.Content) > MaxReviewBytesForMerge {
		t.Errorf("oversize review not truncated: len=%d, cap=%d", len(gem.Content), MaxReviewBytesForMerge)
	}
	if !strings.Contains(gem.Content, "truncated") {
		t.Errorf("truncation marker missing from clipped content")
	}
}

func TestTruncateForMerge_RespectsUTF8Boundaries(t *testing.T) {
	// Pre-fix the byte slice in truncateForMerge could split a multi-
	// byte UTF-8 sequence, feeding invalid UTF-8 into the merge
	// prompt. Cyrillic ("Привіт"), CJK ("世界"), and emoji ("🚨") are
	// all multi-byte; we exercise all three.
	mkLong := func(unit string) string {
		// Repeat enough times to exceed the cap, so truncation actually fires.
		s := strings.Repeat(unit, MaxReviewBytesForMerge/len(unit)+10)
		return s
	}
	cases := []struct {
		name string
		in   string
	}{
		{"cyrillic", mkLong("привіт ")},
		{"cjk", mkLong("世界 ")},
		{"emoji", mkLong("🚨 ")},
		{"mixed ascii + multibyte", mkLong("issue: проблема in 文件 🐛 ")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateForMerge(tc.in)
			if !utf8.ValidString(got) {
				t.Errorf("truncated output is invalid UTF-8 — boundary not respected")
			}
			if !strings.Contains(got, "truncated") {
				t.Errorf("truncation marker missing")
			}
		})
	}
}

func TestBuildMergeInput_PassesNormalReviewsUnchanged(t *testing.T) {
	body := "## Major Issues\n- file:42 — race condition.\n"
	results := []ReviewResult{{LLM: "claude", Output: body}}
	in := BuildMergeInput(results, 2)
	if in.Reviews[0].Content != body {
		t.Errorf("non-oversize review was modified — got:\n%s", in.Reviews[0].Content)
	}
}

func TestBuildMergeInput_SkipsEmptyOutputs(t *testing.T) {
	// Existing behavior preserved: a failed review with empty Output
	// is dropped from the merge input rather than fed as an empty
	// stub the merger has to interpret.
	results := []ReviewResult{
		{LLM: "claude", Output: "real finding"},
		{LLM: "gemini", Output: ""},
		{LLM: "codex", Output: "another"},
	}
	in := BuildMergeInput(results, 2)
	if len(in.Reviews) != 2 {
		t.Errorf("want 2 reviews (empty dropped), got %d", len(in.Reviews))
	}
	if in.LLMNames != "claude, codex" {
		t.Errorf("LLMNames = %q, want %q", in.LLMNames, "claude, codex")
	}
}

func TestBuildMergeInput_SkipsWhitespaceOnlyOutputs(t *testing.T) {
	// A CLI exiting zero with "\n" or " \t\n" is not actually a review.
	// Pre-fix the bare `r.Output != ""` check let these through, and
	// the merger ran on effectively empty input — producing a
	// "successfully formatted" report from nothing. After the
	// HasMergeableOutput unification this drops them on the floor
	// alongside the truly empty case.
	results := []ReviewResult{
		{LLM: "claude", Output: "real finding"},
		{LLM: "gemini", Output: "\n"},
		{LLM: "codex", Output: "  \t\n  "},
	}
	in := BuildMergeInput(results, 2)
	if len(in.Reviews) != 1 {
		t.Errorf("want 1 review (whitespace dropped), got %d (%v)", len(in.Reviews), in.LLMNames)
	}
	if in.LLMNames != "claude" {
		t.Errorf("LLMNames = %q, want %q", in.LLMNames, "claude")
	}
}

func TestCountWithOutput_TrimsWhitespace(t *testing.T) {
	results := []ReviewResult{
		{LLM: "claude", Output: "real finding"},
		{LLM: "gemini", Output: "   "},
		{LLM: "codex", Output: ""},
	}
	if got := CountWithOutput(results); got != 1 {
		t.Errorf("CountWithOutput = %d, want 1 (only claude has non-blank output)", got)
	}
}

func TestHasMergeableOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"empty", "", false},
		{"whitespace only", "  \n\t  ", false},
		{"single newline", "\n", false},
		{"real content", "# Review", true},
		{"content with leading/trailing whitespace", "\n  # Review  \n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasMergeableOutput(ReviewResult{Output: tc.out}); got != tc.want {
				t.Errorf("HasMergeableOutput(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestBuildMergeInput_ClampsThresholdToReviewerCount(t *testing.T) {
	// User configures consensus_threshold: 3 (default) but only 2
	// agents actually produce output. The merge prompt must not ask
	// the LLM for "3+ reviewers agree" — that's impossible by design
	// and the LLM apologizes for it in its own summary line, which
	// reads as a broken template.
	results := []ReviewResult{
		{LLM: "claude", Output: "x"},
		{LLM: "gemini", Output: "y"},
	}
	in := BuildMergeInput(results, 3)
	if in.ConsensusThreshold != 2 {
		t.Errorf("threshold not clamped: got %d, want 2 (reviewer count)", in.ConsensusThreshold)
	}
	// And: when the configured threshold is already ≤ reviewer count,
	// it passes through unchanged.
	in2 := BuildMergeInput(results, 2)
	if in2.ConsensusThreshold != 2 {
		t.Errorf("threshold mutated when within bounds: got %d, want 2", in2.ConsensusThreshold)
	}
	// Edge: a single-survivor degraded run (1 output, threshold 3).
	// Clamp to 1 — the merge prompt becomes "1+ reviewers" which is
	// vacuous but not actively misleading.
	in3 := BuildMergeInput([]ReviewResult{{LLM: "claude", Output: "x"}}, 3)
	if in3.ConsensusThreshold != 1 {
		t.Errorf("solo run: threshold = %d, want 1", in3.ConsensusThreshold)
	}
	// Edge: zero outputs. The reviewer-count ceiling doesn't apply
	// (no reviewers to clamp to), but the floor still pins the
	// threshold to ≥1 so a future caller building input from an
	// empty results slice doesn't see "0+ reviewers" propagate.
	in4 := BuildMergeInput(nil, 3)
	if in4.ConsensusThreshold != 3 {
		t.Errorf("empty input: threshold = %d, want 3 (passthrough)", in4.ConsensusThreshold)
	}
	// Floor: a misconfigured `consensus_threshold: 0` would otherwise
	// reach the merger as "0+ reviewers agree" — meaningless.
	in5 := BuildMergeInput(results, 0)
	if in5.ConsensusThreshold != 1 {
		t.Errorf("zero threshold: got %d, want 1 (floor)", in5.ConsensusThreshold)
	}
	// Floor: same for negative.
	in6 := BuildMergeInput(results, -7)
	if in6.ConsensusThreshold != 1 {
		t.Errorf("negative threshold: got %d, want 1 (floor)", in6.ConsensusThreshold)
	}
}

func TestMergePromptRendersByReviewCount(t *testing.T) {
	// The merge prompt must use single-source language when only one
	// reviewer produced output — pre-fix the template hard-coded
	// "Consolidated Report" / "Generated by merging N LLM reviews"
	// even for ReviewCount==1, which read as outright lies. Rendering
	// here catches a regression where someone unbranches the template
	// or swaps the wording in a way the gate-relevant header strings
	// drift from what we promise.
	tmpl, err := template.New("merge").Parse(mergePromptTemplate)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	render := func(n int) string {
		input := MergeInput{
			ReviewCount:        n,
			LLMNames:           "claude",
			ConsensusThreshold: n,
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, input); err != nil {
			t.Fatalf("execute n=%d: %v", n, err)
		}
		return buf.String()
	}

	cases := []struct {
		name     string
		n        int
		mustHave []string
		mustNot  []string
	}{
		{
			name:     "N=1 → single-source framing",
			n:        1,
			mustHave: []string{"Single-LLM Report", "Reformatted from 1 LLM review", "no cross-model consensus", "do not invent consensus tags"},
			mustNot:  []string{"Consolidated Report", "Generated by merging", "Confirmed by: LLM1, LLM2"},
		},
		{
			name:     "N=2 → consolidated framing",
			n:        2,
			mustHave: []string{"Consolidated Report", "Generated by merging 2 LLM reviews", "Confirmed by:"},
			mustNot:  []string{"Single-LLM Report", "Reformatted from 1"},
		},
		{
			name:     "N=3 → consolidated framing",
			n:        3,
			mustHave: []string{"Consolidated Report", "Generated by merging 3 LLM reviews"},
			mustNot:  []string{"Single-LLM Report"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := render(tc.n)
			for _, want := range tc.mustHave {
				if !strings.Contains(out, want) {
					t.Errorf("rendered prompt missing %q", want)
				}
			}
			for _, no := range tc.mustNot {
				if strings.Contains(out, no) {
					t.Errorf("rendered prompt unexpectedly contains %q", no)
				}
			}
		})
	}
}

func TestStripFenceWrapper(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "wrapped in ```markdown fence — strip",
			in:   "```markdown\n# Report\n## Summary\n```",
			want: "# Report\n## Summary",
		},
		{
			name: "wrapped in ```md fence — strip",
			in:   "```md\n# Report\n```",
			want: "# Report",
		},
		{
			name: "wrapped in bare ``` fence — strip",
			in:   "```\n# Report\n```",
			want: "# Report",
		},
		{
			name: "no fence — pass through",
			in:   "# Report\n\nbody.",
			want: "# Report\n\nbody.",
		},
		{
			name: "inner ```python block must survive when no outer wrapper",
			in:   "# Report\n```python\nx = 1\n```\n",
			want: "# Report\n```python\nx = 1\n```\n",
		},
		{
			name: "outer ```markdown wrapper with inner ```python — only outer stripped",
			in:   "```markdown\n# Report\n```python\nx = 1\n```\n```",
			want: "# Report\n```python\nx = 1\n```",
		},
		{
			name: "fence with surrounding whitespace",
			in:   "  \n```markdown\n# Report\n```\n  ",
			want: "# Report",
		},
		{
			name: "unbalanced opener but no closer — pass through unchanged",
			in:   "```markdown\n# Report (truncated, no closing fence)",
			want: "```markdown\n# Report (truncated, no closing fence)",
		},
		{
			// Critical case from Copilot review: LLM hits max-tokens
			// mid-output. Opener fired, content cuts off inside a
			// Python block; the LAST line happens to be ``` (closing
			// the inner block, not the outer wrapper). Naive strip
			// would corrupt the markdown by removing the inner closer.
			// Parity guard refuses.
			name: "truncated content ending with inner block closer — pass through",
			in:   "```markdown\n# Report\n\nFix:\n```python\nx = 1\n```",
			want: "```markdown\n# Report\n\nFix:\n```python\nx = 1\n```",
		},
		{
			// CRLF line endings (Windows binaries / some CLIs) must
			// not defeat the strip — pre-fix the opener regex required
			// `\n` and \r\n input would print the literal fence.
			name: "CRLF wrapped — strip",
			in:   "```markdown\r\n# Report\r\n```",
			want: "# Report",
		},
		{
			// CRLF with inner block — outer pair stripped, inner
			// block preserved (CRLF intact inside).
			name: "CRLF with inner block — strip outer only",
			in:   "```markdown\r\n# Report\r\n```python\r\nx=1\r\n```\r\n```",
			want: "# Report\r\n```python\r\nx=1\r\n```",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripFenceWrapper(tc.in)
			if got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

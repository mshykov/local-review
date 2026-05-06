package multi

import (
	"strings"
	"testing"
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
	// Edge: zero outputs. Clamp would underflow / produce 0; keep
	// the configured threshold so we don't divide-by-zero downstream
	// (the caller short-circuits before reaching the merger anyway).
	in4 := BuildMergeInput(nil, 3)
	if in4.ConsensusThreshold != 3 {
		t.Errorf("empty input: threshold = %d, want 3 (passthrough)", in4.ConsensusThreshold)
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

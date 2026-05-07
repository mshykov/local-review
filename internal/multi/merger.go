package multi

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/mshykov/local-review/internal/cli"
)

//go:embed merge_prompt.md
var mergePromptTemplate string

// Merger consolidates review findings from multiple LLMs.
type Merger struct {
	invoker  cli.Invoker
	template *template.Template
}

// NewMerger creates a new Merger that uses the given LLM for merging.
func NewMerger(llm cli.LLM) (*Merger, error) {
	invoker := cli.NewInvoker(llm)
	if invoker == nil {
		return nil, fmt.Errorf("failed to create invoker for %s", llm.Name)
	}

	tmpl, err := template.New("merge").Parse(mergePromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse merge template: %w", err)
	}

	return &Merger{
		invoker:  invoker,
		template: tmpl,
	}, nil
}

// MergeInput holds data for the merge template.
type MergeInput struct {
	ReviewCount        int
	LLMNames           string
	ConsensusThreshold int
	Reviews            []ReviewContent
}

// ReviewContent holds a single review's content.
type ReviewContent struct {
	LLM     string
	Content string
}

// ErrNoReviewsToMerge signals that Merge was called with an empty
// review set. The runner short-circuits before reaching this in
// normal flow (see classifyRunMode + the no-mergeable-output path),
// but the guard here is defense-in-depth — pre-fix, ReviewCount==0
// fell into the multi-review template branch and rendered "0
// separate code review reports from: " with an empty reviewer list,
// producing an incoherent prompt to the merger LLM.
var ErrNoReviewsToMerge = fmt.Errorf("no reviews to merge")

// Merge consolidates multiple reviews into one using an LLM.
//
// Returns the merged markdown report, the merge step's own token
// usage (for cost-attribution alongside per-LLM review usage), and
// any error. Tokens may be zero when the merge LLM's CLI doesn't
// surface usage data.
//
// Returns ErrNoReviewsToMerge when input has no reviewable content.
// The runner is expected to filter zero-review cases upstream
// (single-LLM fallback path or "all agents failed" branch); this is
// just a backstop.
func (m *Merger) Merge(ctx context.Context, input MergeInput) (string, cli.TokenUsage, error) {
	if input.ReviewCount == 0 || len(input.Reviews) == 0 {
		return "", cli.TokenUsage{}, ErrNoReviewsToMerge
	}

	// Render the template
	var buf bytes.Buffer
	if err := m.template.Execute(&buf, input); err != nil {
		return "", cli.TokenUsage{}, fmt.Errorf("execute template: %w", err)
	}

	prompt := buf.String()

	// Send to LLM for merging (use RunPrompt, not Review, to avoid double-wrapping)
	merged, tokens, err := m.invoker.RunPrompt(ctx, prompt)
	if err != nil {
		return "", cli.TokenUsage{}, fmt.Errorf("merge review: %w", err)
	}

	// Some merger LLMs ignore "Return ONLY the merged markdown report"
	// and wrap their output in a ```markdown ... ``` fence. Without
	// this strip, every multi-LLM run prints literal triple-backticks
	// to the terminal AND saves them to disk in <commit>_merged.md.
	return stripFenceWrapper(merged), tokens, nil
}

// fenceOpener matches the leading ```markdown / ```md / bare ```
// fence the merger LLM sometimes wraps its full report in. Anchored
// to start-of-string + optional whitespace so it can't match an
// interior code block by accident. `\r?\n` tolerates CRLF line
// endings — windows builds and some CLIs emit them.
var fenceOpener = regexp.MustCompile("^\\s*```(?:markdown|md)?\\s*\\r?\\n")

// fenceCloser matches the closing ``` at end-of-string. Optional
// trailing whitespace tolerates the LLM ending with `\n`, `\n\n`,
// or `\r\n`.
var fenceCloser = regexp.MustCompile("\\r?\\n?\\s*```\\s*$")

// fenceCounter counts triple-backtick fences at line starts. Used to
// decide whether stripping the outer pair would corrupt inner code
// blocks (an unbalanced count means the wrapper is truncated, not
// complete).
var fenceCounter = regexp.MustCompile("(?m)^\\s*```")

// stripFenceWrapper removes a single outer ```markdown / ```md fence
// when the LLM's full output is wrapped in one. Inner code blocks in
// the report are unaffected.
//
// Three guards fire in order:
//
//  1. Both opener AND closer regexes must match (anchored to string
//     boundaries). A partial wrapper is left alone.
//  2. The total fence count must be even. A complete report with the
//     outer wrapper has an even count (outer pair + each inner pair);
//     an odd count means a truncated wrapper, where what looks like
//     the closer is actually an inner block's closer that we'd
//     corrupt by stripping. Pre-guard, a report ending mid-Python-
//     block with `\n```\n` would lose its inner closer.
//  3. After stripping, the remainder must have a balanced (even)
//     fence count. Defense in depth — if the regex matched something
//     other than what step 2's parity check assumes, we leave the
//     input alone.
//
// On any guard failure we return the input unchanged: better to show
// a leading ```markdown line verbatim than to silently mangle
// content.
func stripFenceWrapper(s string) string {
	if !fenceOpener.MatchString(s) || !fenceCloser.MatchString(s) {
		return s
	}
	if len(fenceCounter.FindAllString(s, -1))%2 != 0 {
		return s
	}
	stripped := fenceOpener.ReplaceAllString(s, "")
	stripped = fenceCloser.ReplaceAllString(stripped, "")
	if len(fenceCounter.FindAllString(stripped, -1))%2 != 0 {
		return s
	}
	return strings.TrimRight(stripped, "\r\n")
}

// MaxReviewBytesForMerge caps how much of any single per-LLM review
// gets concatenated into the merger prompt. The merger LLM has a
// finite context window; a verbose-enough reviewer (or one that
// hallucinates a long preamble) can blow it out by itself, killing
// the whole pipeline at the very last step.
//
// 8 KB ≈ 2k tokens at typical English density — enough room for a
// detailed review while leaving plenty of context budget for the
// other reviewers and the merger's own template / instructions.
// Saved per-LLM review files on disk are NOT truncated; this limit
// applies only to what the merger sees.
const MaxReviewBytesForMerge = 8 * 1024

// BuildMergeInput creates MergeInput from review results.
// Includes all reviews that have output, even if saving to disk failed.
// Per-review output is truncated to MaxReviewBytesForMerge with a
// trailing notice so the merger can still pick up an explicit signal
// that some content was clipped.
//
// ConsensusThreshold is clamped to the actual reviewer count so the
// merge prompt never asks for "3+ reviewers agree" when only 2 ran —
// the LLM was apologising for the impossible-by-design ask in its own
// summary line ("0 (only 2 reviewers, but 3 issues have 2/2 consensus)"),
// which read as a broken template.
func BuildMergeInput(results []ReviewResult, consensusThreshold int) MergeInput {
	var reviews []ReviewContent
	var llmNames []string

	for _, r := range results {
		// Include any review with non-blank output, regardless of save
		// errors. HasMergeableOutput trims whitespace so a CLI exiting
		// zero with "\n" doesn't feed an effectively empty review into
		// the merger.
		if HasMergeableOutput(r) {
			reviews = append(reviews, ReviewContent{
				LLM:     r.LLM,
				Content: truncateForMerge(r.Output),
			})
			llmNames = append(llmNames, r.LLM)
		}
	}

	// Clamp the threshold into [1, reviewerCount]. Two failure modes
	// guard:
	//
	//   - User config sets `consensus_threshold: 0` (or negative). Without
	//     the floor, the prompt would say "0+ reviewers agree" / "-1+
	//     reviewers agree" — meaningless instructions to the merger.
	//   - User config leaves the default (3) but only 2 agents produce
	//     output. The ceiling drops the prompt's threshold to 2 so it
	//     doesn't ask the impossible.
	effectiveThreshold := consensusThreshold
	if effectiveThreshold < 1 {
		effectiveThreshold = 1
	}
	if n := len(reviews); n > 0 && effectiveThreshold > n {
		effectiveThreshold = n
	}

	return MergeInput{
		ReviewCount:        len(reviews),
		LLMNames:           strings.Join(llmNames, ", "),
		ConsensusThreshold: effectiveThreshold,
		Reviews:            reviews,
	}
}

// truncateForMerge clips an oversize per-LLM review to fit comfortably
// inside the merger's context window. Returns the original string
// when within the cap; otherwise truncates and appends an explicit
// "[…truncated]" marker so a reader (human or LLM) can tell that
// findings may continue in the saved per-LLM file.
//
// The cut point is walked back to a UTF-8 rune boundary. Pre-fix the
// raw byte slice could split a multi-byte code point (Cyrillic, CJK,
// emoji) and feed invalid UTF-8 into the merger prompt, degrading or
// breaking the merge step on non-ASCII reviews.
func truncateForMerge(s string) string {
	if len(s) <= MaxReviewBytesForMerge {
		return s
	}
	const marker = "\n\n[…truncated by local-review for merge: see the saved per-LLM file for full output]"
	cut := MaxReviewBytesForMerge - len(marker)
	if cut < 0 {
		cut = 0
	}
	// Walk back until we land on a UTF-8 start byte (or hit position 0).
	// utf8.RuneStart returns true for any byte that is the first byte
	// of an encoded rune, so this leaves us with a valid prefix even
	// when the cap fell mid-rune.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

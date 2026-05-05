package multi

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

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

// Merge consolidates multiple reviews into one using an LLM.
func (m *Merger) Merge(ctx context.Context, input MergeInput) (string, error) {
	// Render the template
	var buf bytes.Buffer
	if err := m.template.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	prompt := buf.String()

	// Send to LLM for merging (use RunPrompt, not Review, to avoid double-wrapping)
	merged, err := m.invoker.RunPrompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("merge review: %w", err)
	}

	return merged, nil
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
func BuildMergeInput(results []ReviewResult, consensusThreshold int) MergeInput {
	var reviews []ReviewContent
	var llmNames []string

	for _, r := range results {
		// Include any review with output, regardless of save errors
		if r.Output != "" {
			reviews = append(reviews, ReviewContent{
				LLM:     r.LLM,
				Content: truncateForMerge(r.Output),
			})
			llmNames = append(llmNames, r.LLM)
		}
	}

	return MergeInput{
		ReviewCount:        len(reviews),
		LLMNames:           strings.Join(llmNames, ", "),
		ConsensusThreshold: consensusThreshold,
		Reviews:            reviews,
	}
}

// truncateForMerge clips an oversize per-LLM review to fit comfortably
// inside the merger's context window. Returns the original string
// when within the cap; otherwise truncates and appends an explicit
// "[…truncated]" marker so a reader (human or LLM) can tell that
// findings may continue in the saved per-LLM file.
func truncateForMerge(s string) string {
	if len(s) <= MaxReviewBytesForMerge {
		return s
	}
	const marker = "\n\n[…truncated by local-review for merge: see the saved per-LLM file for full output]"
	cut := MaxReviewBytesForMerge - len(marker)
	if cut < 0 {
		cut = 0
	}
	return s[:cut] + marker
}

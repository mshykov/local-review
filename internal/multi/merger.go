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

// BuildMergeInput creates MergeInput from review results.
// Includes all reviews that have output, even if saving to disk failed.
func BuildMergeInput(results []ReviewResult, consensusThreshold int) MergeInput {
	var reviews []ReviewContent
	var llmNames []string

	for _, r := range results {
		// Include any review with output, regardless of save errors
		if r.Output != "" {
			reviews = append(reviews, ReviewContent{
				LLM:     r.LLM,
				Content: r.Output,
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

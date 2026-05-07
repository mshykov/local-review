package multi

import (
	"encoding/json"
	"os"
	"time"
)

// Metadata tracks details about a multi-LLM review run.
type Metadata struct {
	Commit    string       `json:"commit"`
	Branch    string       `json:"branch"`
	Timestamp time.Time    `json:"timestamp"`
	Reviews   []ReviewMeta `json:"reviews"`
	Merge     MergeMeta    `json:"merge"`
}

// ReviewMeta holds details about a single LLM's review.
type ReviewMeta struct {
	LLM           string `json:"llm"`
	Version       string `json:"version"`
	Mode          string `json:"mode"`   // "cli" or "api"
	Status        string `json:"status"` // "success" or "failed"
	DurationMs    int64  `json:"duration_ms"`
	FindingsCount int    `json:"findings_count,omitempty"`
	OutputFile    string `json:"output_file,omitempty"`
	Error         string `json:"error,omitempty"`
	// InputTokens / OutputTokens are prompt and response *size* in
	// tokens — not billing numbers. Sourced from each CLI's
	// structured output (claude / gemini JSON, codex stdout
	// metadata) when available. For Anthropic, InputTokens
	// includes cache-read and cache-creation tokens (Anthropic
	// discounts cache reads ~10× at the billing layer, but for
	// "how big was the prompt" we want the full count). Sum with
	// MergeMeta values for total token volume per PR; for actual
	// dollar spend, use the vendor's billing dashboard. Both 0
	// means usage was indeterminate. omitempty keeps backward-
	// compat for readers that don't know about these fields.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	// TotalOnlyTokens=true means input/output split is unknown and
	// InputTokens holds the combined total (codex pre-v0.128 stdout
	// shape). Aggregation tools should still sum InputTokens +
	// OutputTokens — the total is in InputTokens, OutputTokens is 0
	// — but display-layer tools should show "Nk total" rather than
	// "Nk in / 0 out".
	TotalOnlyTokens bool `json:"total_only_tokens,omitempty"`
}

// MergeMeta holds details about the merge operation.
type MergeMeta struct {
	LLM                  string `json:"llm"`
	Status               string `json:"status"`
	FinalFindingsCount   int    `json:"final_findings_count,omitempty"`
	DeduplicationRemoved int    `json:"deduplication_removed,omitempty"`
	DurationMs           int64  `json:"duration_ms,omitempty"`
	Error                string `json:"error,omitempty"`
	// InputTokens / OutputTokens for the merge step's own LLM call,
	// same shape and semantics as ReviewMeta — prompt/response
	// *size*, not billed amount. Sum with each ReviewMeta to get
	// total per-PR token volume; for dollar spend, see the vendor
	// billing dashboard.
	InputTokens     int  `json:"input_tokens,omitempty"`
	OutputTokens    int  `json:"output_tokens,omitempty"`
	TotalOnlyTokens bool `json:"total_only_tokens,omitempty"`
}

// Save writes the metadata to a JSON file.
func (m *Metadata) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Add trailing newline for POSIX compliance
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

// Load reads metadata from a JSON file.
func Load(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

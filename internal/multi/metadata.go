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
}

// MergeMeta holds details about the merge operation.
type MergeMeta struct {
	LLM                  string `json:"llm"`
	Status               string `json:"status"`
	FinalFindingsCount   int    `json:"final_findings_count,omitempty"`
	DeduplicationRemoved int    `json:"deduplication_removed,omitempty"`
	DurationMs           int64  `json:"duration_ms,omitempty"`
	Error                string `json:"error,omitempty"`
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

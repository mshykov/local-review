// Package review owns the orchestration: takes a diff, runs it through the LLM
// with the right prompt pack, and returns structured findings.
package review

import (
	"encoding/json"
	"fmt"
)

// Severity tier. Drives the default filter (>= warning shown by default).
type Severity int

const (
	SeverityNit Severity = iota
	SeverityInfo
	SeverityWarning
	SeverityMajor
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityNit:
		return "nit"
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityMajor:
		return "major"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseSeverity converts a string ("nit", "info", "warning", "major",
// "critical") to a Severity. Unknown values default to warning.
func ParseSeverity(s string) Severity {
	switch s {
	case "nit":
		return SeverityNit
	case "info":
		return SeverityInfo
	case "warning":
		return SeverityWarning
	case "major":
		return SeverityMajor
	case "critical":
		return SeverityCritical
	default:
		return SeverityWarning
	}
}

// MarshalJSON emits Severity as its string form (e.g. "major") so JSON
// output is human-readable and stable across additions of new tiers.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON accepts both the string form ("major") produced by
// MarshalJSON and the legacy numeric form (3) that earlier --json
// output emitted before MarshalJSON existed. The numeric form is
// supported for backward compatibility with persisted output and
// resilience against an LLM that emits a number by mistake.
func (s *Severity) UnmarshalJSON(data []byte) error {
	// Try string first (the MarshalJSON-produced form, the common case).
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = ParseSeverity(str)
		return nil
	}
	// Fall back to numeric. Out-of-range values clamp to the nearest
	// defined tier so unknown integers don't crash decoders.
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("severity must be a string or number, got %s", string(data))
	}
	switch {
	case n < int(SeverityNit):
		*s = SeverityNit
	case n > int(SeverityCritical):
		*s = SeverityCritical
	default:
		*s = Severity(n)
	}
	return nil
}

// Finding is one issue raised against the diff.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Tag      string   `json:"tag,omitempty"` // optional category, e.g. "security", "perf"
}

func (f Finding) Loc() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return f.File
}

// Report is the full output of one review run.
type Report struct {
	Findings []Finding  `json:"findings"`
	Meta     ReportMeta `json:"meta"`
}

type ReportMeta struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Files    int    `json:"files"`
	Tokens   int    `json:"tokens,omitempty"`
}

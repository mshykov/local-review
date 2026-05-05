package review

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/lang"
	"github.com/mshykov/local-review/internal/llm"
	"github.com/mshykov/local-review/internal/prompts"
)

// Reviewer wires the LLM client + config into one callable surface.
type Reviewer struct {
	cfg    config.Config
	client *llm.Client
}

func New(cfg config.Config) *Reviewer {
	return &Reviewer{
		cfg:    cfg,
		client: llm.New(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.APIKeyEnv, cfg.Provider.Model, cfg.Provider.TimeoutSec),
	}
}

// Run extracts the diff for the given mode/ref, picks a prompt pack,
// and returns a filtered Report.
func (r *Reviewer) Run(ctx context.Context, mode git.Mode, ref string) (Report, error) {
	diffs, err := git.Extract(mode, ref)
	if err != nil {
		return Report{}, fmt.Errorf("extract diff: %w", err)
	}
	diffs = filter(diffs, r.cfg.Review.IncludeGlobs, r.cfg.Review.ExcludeGlobs)
	if len(diffs) == 0 {
		return Report{Meta: r.meta(0)}, nil
	}

	packID := r.cfg.Review.PromptPack
	if packID == "" {
		paths := make([]string, len(diffs))
		for i, d := range diffs {
			paths[i] = d.Path
		}
		packID = lang.Dominant(paths)
	}

	pack, err := prompts.Get(packID)
	if err != nil {
		return Report{}, fmt.Errorf("load prompt pack %q: %w", packID, err)
	}

	user := buildUserMessage(diffs)
	raw, err := r.client.Complete(ctx, []llm.Message{
		{Role: "system", Content: pack},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return Report{}, fmt.Errorf("llm: %w", err)
	}

	findings, err := parseFindings(raw)
	if err != nil {
		return Report{}, fmt.Errorf("parse findings: %w", err)
	}

	min := ParseSeverity(r.cfg.Review.MinSeverity)
	findings = applyFilters(findings, min, r.cfg.Review.MaxFindings)

	return Report{Findings: findings, Meta: r.meta(len(diffs))}, nil
}

func (r *Reviewer) meta(files int) ReportMeta {
	return ReportMeta{
		Provider: r.cfg.Provider.BaseURL,
		Model:    r.cfg.Provider.Model,
		Files:    files,
	}
}

// buildUserMessage formats the diff for the model. We send each file
// as a labelled block so the LLM can attribute findings cleanly.
func buildUserMessage(diffs []git.Diff) string {
	var b strings.Builder
	b.WriteString("Review the following diff. Return only the JSON object specified in your instructions.\n\n")
	for _, d := range diffs {
		fmt.Fprintf(&b, "## File: %s\n", d.Path)
		for _, h := range d.Hunks {
			b.WriteString(h.Header)
			b.WriteString("\n")
			b.WriteString(h.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseFindings extracts the LLM's JSON. Tolerates surrounding prose
// or markdown fences (some providers re-wrap in ```json```), and
// multi-block output where the LLM shows an example object first and
// the actual answer second ("Here's an example {...}. My result: {...}").
//
// The previous "first-{ to last-}" heuristic concatenated example +
// prose + answer into one substring and then failed to unmarshal,
// producing a confusing parse error on what should have been a clean
// review response.
func parseFindings(raw string) ([]Finding, error) {
	body := strings.TrimSpace(raw)
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)

	candidates := topLevelJSONObjects(body)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no JSON object found in response")
	}

	// Try the LAST candidate first. LLMs that emit an example block
	// followed by the real answer always put the answer last, so this
	// keeps the happy path one unmarshal call. Fall back through the
	// earlier candidates so degraded outputs still parse.
	//
	// We probe each candidate with a `map[string]json.RawMessage` first
	// to confirm it has a "findings" key — Go's json.Unmarshal silently
	// ignores unknown fields, so a stray `{"note": "..."}` block at the
	// end would otherwise look like a valid empty-findings envelope and
	// shadow the real one earlier in the response.
	var lastErr error
	for i := len(candidates) - 1; i >= 0; i-- {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidates[i]), &probe); err != nil {
			lastErr = err
			continue
		}
		raw, ok := probe["findings"]
		if !ok {
			continue
		}
		var findings []rawFinding
		if err := json.Unmarshal(raw, &findings); err != nil {
			lastErr = err
			continue
		}
		out := make([]Finding, 0, len(findings))
		for _, f := range findings {
			out = append(out, Finding{
				File:     f.File,
				Line:     f.Line,
				Severity: ParseSeverity(f.Severity),
				Title:    f.Title,
				Body:     f.Body,
				Tag:      f.Tag,
			})
		}
		return out, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no JSON object with a `findings` key found in response")
}

// topLevelJSONObjects scans s and returns every balanced top-level
// `{...}` substring in order of appearance. Tracks string-literal
// state (with backslash escaping) so braces inside JSON strings
// don't mis-balance the scanner. Square brackets aren't tracked
// because the LLM envelope is always an object.
func topLevelJSONObjects(s string) []string {
	var out []string
	depth := 0
	start := -1
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				// Stray closing brace — ignore, no balanced object opens here.
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i+1])
				start = -1
			}
		}
	}
	return out
}

type rawFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Tag      string `json:"tag"`
}

// applyFilters drops findings below min severity and caps total count.
// Sorts by severity desc, then by file asc for stable output.
func applyFilters(in []Finding, min Severity, max int) []Finding {
	out := in[:0]
	for _, f := range in {
		if f.Severity >= min {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// FilterDiffs keeps diffs matching any include glob (when set) and not
// matching any exclude glob. Exported so the multi-LLM runner can apply
// the same review.include/review.exclude config the single-LLM path
// already uses — pre-v0.5.x the multi path silently bypassed both.
func FilterDiffs(diffs []git.Diff, include, exclude []string) []git.Diff {
	return filter(diffs, include, exclude)
}

// filter keeps diffs matching any IncludeGlob (when set) and not
// matching any ExcludeGlob.
func filter(diffs []git.Diff, include, exclude []string) []git.Diff {
	out := diffs[:0]
	for _, d := range diffs {
		if len(include) > 0 && !matchesAny(d.Path, include) {
			continue
		}
		if matchesAny(d.Path, exclude) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func matchesAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if matchGlob(path, p) {
			return true
		}
	}
	return false
}

// matchGlob is a tiny glob matcher with `**` support. We convert the
// glob to a regex once per check — perf is fine because we run this
// against handfuls of files, not millions.
//
// Semantics:
//
//   - matches any chars except '/'
//     ** matches any chars (including '/')
//     ?  matches one char except '/'
func matchGlob(path, pattern string) bool {
	re, err := regexp.Compile(globToRegex(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func globToRegex(pattern string) string {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			sb.WriteString(".*")
			i++
			// `**/foo` — consume the slash so the regex doesn't
			// require an extra `/` between `.*` and the next chunk.
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		case c == '.', c == '+', c == '(', c == ')', c == '|',
			c == '^', c == '$', c == '{', c == '}', c == '[', c == ']', c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteString("$")
	return sb.String()
}

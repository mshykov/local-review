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
		client: llm.New(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.Model, cfg.Provider.TimeoutSec),
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
// or markdown fences (some providers re-wrap in ```json```).
func parseFindings(raw string) ([]Finding, error) {
	body := strings.TrimSpace(raw)
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)

	first := strings.Index(body, "{")
	last := strings.LastIndex(body, "}")
	if first < 0 || last <= first {
		return nil, fmt.Errorf("no JSON object found in response")
	}
	body = body[first : last+1]

	var envelope struct {
		Findings []rawFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return nil, err
	}

	out := make([]Finding, 0, len(envelope.Findings))
	for _, f := range envelope.Findings {
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

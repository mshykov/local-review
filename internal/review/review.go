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

	// Resolve picks up any user override (PackDir / Prepend / Append
	// from cfg.Prompts, issue #55) so a team's house rules reach the
	// single-LLM fallback path the same way they reach the multi-LLM
	// CLI invokers — both paths share one resolver. The mapping is
	// inlined here AND in cmd/local-review/runner.go selectPromptPack
	// to keep internal/prompts free of an import dependency on
	// internal/config; the three-field copy is too small to warrant
	// a shared helper, and inlining matches the project's "manual
	// walk over config fields" style (see merge() in config.go).
	pack, err := prompts.Resolve(packID, prompts.ResolveOptions{
		PackDir: r.cfg.Prompts.PackDir,
		Prepend: r.cfg.Prompts.Prepend,
		Append:  r.cfg.Prompts.Append,
	})
	if err != nil {
		return Report{}, fmt.Errorf("load prompt pack %q: %w", packID, err)
	}

	user := buildUserMessage(diffs)
	raw, err := r.client.Complete(ctx, []llm.Message{
		{Role: "system", Content: pack.Content},
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
//
// Compiles each pattern to a regex exactly once before the per-file
// loop. Pre-fix matchGlob did regexp.Compile inside the inner loop —
// for a 500-file repo with 5 globs that's 2,500 compiles. Now it's
// at most len(include)+len(exclude).
//
// Include semantics deliberately fail closed: if `include` was
// non-empty but every pattern failed to compile, we drop everything.
// The legacy matchGlob path also fail-closed (compile error → no
// match → file excluded by the include test). Without this guard a
// user typo'ing the only include rule would silently *expand* the
// review to every file in the diff.
func filter(diffs []git.Diff, include, exclude []string) []git.Diff {
	includeRE := compileGlobs(include)
	excludeRE := compileGlobs(exclude)
	includeRequested := len(include) > 0
	out := diffs[:0]
	for _, d := range diffs {
		if includeRequested && !matchesAnyCompiled(d.Path, includeRE) {
			continue
		}
		if matchesAnyCompiled(d.Path, excludeRE) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// compileGlobs converts each pattern to a regex via globToRegex.
// Patterns that fail to compile are silently dropped (same fail-open
// behavior as the legacy matchGlob, which returned false on compile
// error). A failing pattern is invariably a config typo we can't fix
// from this layer; the user-visible signal is that the glob doesn't
// match anything.
func compileGlobs(patterns []string) []*regexp.Regexp {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if re, err := regexp.Compile(globToRegex(p)); err == nil {
			out = append(out, re)
		}
	}
	return out
}

func matchesAnyCompiled(path string, res []*regexp.Regexp) bool {
	for _, re := range res {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// matchesAny is the un-compiled variant kept for tests and any caller
// that has a string slice handy and doesn't care about per-call regex
// compile cost. Production filtering goes through compileGlobs +
// matchesAnyCompiled.
func matchesAny(path string, patterns []string) bool {
	return matchesAnyCompiled(path, compileGlobs(patterns))
}

// matchGlob is a tiny glob matcher with `**` support. Kept for tests
// and back-compat with any external caller; production filtering uses
// compileGlobs + matchesAnyCompiled to amortize regex compilation
// across all paths in a diff.
//
// Semantics:
//
//   - `*` matches any chars except '/'
//   - `**/` matches zero or more path segments (anchored to a path
//     boundary, so `**/dist/**` does NOT match src/mydist/file)
//   - `**` matches any chars (including '/'); only at end of pattern
//   - `?` matches one char except '/'
//   - `[ab]` / `[a-z]` / `[!ab]` character classes (glob-native,
//     `[!...]` → `[^...]`)
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
			i++ // consume the second *
			// `**/x` should match "x at the start of any path segment"
			// — emit (?:.*/)? so the trailing slash is part of the
			// optional prefix. Pre-fix we emitted plain `.*`, which
			// happily matched src/mydist/file against **/dist/** by
			// gobbling "src/my" without requiring a path boundary.
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				sb.WriteString(`(?:.*/)?`)
				i++ // consume the /
			} else {
				// `**` at end of pattern — match anything remaining.
				sb.WriteString(".*")
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		case c == '[':
			// Glob character class. Copy verbatim until the matching ']',
			// translating a leading '!' into regex '^' negation. If we
			// don't find a closing ']' we fall back to escaping the '['
			// — that's what filepath.Match does and what users on the
			// receiving end of a malformed glob probably expect.
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				sb.WriteString(`\[`)
				continue
			}
			body := pattern[i+1 : i+1+end]
			sb.WriteByte('[')
			if strings.HasPrefix(body, "!") {
				sb.WriteByte('^')
				body = body[1:]
			}
			sb.WriteString(body)
			sb.WriteByte(']')
			i += 1 + end // skip past the ']'
		case c == '.', c == '+', c == '(', c == ')', c == '|',
			c == '^', c == '$', c == '{', c == '}', c == ']', c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteString("$")
	return sb.String()
}

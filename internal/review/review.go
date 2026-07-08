// Package review owns the diff-filter helpers and the blocking-markdown
// exit-gate scan.
//
// History: pre-v0.15 this file also contained the single-LLM-fallback
// orchestration (Reviewer.Run, parseFindings, etc.) — the v0 code path
// that hit the configured `provider:` endpoint directly. The unified
// agent model (v0.14) made that path redundant: provider endpoints
// (Ollama / vLLM / OpenAI / …) are now first-class agents under
// `llms.<name>.base_url`, run by the same fan-out as the CLI agents.
// v0.15 removed the orchestration + `provider:` config block; what
// survives here is the glob-filter machinery the multi-LLM runner
// shares with the audit walker.
package review

import (
	"regexp"
	"strings"

	"github.com/mshykov/local-review/internal/git"
)

// FilterDiffs keeps diffs matching any include glob (when set) and not
// matching any exclude glob. The multi-LLM runner and the audit walker
// both call this so review.include / review.exclude apply uniformly.
func FilterDiffs(diffs []git.Diff, include, exclude []string) []git.Diff {
	return filter(diffs, include, exclude)
}

// filter keeps diffs matching any IncludeGlob (when set) and not
// matching any ExcludeGlob.
//
// Compiles each pattern to a regex exactly once before the per-file
// loop. A naive "compile inside the inner loop" implementation on a
// 500-file repo with 5 globs would do 2,500 compiles; this is at
// most len(include)+len(exclude).
//
// Include semantics deliberately fail closed: if `include` was
// non-empty but every pattern failed to compile, we drop everything.
// (compileGlobs silently skips uncompilable patterns — see its doc
// for why; the fail-closed behaviour falls out of "no patterns
// match" rather than being implemented separately.) Without this
// guard a user typo'ing the only include rule would silently
// *expand* the review to every file in the diff.
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
// Patterns that fail to compile are silently dropped — a failing
// pattern is invariably a config typo we can't fix from this layer;
// the user-visible signal is that the glob doesn't match anything,
// which combines with filter()'s "include fails closed" semantics
// to drop everything (rather than silently expand the review).
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

// matchesAny is the un-compiled variant kept for tests and one-off
// callers that have a string slice handy and don't care about per-
// call regex compile cost. Production filtering goes through
// compileGlobs + matchesAnyCompiled. The glob dialect (anchoring,
// `**`, character classes) is documented on globToRegex below.
func matchesAny(path string, patterns []string) bool {
	return matchesAnyCompiled(path, compileGlobs(patterns))
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

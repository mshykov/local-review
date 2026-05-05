// Package lang maps file paths to a language identifier used to pick
// the right prompt pack. Keep this list short and obvious; unknown
// extensions fall through to the default pack.
package lang

import (
	"path/filepath"
	"strings"
)

// Identifier returned by Detect. These match the pack file names in
// internal/prompts/packs (e.g. Detect("foo.ts") → "typescript" → typescript.md).
//
// JavaScript intentionally maps to TypeScript: there's no dedicated
// javascript.md pack and shipping one would duplicate ~95% of the TS
// pack content (TS is a superset; the React/Next.js/Node patterns
// covered by the TS pack apply equally to plain JS). Better to point
// at one well-tuned pack than maintain two near-duplicates. If JS-
// specific concerns ever diverge meaningfully, ship a javascript.md
// and flip this constant.
const (
	Default    = "default"
	TypeScript = "typescript"
	Go         = "go"
	Python     = "python"
	Java       = "java"
	Rust       = "rust"
	Ruby       = "ruby"
	CSharp     = "csharp"
	PHP        = "php"
)

var byExt = map[string]string{
	".ts":   TypeScript,
	".tsx":  TypeScript,
	".js":   TypeScript, // see comment on the JavaScript constant above
	".jsx":  TypeScript,
	".mjs":  TypeScript,
	".cjs":  TypeScript,
	".go":   Go,
	".py":   Python,
	".pyw":  Python,
	".java": Java,
	".rs":   Rust,
	".rb":   Ruby,
	".cs":   CSharp,
	".php":  PHP,
}

// Detect returns the language identifier for a file path, or Default
// when the extension is unknown.
func Detect(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if l, ok := byExt[ext]; ok {
		return l
	}
	return Default
}

// Dominant picks the most common language across a set of files. Ties
// are broken by alphabetical order so the result is deterministic.
func Dominant(paths []string) string {
	counts := map[string]int{}
	for _, p := range paths {
		counts[Detect(p)]++
	}
	if len(counts) == 0 {
		return Default
	}
	best := Default
	bestN := -1
	for lang, n := range counts {
		if n > bestN || (n == bestN && lang < best) {
			best = lang
			bestN = n
		}
	}
	return best
}

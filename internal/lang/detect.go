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
const (
	Default    = "default"
	TypeScript = "typescript"
	JavaScript = "javascript"
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
	".js":   JavaScript,
	".jsx":  JavaScript,
	".mjs":  JavaScript,
	".cjs":  JavaScript,
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

// Package prompts ships the review prompt packs as embedded markdown.
//
// Each pack is its own file under packs/, named after the language id
// returned by lang.Detect (default.md, typescript.md, go.md, ...).
//
// Get(language) returns the language-specific pack when present, or
// the default pack otherwise. This is the language-aware-but-tool-
// agnostic split: the binary is one thing; the rules are pluggable.
package prompts

import (
	"embed"
	"fmt"
)

//go:embed packs/*.md
var fs embed.FS

// Get returns the text of the named pack. If the language has no
// dedicated pack, the default pack is returned.
func Get(language string) (string, error) {
	if b, err := fs.ReadFile("packs/" + language + ".md"); err == nil {
		return string(b), nil
	}
	b, err := fs.ReadFile("packs/default.md")
	if err != nil {
		return "", fmt.Errorf("default prompt pack missing from binary: %w", err)
	}
	return string(b), nil
}

// Available returns the language ids that have dedicated packs.
func Available() ([]string, error) {
	entries, err := fs.ReadDir("packs")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if len(name) > 3 && name[len(name)-3:] == ".md" {
			ids = append(ids, name[:len(name)-3])
		}
	}
	return ids, nil
}

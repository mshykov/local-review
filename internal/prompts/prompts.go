// Package prompts ships the review prompt packs as embedded markdown.
//
// Each pack is its own file under packs/, named after the language id
// returned by lang.Detect (default.md, typescript.md, go.md, ...).
//
// Get(language) returns the language-specific pack when present, or
// the default pack otherwise. This is the language-aware-but-tool-
// agnostic split: the binary is one thing; the rules are pluggable.
//
// Resolve(language, ResolveOptions) layers user customization on top
// (issue #55, v0.8): a per-language override file from a config-pointed
// directory, plus optional prepend/append text. Used by both the
// single-LLM fallback path and the multi-LLM CLI invokers so a team's
// house rules reach every reviewer.
package prompts

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed packs/*.md
var fs embed.FS

// Pack is the resolved prompt content plus a human-readable label of
// where it came from. Source values:
//
//	"embedded"               — the upstream pack baked into the binary
//	"<absolute path>.md"     — a user-supplied override file
//	"embedded+prepend"       — embedded pack with house rules prepended
//	"embedded+append"        — same idea, appended
//	"embedded+prepend+append"  / "<path>+prepend" / etc. for combos
//
// `local-review config` prints Source so users can see at a glance
// which prompt actually fed the review (the most common debugging
// question after "did my override take effect?"). Source is not
// security-sensitive — it carries the resolved override path verbatim.
type Pack struct {
	Content string
	Source  string
}

// ResolveOptions carries the runtime knobs that turn an embedded pack
// into the user's final prompt. Zero value = identical to Get(language)
// — no override directory, no prepend/append, "embedded" Source.
type ResolveOptions struct {
	// PackDir is a directory of <language>.md files that override the
	// matching embedded pack. A `go.md` in this directory replaces the
	// embedded `go.md`; a missing file falls through to the embedded
	// pack of the same language. Empty = no override directory.
	PackDir string

	// Prepend is text spliced BEFORE the pack body (whether embedded
	// or override). Use for house rules that should colour the entire
	// review ("never approve commented-out code", "prefer TypeScript
	// over JavaScript in new code").
	Prepend string

	// Append is text spliced AFTER the pack body. Use for output-shape
	// rules ("respond in English only", "include a one-line summary at
	// the end") that the LLM should see last.
	Append string
}

// Get returns the text of the named pack. If the language has no
// dedicated pack, the default pack is returned. Backward-compatible
// pre-v0.8 wrapper around Resolve with empty options.
func Get(language string) (string, error) {
	p, err := Resolve(language, ResolveOptions{})
	if err != nil {
		return "", err
	}
	return p.Content, nil
}

// Resolve returns the prompt pack for `language`, applying user
// customization in this order:
//
//  1. Body: opts.PackDir/<language>.md (if present) → embedded
//     <language>.md → embedded default.md.
//  2. Prepend opts.Prepend (if set), separated by a blank line.
//  3. Append opts.Append (if set), separated by a blank line.
//
// The Source field on the returned Pack labels what actually got
// loaded (file path or "embedded") so callers can surface it to
// users. A missing PackDir entry is NOT an error — fall-through is
// the documented behaviour. PackDir is missing AS A WHOLE returns no
// error either; that case is surfaced by `local-review doctor`.
func Resolve(language string, opts ResolveOptions) (Pack, error) {
	// Validate the language id against a strict allow-list before
	// anywhere uses it to construct a filesystem path. Without this
	// guard, a hostile repo config (`review.prompt_pack:
	// ../../etc/passwd`) could escape `pack_dir` and load arbitrary
	// files into the system prompt — which is then sent to the LLM
	// in plaintext and can come back in the model's review output.
	// The threat model is a CI runner checking out an attacker-
	// controlled commit. (Codex flagged this in PR self-review iter 3.)
	if err := validateLanguageID(language); err != nil {
		return Pack{}, err
	}

	body, source, err := loadBody(language, opts.PackDir)
	if err != nil {
		return Pack{}, err
	}

	content := body
	tags := []string{}
	if s := strings.TrimSpace(opts.Prepend); s != "" {
		content = opts.Prepend + "\n\n" + content
		tags = append(tags, "prepend")
	}
	if s := strings.TrimSpace(opts.Append); s != "" {
		content = content + "\n\n" + opts.Append
		tags = append(tags, "append")
	}
	if len(tags) > 0 {
		source = source + "+" + strings.Join(tags, "+")
	}

	return Pack{Content: content, Source: source}, nil
}

// loadBody picks the pack body — a user override file if present in
// packDir, else the embedded language pack, else the embedded default.
// Returns (body, sourceLabel, err). Errors only when the embedded
// default is missing (build broken).
func loadBody(language, packDir string) (string, string, error) {
	if packDir != "" {
		if body, path, ok := readOverride(packDir, language); ok {
			return body, path, nil
		}
	}
	if b, err := fs.ReadFile("packs/" + language + ".md"); err == nil {
		return string(b), "embedded", nil
	}
	b, err := fs.ReadFile("packs/default.md")
	if err != nil {
		return "", "", fmt.Errorf("default prompt pack missing from binary: %w", err)
	}
	return string(b), "embedded", nil
}

// languageIDRE bounds what a language id can contain. Strict on
// purpose: lowercase alphanumeric + dash + underscore, no path
// separators, no leading dot, no embedded dot. Every shipped pack
// id (default, go, python, rust, typescript, java, ruby, csharp,
// php) fits the set. A user adding a custom language ("acme-house")
// fits too. Path-traversal sequences ("..", "/etc") do not.
var languageIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// validateLanguageID is the security gate Resolve calls before any
// path construction. Returning an explicit error (rather than
// silently falling back to the default pack) means a misconfigured
// `review.prompt_pack:` value fails fast instead of being dropped
// on the floor — the user finds out at the start of the run, not
// after the LLM has produced a review against a different pack.
func validateLanguageID(s string) error {
	if !languageIDRE.MatchString(s) {
		return fmt.Errorf("invalid language id %q: must match [a-z0-9][a-z0-9_-]* (no path separators, no leading dot)", s)
	}
	return nil
}

// readOverride returns (body, abs path, true) when packDir contains a
// readable <language>.md, or (_, _, false) for any "fall through" case
// (file missing, unreadable, empty after trim).
//
// Fall-through-on-any-error is intentional. The alternatives —
// returning an error from Resolve, or surfacing a warning per
// review — were considered and rejected:
//
//   - A transient permission glitch on one override file (mount
//     drop, NFS hiccup, post-deploy chmod race) would otherwise
//     turn every review into a hard failure, exactly when users
//     are trying to ship.
//   - The resolver runs on the hot path of every review; warning
//     spam there would train users to ignore it, the worst
//     possible outcome for a "house rules aren't applying"
//     diagnostic.
//
// Visibility instead lives in `local-review doctor`, which actively
// probes pack_dir for unreadable known-language files and surfaces
// them once at setup-check time. See checkPromptOverride in
// cmd/local-review/doctor.go.
//
// Empty files (whitespace-only after trim) also fall through —
// that defends against the worst failure mode here, where an
// accidentally-truncated go.md would silently neuter the entire
// system prompt instead of falling back to a known-good embedded
// pack.
func readOverride(packDir, language string) (string, string, bool) {
	path := filepath.Join(packDir, language+".md")

	// Belt-and-suspenders containment check: even though
	// validateLanguageID already rejected path-traversal in
	// `language`, also confirm that the resolved file path lives
	// under packDir. Catches any future caller that constructs a
	// path outside the validation gate, and any edge case the
	// regex misses (e.g., a future encoding shift on Windows).
	if !pathInsideDir(path, packDir) {
		return "", "", false
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		// Couldn't resolve absolute path; the relative path still
		// uniquely identifies the override file from cwd.
		abs = path
	}
	return string(b), abs, true
}

// pathInsideDir returns true when filePath, after cleaning, sits
// inside dir. Used as the second line of defence against path
// traversal: validateLanguageID is the primary gate, this is the
// "what if the gate ever leaks?" check. Both paths are cleaned
// (Lexically resolved); we deliberately don't follow symlinks
// here — symlinks inside a controlled pack_dir are typically
// legitimate (e.g., shared org-wide override repo mounted in).
//
// Future hardening: when the project moves to Go 1.24+, replace
// the lexical Rel-based check with `os.Root` / `os.OpenInRoot`
// (added in Go 1.24, hardened against TOCTOU and symlink-escape
// races that any check-then-open approach inherently has). The
// current go.mod targets 1.23 so the 1.24 API isn't available
// yet; the regex gate + Rel check is sufficient for the threat
// model in the meantime. Tracked for the next Go-version bump.
func pathInsideDir(filePath, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(filePath))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
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

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
	"sort"
	"strings"

	"github.com/mshykov/local-review/internal/pathsafe"
)

//go:embed packs/*.md audit/*.md
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

	// RequireJSON appends the canonical findings JSON schema
	// (FindingsJSONSchema) to the pack body. The single-LLM fallback
	// path sets this — it parses the model's reply as JSON, so the
	// schema MUST be in the prompt. The multi-LLM path leaves it false:
	// those invokers append their own "respond in markdown, NOT JSON"
	// override (the merger consolidates prose), so injecting a JSON
	// schema there would contradict the override and risk a stray
	// JSON reply the merger can't read.
	//
	// Pre-v0.12.1 the language packs ended with "Same JSON shape as the
	// default pack" but Resolve only ever sent ONE pack — so a
	// single-LLM review of any non-default language never received the
	// actual schema. Strong cloud models inferred it; weak local models
	// (Ollama) returned JSON without the `findings` key and the review
	// failed to parse. Centralising the schema here and appending it on
	// demand fixes that for every language.
	RequireJSON bool
}

// FindingsJSONSchema is the single source of truth for the structured
// output contract. Appended to the resolved pack when
// ResolveOptions.RequireJSON is set. The parser that consumed it
// (internal/review's single-LLM path) was removed in v0.15 along with
// the rest of that package's types; the schema is kept for the planned
// opt-in structured-JSON multi-LLM mode (see CLAUDE.md), whose parser
// must match these field names + severity/tag enums.
const FindingsJSONSchema = "## Output format\n\n" +
	"Return a single JSON object with this exact shape:\n\n" +
	"```json\n" +
	"{\n" +
	"  \"findings\": [\n" +
	"    {\n" +
	"      \"file\": \"src/foo.ts\",\n" +
	"      \"line\": 42,\n" +
	"      \"severity\": \"major\",\n" +
	"      \"title\": \"Short imperative summary, < 80 chars\",\n" +
	"      \"body\": \"1–3 sentence explanation. State *why* it's a problem and *what* to do.\",\n" +
	"      \"tag\": \"security\"\n" +
	"    }\n" +
	"  ]\n" +
	"}\n" +
	"```\n\n" +
	"`file` and `line` must come from the diff. `severity` must be one of: `critical`, `major`, `warning`, `info`, `nit`. " +
	"`tag` is optional (use one of: `correctness`, `security`, `perf`, `maintainability`, `error_handling`, `testing`, `compat`, `ux`, `ethics`, `style`, `specialist`). " +
	"If there are no findings, return `{\"findings\": []}`."

// BaselinePrompt is the minimal "raw model" system prompt the
// bench's --uplift mode uses to measure what local-review's
// language-specific packs add over a generic prompt. Deliberately
// generic — the kind of thing a developer would type into
// Claude.app or ChatGPT without specialised tooling. Honest
// baseline: short, asks for bugs, asks for file:line locations
// (so the bench parser can score the output), asks for brevity.
//
// Don't tune this for performance. The point is to measure what
// a no-effort baseline produces, then show the delta against the
// shipped pack. Tuning the baseline would inflate "uplift" by
// making the comparison artificially low.
const BaselinePrompt = `You are a code reviewer. Read the following git diff and list any bugs, security issues, or other problems you find.

For each finding, include the file path and line number using "path/to/file.ext:LINE" format so they can be located.

Be concise. Don't praise the code; only report problems. If you find nothing, say so.`

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

	// Inject the canonical JSON output schema into the pack body when
	// the caller will parse JSON (single-LLM path). Done here — before
	// the prepend/append wrapping below — so the model sees the pack's
	// rules, then the output contract, and finally any user-supplied
	// Append (which still lands last and can refine output shape).
	// See ResolveOptions.RequireJSON for why the multi-LLM path skips it.
	if opts.RequireJSON {
		body = strings.TrimRight(body, "\n") + "\n\n" + FindingsJSONSchema
	}

	content := body
	tags := []string{}
	if strings.TrimSpace(opts.Prepend) != "" {
		content = opts.Prepend + "\n\n" + content
		tags = append(tags, "prepend")
	}
	if strings.TrimSpace(opts.Append) != "" {
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
	if !pathsafe.InsideDir(path, packDir) {
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

// Available returns the language ids that have dedicated packs.
func Available() ([]string, error) {
	return listMarkdownStems("packs")
}

// GetAuditPack returns the embedded audit pack for a given topic
// (e.g. "security", "tech-debt"). Returns an actionable error
// naming the available topics when the topic is unknown — audit is
// user-facing, so a typo at the CLI must produce a helpful message,
// not a stack trace. v0.10.0-c: no user-override mechanism yet
// (mirrors the early days of language packs before pack_dir / prepend
// / append landed); the audit topics are deliberately fewer and more
// curated than language packs.
func GetAuditPack(topic string) (string, error) {
	if topic == "" {
		return "", fmt.Errorf("audit topic is required (try --topic security or --topic tech-debt)")
	}
	// Reuse the language-id regex: lowercase alphanumeric + underscore
	// + dash, no leading separator. Refuses path traversal even
	// though embed.FS already constrains reads to the embedded tree.
	if !languageIDRE.MatchString(topic) {
		return "", fmt.Errorf("audit topic %q contains invalid characters (allowed: lowercase alphanumeric, dash, underscore; must start with a letter or digit)", topic)
	}
	b, err := fs.ReadFile("audit/" + topic + ".md")
	if err != nil {
		// Propagate the listing error when present — a failure to
		// enumerate available topics means the binary's embed is
		// corrupted and the user needs to know that, not see an
		// empty "(available: )" parenthetical. CLAUDE.md rule 4.
		avail, listErr := AvailableAuditTopics()
		if listErr != nil {
			return "", fmt.Errorf("audit topic %q not found (also failed to list available topics: %w)", topic, listErr)
		}
		return "", fmt.Errorf("audit topic %q not found (available: %s)", topic, strings.Join(avail, ", "))
	}
	return string(b), nil
}

// AvailableAuditTopics returns the topic ids that have dedicated
// audit packs. Same discovery shape as Available() for language
// packs — read the embedded directory at runtime so new topics
// dropped into internal/prompts/audit/ are picked up automatically.
func AvailableAuditTopics() ([]string, error) {
	return listMarkdownStems("audit")
}

// listMarkdownStems is the shared helper behind Available and
// AvailableAuditTopics: enumerate `.md` files in an embed.FS
// subdirectory and return their stems (sorted for deterministic
// CLI output and `--help` listings).
func listMarkdownStems(dir string) ([]string, error) {
	entries, err := fs.ReadDir(dir)
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
	sort.Strings(ids)
	return ids, nil
}

package audit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/lang"
)

// Chunk is one unit of audit work — all the source from one
// directory, ready to feed into the LLM. The walker produces
// these; the runner consumes them.
type Chunk struct {
	// Package is the repo-relative directory path. Files at the
	// repo root are bucketed under ".".
	Package string

	// Files lists the repo-relative paths included in this chunk,
	// sorted for deterministic ordering across audit runs.
	Files []string

	// Body is the LLM-ready concatenation: each file preceded by a
	// `// === FILE: <path> ===` marker. The audit pack tells the LLM
	// to expect that delimiter shape.
	Body string

	// SizeBytes is the total size of Body — used by the runner to
	// warn / split when a package would overflow a typical LLM
	// context window. v1 just emits a warning; the soft split
	// strategy is deferred.
	SizeBytes int
}

// WalkOptions configure which tracked files the walker considers.
// Zero value = "every tracked text file under the working tree."
type WalkOptions struct {
	// Root is the working-tree root the audit operates against.
	// Empty = current working directory.
	Root string

	// Include / Exclude are optional path-prefix filters. Both
	// match against the repo-relative path. Include wins (when
	// non-empty) — only files under one of the Include prefixes
	// are considered, then Exclude further removes matches. Used
	// by `--include` / `--exclude` CLI flags so users can audit
	// just one subdirectory.
	Include []string
	Exclude []string

	// MaxBytesPerChunk soft-caps the LLM input. A chunk over this
	// size emits a warning via Warn (when non-nil) at walk time;
	// the runner still sends the full chunk and lets the LLM
	// truncate / refuse — splitting an over-sized chunk usefully
	// would need package-internal knowledge (e.g. import graphs)
	// the audit doesn't have. Soft warning is honest about that.
	// Zero = use the package default (256 KiB).
	MaxBytesPerChunk int

	// Warn, when non-nil, receives a one-line message per
	// over-sized chunk so the user sees the warning before paying
	// LLM tokens on a chunk that may not survive the context
	// window. The CLI wires this to os.Stderr; tests pass nil.
	Warn io.Writer
}

// defaultMaxBytesPerChunk: 96 KiB. Empirical default — claude-code
// (the wrapper the multi-LLM path shells out to) returns
// `prompt_too_long` on chunks at ~176 KiB through Claude Haiku 4.5,
// well below the model's nominal 200K-token context. The wrapper
// adds its own system prompt + tool definitions on top of the
// audit pack body, so the effective budget for chunk content is a
// good bit smaller than the raw model context. 96 KiB leaves
// reliable headroom across Claude / Gemini / Codex and keeps
// response latency reasonable. Big packages get auto-split (see
// splitChunk below) rather than failing.
const defaultMaxBytesPerChunk = 96 * 1024

// Walk returns one Chunk per directory containing audit-eligible
// files. Directories with no eligible files are skipped silently
// (no empty chunks in the output).
//
// Eligibility:
//   - File is tracked by git (we use TrackedFiles to enumerate).
//   - File extension maps to a known language pack via
//     internal/lang.Detect, OR matches isAuditable's small
//     built-in allowlist of common config / build / script
//     shapes (Bash, YAML, SQL, Dockerfile, Terraform). Files
//     whose extension lang.Detect classifies as `default` are
//     SKIPPED (binary / image / archive / unknown text) unless
//     they're on the allowlist — keeping audit input focused on
//     source the LLM can usefully reason about.
//   - File survives the Include/Exclude filters.
//
// Output is sorted by Package (alphabetical) for deterministic
// audit runs.
func Walk(opts WalkOptions) ([]Chunk, error) {
	// Resolve the repo root once via `git rev-parse
	// --show-toplevel` and thread it both to TrackedFiles (so
	// `git -C <root> ls-files` runs against the right tree) and
	// to concatFiles (the on-disk read root). Single resolution,
	// no redundant rev-parse — Copilot flagged the duplicate
	// lookup on PR #73.
	root := opts.Root
	if root == "" {
		r, err := git.RepoRoot()
		if err != nil {
			return nil, fmt.Errorf("resolve repo root: %w", err)
		}
		root = r
	}
	files, err := git.TrackedFiles(root)
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}

	maxBytes := opts.MaxBytesPerChunk
	if maxBytes == 0 {
		maxBytes = defaultMaxBytesPerChunk
	}

	// Group eligible files by package (directory).
	byPkg := map[string][]string{}
	for _, p := range files {
		if !pathPassesFilters(p, opts.Include, opts.Exclude) {
			continue
		}
		if !isAuditable(p) {
			continue
		}
		pkg := filepath.Dir(p)
		if pkg == "" {
			pkg = "."
		}
		byPkg[pkg] = append(byPkg[pkg], p)
	}

	// Build chunks. Sort packages so the audit output order is
	// stable; sort files within each package for the same reason.
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	out := make([]Chunk, 0, len(pkgs))
	for _, pkg := range pkgs {
		paths := byPkg[pkg]
		sort.Strings(paths)
		parts, err := splitChunk(root, pkg, paths, maxBytes, opts.Warn)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// splitChunk turns one package's files into one OR MORE Chunks,
// bin-packing files so each chunk stays under maxBytes. Packages
// that fit in a single chunk emit one Chunk with the package
// name unchanged; oversized packages emit multiple Chunks tagged
// `pkg [part N/M]` so the report attribution stays readable.
//
// The greedy bin-packing is intentional: it preserves file
// adjacency (files A, B, C, D in that order end up as [A,B] +
// [C,D] not [A,D] + [B,C]). LLMs do better when a chunk's files
// are related, and `git ls-files` already returns files in tree
// order, so adjacency is a useful proxy for "related."
//
// Edge case — a single file larger than maxBytes — emits a
// one-file chunk that exceeds the cap and surfaces a warning via
// opts.Warn (when non-nil). The LLM will probably refuse with
// `prompt_too_long`, but splitting a single source file at an
// arbitrary line boundary would produce nonsense chunks (split
// mid-function, no scope context), so the alternative is worse.
// v1: surface the size + skip nothing; future work could add a
// per-file split strategy keyed on language (e.g., split Go at
// `func` boundaries).
//
// Caught by user-reported failures on PR #73's first dogfood:
// the 256 KiB soft cap was too high empirically and the runner
// just shipped oversized chunks to the LLM, which returned
// `prompt_too_long` on every one. Out of 343 Android packages,
// 321 errored that way.
func splitChunk(root, pkg string, paths []string, maxBytes int, warn io.Writer) ([]Chunk, error) {
	type bin struct {
		files []string
		size  int
	}
	var bins []bin
	cur := bin{}
	for _, p := range paths {
		full := filepath.Join(root, p)
		info, statErr := os.Stat(full)
		if statErr != nil {
			return nil, fmt.Errorf("stat %s: %w", p, statErr)
		}
		// Per-file overhead estimate: the `// === FILE: <p> ===\n`
		// marker plus a trailing newline. Small but accumulates.
		fileSize := int(info.Size()) + len(p) + fileMarkerOverhead
		// Single file over the cap: flush the current bin (if any)
		// to keep its bin-mate files together, then emit the
		// oversized file as its own bin. The warning fires for
		// the user's awareness.
		if fileSize > maxBytes {
			if len(cur.files) > 0 {
				bins = append(bins, cur)
				cur = bin{}
			}
			if warn != nil {
				_, _ = fmt.Fprintf(warn, "warning: file %s is %s (over %s per-chunk cap); cannot split a single file across chunks, LLM may refuse this chunk\n",
					p, FormatBytes(fileSize), FormatBytes(maxBytes))
			}
			bins = append(bins, bin{files: []string{p}, size: fileSize})
			continue
		}
		// Would this file overflow the current bin? Start a new
		// one. The check is "current + new > cap" so a freshly
		// flushed bin can still take a sub-cap file.
		if cur.size+fileSize > maxBytes && len(cur.files) > 0 {
			bins = append(bins, cur)
			cur = bin{}
		}
		cur.files = append(cur.files, p)
		cur.size += fileSize
	}
	if len(cur.files) > 0 {
		bins = append(bins, cur)
	}

	out := make([]Chunk, 0, len(bins))
	for i, b := range bins {
		body, size, err := concatFiles(root, b.files)
		if err != nil {
			return nil, err
		}
		label := pkg
		if len(bins) > 1 {
			label = fmt.Sprintf("%s [part %d/%d]", pkg, i+1, len(bins))
		}
		out = append(out, Chunk{
			Package:   label,
			Files:     b.files,
			Body:      body,
			SizeBytes: size,
		})
	}
	return out, nil
}

// fileMarkerOverhead approximates the bytes the `// === FILE: <p>
// ===\n` marker adds per file in concatFiles' output. Used by
// splitChunk's pre-flight bin-packing — we estimate from
// os.Stat(file.Size()) + this constant rather than reading the
// file twice (once for sizing, once for concatenation). A small
// over-estimate is fine; an under-estimate would let chunks creep
// past maxBytes.
const fileMarkerOverhead = len("// === FILE: ") + len(" ===\n") + 1 // +1 for the possibly-missing trailing newline

// concatFiles reads each file under root and concatenates with a
// `// === FILE: <path> ===` marker that the audit pack tells the
// LLM to expect. Returns (body, size, err).
//
// File-read errors are returned verbatim — the walker can't tell
// "transient I/O" from "file deleted between ls-files and read";
// either way, the audit should surface the failure rather than
// silently shrink the chunk.
func concatFiles(root string, paths []string) (string, int, error) {
	var b strings.Builder
	for _, p := range paths {
		// Note: git ls-files returns paths relative to repo root,
		// not to the current working directory. The audit runs
		// from any cwd in the worktree, so use root explicitly.
		full := filepath.Join(root, p)
		data, err := os.ReadFile(full)
		if err != nil {
			return "", 0, fmt.Errorf("read %s: %w", p, err)
		}
		b.WriteString("// === FILE: ")
		b.WriteString(p)
		b.WriteString(" ===\n")
		b.Write(data)
		// Ensure each file ends in a newline so the next marker
		// starts on its own line even when the source didn't.
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	body := b.String()
	return body, len(body), nil
}

// pathPassesFilters applies the include / exclude prefix filters
// from WalkOptions. Empty Include = no include filter (everything
// passes the include test); non-empty Exclude always filters.
//
// Path matching is directory-boundary-aware: the prefix must
// match either the whole path or a prefix followed by `/`. Raw
// HasPrefix would have matched `internal/cli2/foo.go` against
// `internal/cli` and pulled it into the wrong filter — CodeRabbit
// caught this on PR #73.
func pathPassesFilters(path string, include, exclude []string) bool {
	if len(include) > 0 {
		matched := false
		for _, prefix := range include {
			if pathHasPrefix(path, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, prefix := range exclude {
		if pathHasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

// pathHasPrefix returns true when path is exactly prefix or sits
// under prefix as a directory entry. `internal/cli` matches
// `internal/cli/foo.go` and `internal/cli` itself but NOT
// `internal/cli2/foo.go`. Trailing `/` on the prefix is tolerated
// so users can write either `--exclude bench/` or `--exclude bench`.
func pathHasPrefix(path, prefix string) bool {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return true
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// isAuditable returns true when the file's extension maps to a
// known language pack OR matches a built-in allowlist of audit-
// eligible shapes (build files, configs that often hide bugs).
// Binary / image / archive / lockfile / minified-vendor files are
// skipped — the LLM can't usefully audit them and they bloat the
// chunk past context windows.
func isAuditable(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))

	// Known source extensions via the existing lang package.
	if lang.Detect(path) != lang.Default {
		return true
	}

	// Built-in extras: files whose extension lang.Detect doesn't
	// know but which carry audit-relevant content. Keep this list
	// short and obvious — the audit is not the place to enumerate
	// every config shape under the sun.
	switch ext {
	case ".sh", ".bash", ".zsh":
		return true
	case ".yml", ".yaml":
		// CI workflows, k8s manifests, GitHub Actions — common
		// site of injected-input bugs and secrets-by-accident.
		return true
	case ".sql":
		return true
	case ".dockerfile":
		return true
	case ".tf":
		return true
	}

	// Build-system bare-name files (no extension or special name).
	switch base {
	case "dockerfile", "makefile", "rakefile":
		return true
	}

	// Lockfiles are skipped — they're not human-edited, audit
	// findings on them are noise.
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "gemfile.lock", "podfile.lock", "composer.lock", "poetry.lock":
		return false
	}
	return false
}

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

	// MaxBytesPerChunk caps the LLM input per chunk. Packages over
	// this size are auto-split into `pkg [part N/M]` sub-chunks via
	// the greedy bin-packer in splitChunk; single files individually
	// over the cap surface a warning and pass through as one chunk
	// (splitting a source file at an arbitrary line boundary would
	// produce semantically broken chunks). Zero = use the package
	// default (96 KiB — empirical headroom needed for claude-code's
	// own system prompt + tool definitions on top of the audit pack
	// body). Negative values are rejected by Walk at load time —
	// silently letting a negative cap flow through would make every
	// file appear oversized and produce nonsense warnings.
	MaxBytesPerChunk int

	// Warn, when non-nil, receives a one-line message per
	// over-sized chunk so the user sees the warning before paying
	// LLM tokens on a chunk that may not survive the context
	// window. The CLI wires this to os.Stderr; tests pass nil.
	//
	// Walk is sequential — writes to Warn happen one at a time
	// from a single goroutine, so the writer doesn't need to be
	// thread-safe. If Walk ever fans out across goroutines
	// (currently it doesn't; per-chunk LLM calls already give
	// us enough parallelism upstream in Run), this field's
	// contract changes and synchronization moves into the
	// caller.
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
	// Validate caller-supplied options up-front before doing
	// any I/O (git ls-files, file reads). Cheap fail-fast for
	// obvious misuse — CLAUDE.md rule 4 (fail loud, fail
	// closed). A negative cap would make every file appear
	// oversized in splitChunk and produce nonsense warnings;
	// refuse at the entry so the caller sees the bug instead
	// of a wall of misleading stderr. Caught by CodeRabbit on
	// PR #74.
	if opts.MaxBytesPerChunk < 0 {
		return nil, fmt.Errorf("MaxBytesPerChunk must be >= 0 (got %d); use 0 for the default", opts.MaxBytesPerChunk)
	}

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
// the provided warn writer (when non-nil). The LLM will probably refuse with
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
	bins, err := packBins(root, paths, maxBytes, warn)
	if err != nil {
		return nil, err
	}
	return assembleChunks(root, pkg, bins, maxBytes, warn)
}

// fileBin holds one group of files that should be sent to the LLM
// as a single chunk. Internal to splitChunk's bin-packing pass;
// not exported to keep the public surface small.
type fileBin struct {
	files []string
	size  int // estimated; actual concat size is computed in assembleChunks
}

// packBins is splitChunk's bin-packing pass: walks the input paths
// in order and produces []fileBin, each respecting maxBytes
// (except single-file bins where the file alone exceeds the cap —
// those get a warning and pass through). Pulled out of splitChunk
// so the cognitive-complexity budget on the orchestrator stays
// under Sonar's threshold; same decomposition pattern as
// cmd/local-review/runner.go.
func packBins(root string, paths []string, maxBytes int, warn io.Writer) ([]fileBin, error) {
	var bins []fileBin
	cur := fileBin{}
	for _, p := range paths {
		fileSize, err := estimateFileSize(root, p)
		if err != nil {
			return nil, err
		}
		if fileSize > maxBytes {
			bins = flushAndAppendOversized(bins, &cur, p, fileSize, maxBytes, warn)
			continue
		}
		if cur.size+fileSize > maxBytes && len(cur.files) > 0 {
			bins = append(bins, cur)
			cur = fileBin{}
		}
		cur.files = append(cur.files, p)
		cur.size += fileSize
	}
	if len(cur.files) > 0 {
		bins = append(bins, cur)
	}
	return bins, nil
}

// estimateFileSize returns the bin-packing estimate for one file:
// on-disk size + the per-file marker overhead concatFiles will
// emit around it. Doesn't read the file body — uses os.Stat to
// keep the bin-pack pass cheap (single stat per file vs reading
// every file twice).
//
// Returns an error if the file's on-disk size doesn't fit in an
// int (i.e., > 2 GiB on a 32-bit build). Audit chunks max out at
// the per-chunk cap (96 KiB by default; never more than a couple
// of MiB in any reasonable user config), so a multi-GiB single
// file is either a binary that slipped past isAuditable's
// allowlist or a genuine I/O misadventure — either way, failing
// here is better than wrapping arithmetic. Caught by Copilot on
// PR #74 — int64-to-int cast was unguarded.
func estimateFileSize(root, p string) (int, error) {
	full := filepath.Join(root, p)
	info, err := os.Stat(full)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", p, err)
	}
	rawSize := info.Size()
	if rawSize < 0 || rawSize > int64(maxIntValue) {
		return 0, fmt.Errorf("stat %s: file size %d does not fit in int (32-bit build with a >2GiB file?)", p, rawSize)
	}
	return int(rawSize) + len(p) + fileMarkerOverhead, nil
}

// maxIntValue is platform-dependent int max — the upper bound for
// the int64-to-int conversion in estimateFileSize. On 64-bit
// builds (the project's release targets: darwin/linux/windows ×
// amd64/arm64) this is 2^63-1 and the bounds check effectively
// never fires; on 32-bit builds it's 2^31-1 and a >2GiB file
// would otherwise wrap. ^uint(0) >> 1 is the standard
// int-max idiom that doesn't require importing math.
const maxIntValue = int(^uint(0) >> 1)

// flushAndAppendOversized handles the single-file-over-cap case:
// flushes the current bin (so its bin-mates stay together), warns
// the user, and emits the oversized file as its own bin. Returns
// the new bins slice; the caller's cur bin is reset to empty.
func flushAndAppendOversized(bins []fileBin, cur *fileBin, p string, fileSize, maxBytes int, warn io.Writer) []fileBin {
	if len(cur.files) > 0 {
		bins = append(bins, *cur)
		*cur = fileBin{}
	}
	if warn != nil {
		// fileSize includes the per-file marker overhead (~30
		// bytes for the `// === FILE: <p> ===\n` line); say
		// "estimated chunk contribution" rather than "file size"
		// so a user with a 100 KiB file doesn't wonder why the
		// warning says 100.1 KiB. Copilot caught the wording
		// mismatch on PR #74.
		_, _ = fmt.Fprintf(warn, "warning: file %s has an estimated chunk contribution of %s (over %s per-chunk cap); cannot split a single file across chunks, LLM may refuse this chunk\n",
			p, FormatBytes(fileSize), FormatBytes(maxBytes))
	}
	return append(bins, fileBin{files: []string{p}, size: fileSize})
}

// assembleChunks turns the bin-packer's []fileBin into the final
// []Chunk: concatenates each bin's files, labels multi-bin
// packages as `pkg [part N/M]`, and surfaces the post-flight
// drift warning if the actual concat size exceeded the estimate
// for a multi-file bin (single-file bins were already warned
// about during packing, so no double-fire here).
func assembleChunks(root, pkg string, bins []fileBin, maxBytes int, warn io.Writer) ([]Chunk, error) {
	out := make([]Chunk, 0, len(bins))
	for i, b := range bins {
		body, size, err := concatFiles(root, b.files)
		if err != nil {
			return nil, err
		}
		warnPostFlightDrift(pkg, i, len(bins), b.files, size, maxBytes, warn)
		out = append(out, Chunk{
			Package:   labelChunk(pkg, i, len(bins)),
			Files:     b.files,
			Body:      body,
			SizeBytes: size,
		})
	}
	return out, nil
}

// warnPostFlightDrift fires when a multi-file bin's actual concat
// size exceeded the cap that the bin-pack estimate said it should
// fit under. Belt-and-braces guard: if fileMarkerOverhead ever
// drifts low or concatFiles ever changes its emission shape, this
// catches it loudly instead of silently shipping an oversized
// chunk. Single-file bins skip — they were warned about during
// packing.
func warnPostFlightDrift(pkg string, i, n int, files []string, size, maxBytes int, warn io.Writer) {
	if size <= maxBytes || len(files) <= 1 || warn == nil {
		return
	}
	// "over %s cap" is the honest framing: maxBytes is the cap the
	// bin-packer aimed to stay under, not an estimate. The drift is
	// between the pre-concat size estimate and the actual concat
	// size; the cap is the cap. Copilot caught the wording on PR #74.
	_, _ = fmt.Fprintf(warn, "warning: %s chunk %d/%d packed to %s after concat (over %s cap); bin-pack overhead drift, LLM may refuse this chunk\n",
		pkg, i+1, n, FormatBytes(size), FormatBytes(maxBytes))
}

// labelChunk renders the per-chunk package name: just the pkg
// name when there's a single bin, `pkg [part N/M]` when there
// are multiple.
func labelChunk(pkg string, i, n int) string {
	if n <= 1 {
		return pkg
	}
	return fmt.Sprintf("%s [part %d/%d]", pkg, i+1, n)
}

// fileMarkerOverhead is the per-file byte overhead concatFiles adds
// beyond the file's own contents — the `// === FILE: <p> ===\n`
// header plus the conditional trailing `\n` it appends when the
// file body doesn't already end with one. Used by splitChunk's
// pre-flight bin-packing to estimate chunk size from
// `os.Stat(file).Size() + len(p) + fileMarkerOverhead` rather
// than reading every file twice (size pass + concat pass).
//
// Concretely: len("// === FILE: ") = 13, len(" ===\n") = 5, plus
// 1 byte for the worst-case trailing newline. Total 19. The +1
// over-estimates by exactly 1 byte for files that already end
// with a newline (the common case) — at a 96 KiB cap that's a
// 0.001% over-estimate per file, which is negligible. An
// under-estimate would be worse: chunks could creep past maxBytes
// and resurface the prompt_too_long failures this whole feature
// is supposed to prevent.
const fileMarkerOverhead = len("// === FILE: ") + len(" ===\n") + 1

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
//
// IMPORTANT order-of-operations: the skip list at the top runs
// BEFORE the extension allowlist below. Pre-v0.15.1 the order was
// reversed — `pnpm-lock.yaml` matched `.yaml` (allowlist returns
// true) before the base-name `switch` got a chance to skip it, so
// a 272 KiB lockfile ended up as its own audit chunk and burned
// ~5 minutes on Ollama. Surfaced by a real-world v0.15 dogfood.
func isAuditable(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))

	// HARD SKIP: lockfiles + obvious generated/minified content.
	// These match BEFORE any allowlist below — file-extension can
	// agree with an allowlist (e.g. .yaml → pnpm-lock.yaml) but
	// the base-name still wins because there's no useful audit
	// signal in a lockfile or minified bundle.
	switch base {
	case "go.sum",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"npm-shrinkwrap.json",
		"bun.lockb",
		"cargo.lock",
		"gemfile.lock",
		"podfile.lock",
		"composer.lock",
		"poetry.lock",
		"pipfile.lock",
		"mix.lock",
		"pubspec.lock",
		"flake.lock":
		return false
	}
	// Minified bundles — same logic: the source lives elsewhere.
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".min.map") {
		return false
	}

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

	return false
}

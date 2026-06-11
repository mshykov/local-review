// Package pathsafe holds the symlink-aware path-containment check used to
// keep untrusted, config-supplied paths (prompts.pack_dir and the files
// under it) from escaping their intended directory.
//
// It exists as a single source of truth on purpose: the check lived in
// BOTH internal/config and internal/prompts as separate copies, and that
// duplication caused a real vulnerability — internal/config got the v0.10.5
// symlink hardening while the internal/prompts copy stayed lexical-only,
// allowing `pack_dir/<lang>.md -> /etc/passwd` to leak arbitrary files into
// the LLM prompt (fixed in v0.17.1). A security primitive must not be
// duplicated where the copies can drift; both packages now call InsideDir.
package pathsafe

import (
	"path/filepath"
	"strings"
)

// InsideDir reports whether filePath sits inside dir, AFTER resolving
// symlinks on both sides. This MUST resolve symlinks: callers open the
// resulting path with os.ReadFile (which follows symlinks), so a
// lexical-only check would admit `dir/leaf -> /outside` escapes.
//
// Algorithm:
//   - Lexical containment first — cheap, fails fast on `..` escapes.
//   - Then EvalSymlinks on dir and a deepest-existing-ancestor walk-up of
//     filePath (the leaf may not exist yet), checking the resolved real
//     paths. This catches the lexically-inside-but-symlinks-out case
//     (e.g. dir/link -> /etc, or dir itself being a symlink).
//
// Fail-closed: any resolve failure returns false rather than admitting the
// path on the weaker lexical pass alone. A `..` is rejected lexically before
// the symlink branch runs, so the symlink branch can only ADD rejections.
//
// Future hardening: on Go 1.24+, os.Root / os.OpenInRoot closes the residual
// TOCTOU race any check-then-open approach inherently has.
func InsideDir(filePath, dir string) bool {
	cleanedDir := filepath.Clean(dir)
	cleanedPath := filepath.Clean(filePath)

	rel, err := filepath.Rel(cleanedDir, cleanedPath)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	resolvedDir, derr := filepath.EvalSymlinks(cleanedDir)
	if derr != nil {
		return false
	}
	resolvedAncestor := deepestExistingAncestor(cleanedPath)
	if resolvedAncestor == "" {
		return false
	}
	relReal, err := filepath.Rel(resolvedDir, resolvedAncestor)
	if err != nil {
		return false
	}
	if relReal == ".." || strings.HasPrefix(relReal, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// deepestExistingAncestor returns the EvalSymlinks-resolved real path of the
// deepest existing prefix of path, walking up via filepath.Dir until
// EvalSymlinks succeeds. This closes the missing-leaf bypass (a non-existent
// leaf hiding a parent that already resolves outside the base). Returns ""
// only when even the root fails to resolve — callers treat that as
// fail-closed.
func deepestExistingAncestor(path string) string {
	cur := path
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

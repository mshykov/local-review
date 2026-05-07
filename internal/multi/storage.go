// Package multi handles multi-LLM orchestration, storage, and merging.
package multi

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mshykov/local-review/internal/git"
)

// ReviewStorage handles saving review outputs to disk.
type ReviewStorage struct {
	basePath string
}

// NewStorage creates a new ReviewStorage with the given base path.
func NewStorage(basePath string) *ReviewStorage {
	return &ReviewStorage{basePath: basePath}
}

// unsafeFilenameChars matches anything that isn't safe in a filename
// across darwin/linux/windows: path separators, drive prefixes, NUL,
// shell metacharacters, and the windows-reserved characters
// (`<>:"|?*`). Anything matched gets replaced with `-` to keep
// filenames predictable across platforms.
//
// The character class is intentionally a deny-list rather than the
// stricter allow-list `[^A-Za-z0-9._-]` because LLM names and
// version strings can legitimately include `+` (semver build
// metadata: `2.1.132-rc.1+build.42`) or other innocuous chars that
// the allow-list would scrub. We only deny the genuinely dangerous
// set.
var unsafeFilenameChars = regexp.MustCompile(`[/\\:<>"|?*\x00\s]`)

// sanitizeFilenameComponent makes an LLM name or version string safe
// to embed in a filename. Used for `<commit>_<llm>_<version>.md`
// where LLM names come from a trusted detector (claude/gemini/codex)
// but version strings come from CLI version probes — which are
// vendor-controlled. A vendor that ships a banner like `v2.1.132
// (rc/staging)` would otherwise produce a filename with embedded
// path separators and break the storage layout. Defensive
// sanitisation keeps the contract one-directory-per-branch even as
// vendor CLIs evolve.
//
// Empty input returns "unknown" so we don't end up with collapsed
// double-underscores in the filename (`<commit>__<version>.md`).
func sanitizeFilenameComponent(s string) string {
	if s == "" {
		return "unknown"
	}
	cleaned := unsafeFilenameChars.ReplaceAllString(s, "-")
	// Collapse runs of `-` from the replacement (e.g. " / " → "---").
	for i := 0; i < 3 && len(cleaned) > 0; i++ {
		next := regexp.MustCompile(`-+`).ReplaceAllString(cleaned, "-")
		if next == cleaned {
			break
		}
		cleaned = next
	}
	// Don't allow leading/trailing `-` (cosmetic but shows up in
	// shell completion and the printed "Per-LLM reviews → ..." path).
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// SaveReview writes an LLM's review output to disk.
// Returns the path to the saved file.
func (s *ReviewStorage) SaveReview(branch, commit, llmName, llmVersion, content string) (string, error) {
	// Sanitize all path components. branch and commit have their
	// own dedicated sanitizers (in internal/git); llmName and
	// llmVersion go through the local sanitizer because they come
	// from vendor-controlled version strings that could legitimately
	// contain filesystem-unsafe characters in future CLI releases.
	sanitizedBranch := git.SanitizeBranchName(branch)
	sanitizedCommit := git.SanitizeCommit(commit)
	sanitizedLLM := sanitizeFilenameComponent(llmName)
	sanitizedVersion := sanitizeFilenameComponent(llmVersion)

	// Create directory structure: <basePath>/<branch>/
	// Note: MkdirAll is idempotent and called in each Save* method for robustness
	dir := filepath.Join(s.basePath, sanitizedBranch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create review directory: %w", err)
	}

	// File name: <commit>_<llm>_<version>.md
	filename := fmt.Sprintf("%s_%s_%s.md", sanitizedCommit, sanitizedLLM, sanitizedVersion)
	path := filepath.Join(dir, filename)

	// Write content (owner-only permissions for sensitive source code)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write review file: %w", err)
	}

	return path, nil
}

// SaveMerged writes the merged review to disk.
// Returns the path to the saved file.
func (s *ReviewStorage) SaveMerged(branch, commit, content string) (string, error) {
	sanitizedBranch := git.SanitizeBranchName(branch)
	sanitizedCommit := git.SanitizeCommit(commit)

	dir := filepath.Join(s.basePath, sanitizedBranch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create review directory: %w", err)
	}

	filename := fmt.Sprintf("%s_merged.md", sanitizedCommit)
	path := filepath.Join(dir, filename)

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write merged file: %w", err)
	}

	return path, nil
}

// SaveMetadata writes the metadata JSON to disk.
// Returns the path to the saved file.
func (s *ReviewStorage) SaveMetadata(branch, commit string, meta *Metadata) (string, error) {
	sanitizedBranch := git.SanitizeBranchName(branch)
	sanitizedCommit := git.SanitizeCommit(commit)

	dir := filepath.Join(s.basePath, sanitizedBranch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create review directory: %w", err)
	}

	filename := fmt.Sprintf("%s_metadata.json", sanitizedCommit)
	path := filepath.Join(dir, filename)

	if err := meta.Save(path); err != nil {
		return "", fmt.Errorf("save metadata: %w", err)
	}

	return path, nil
}

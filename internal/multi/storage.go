// Package multi handles multi-LLM orchestration, storage, and merging.
package multi

import (
	"fmt"
	"os"
	"path/filepath"

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

// SaveReview writes an LLM's review output to disk.
// Returns the path to the saved file.
func (s *ReviewStorage) SaveReview(branch, commit, llmName, llmVersion, content string) (string, error) {
	// Sanitize branch name and commit for filesystem
	sanitizedBranch := git.SanitizeBranchName(branch)
	sanitizedCommit := git.SanitizeCommit(commit)

	// Create directory structure: <basePath>/<branch>/
	// Note: MkdirAll is idempotent and called in each Save* method for robustness
	dir := filepath.Join(s.basePath, sanitizedBranch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create review directory: %w", err)
	}

	// File name: <commit>_<llm>_<version>.md
	filename := fmt.Sprintf("%s_%s_%s.md", sanitizedCommit, llmName, llmVersion)
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

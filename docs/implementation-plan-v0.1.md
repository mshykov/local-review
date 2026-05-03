# Implementation Plan: v0.1 Multi-LLM Review

This document provides a step-by-step implementation plan for local-review v0.1's multi-LLM feature.

## Overview

**Goal:** Enable parallel code reviews from 4 LLMs (Claude, Gemini, Codex, Copilot), merge findings intelligently, and store results locally.

**Estimated Scope:** 5 phases, ~20 tasks

**Key Principles:**
- Start with CLI detection (non-breaking)
- Build incrementally (each phase is testable)
- Maintain v0 compatibility throughout

---

## Phase 1: CLI Infrastructure (Foundation)

**Goal:** Detect installed LLM CLIs and extract their versions.

### Task 1.1: Create CLI Detection Module
**File:** `internal/cli/detector.go`

```go
package cli

import (
    "os/exec"
)

// LLM represents a detected CLI tool
type LLM struct {
    Name      string   // "claude", "gemini", "codex", "copilot"
    Path      string   // "/usr/local/bin/claude"
    Version   string   // "2.1.0"
    Available bool
}

// DetectAll checks for all supported LLM CLIs
func DetectAll() ([]LLM, error) {
    llms := []string{"claude", "gemini", "codex", "gh"}
    results := make([]LLM, 0, len(llms))

    for _, name := range llms {
        path, err := exec.LookPath(name)
        if err != nil {
            results = append(results, LLM{Name: name, Available: false})
            continue
        }

        version := detectVersion(name, path)
        results = append(results, LLM{
            Name:      name,
            Path:      path,
            Version:   version,
            Available: true,
        })
    }

    return results, nil
}

// Detect checks if a specific LLM CLI is installed
func Detect(name string) (LLM, error) {
    // implementation
}
```

**Tests:** `internal/cli/detector_test.go`
- Test with mock PATH (CLI present)
- Test with empty PATH (CLI absent)
- Test all 4 LLM names

### Task 1.2: Version Detection
**File:** `internal/cli/version.go`

```go
package cli

// detectVersion runs `<cli> --version` and parses output
func detectVersion(name, path string) string {
    switch name {
    case "claude":
        return runVersionCmd(path, "--version")
    case "gemini":
        return runVersionCmd(path, "--version")
    case "codex":
        return runVersionCmd(path, "--version")
    case "gh":
        return runVersionCmd(path, "--version")
    default:
        return "unknown"
    }
}

func runVersionCmd(path string, args ...string) string {
    // exec.Command(path, args...).Output()
    // parse version from stdout (regex: v?\d+\.\d+\.\d+)
}
```

**Tests:**
- Mock version command outputs
- Test version parsing for each CLI format

### Task 1.3: Doctor Command
**File:** `cmd/local-review/doctor.go`

```go
package main

import (
    "github.com/spf13/cobra"
    "github.com/mshykov/local-review/internal/cli"
)

func doctorCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "doctor",
        Short: "Check LLM CLI installations and authentication status",
        RunE: func(cmd *cobra.Command, args []string) error {
            llms, err := cli.DetectAll()
            if err != nil {
                return err
            }

            // Print status table
            for _, llm := range llms {
                if llm.Available {
                    fmt.Printf("✓ %s v%s\n", llm.Name, llm.Version)
                } else {
                    fmt.Printf("✗ %s (not found)\n", llm.Name)
                    printInstallInstructions(llm.Name)
                }
            }

            return nil
        },
    }
}

func printInstallInstructions(name string) {
    switch name {
    case "claude":
        fmt.Println("  Install: npm install -g @anthropic/claude-cli")
    case "gemini":
        fmt.Println("  Install: npm install -g @google/gemini-cli@0.40.0")
    case "codex":
        fmt.Println("  Install: npm install -g @openai/codex@0.128.0")
    case "gh":
        fmt.Println("  Install: brew install gh && gh auth login")
    }
}
```

**Hook into main.go:**
```go
root.AddCommand(doctorCmd())
```

**Manual Test:**
```sh
go build -o local-review ./cmd/local-review
./local-review doctor
```

---

## Phase 2: CLI Invocation

**Goal:** Execute LLM CLI commands and capture output.

### Task 2.1: CLI Invoker Interface
**File:** `internal/cli/invoker.go`

```go
package cli

import (
    "context"
    "github.com/mshykov/local-review/internal/git"
)

// Invoker runs an LLM CLI with a diff and returns the review
type Invoker interface {
    Review(ctx context.Context, diff string) (string, error)
}

// NewInvoker creates an invoker for the given LLM
func NewInvoker(llm LLM) Invoker {
    switch llm.Name {
    case "claude":
        return &ClaudeInvoker{path: llm.Path}
    case "gemini":
        return &GeminiInvoker{path: llm.Path}
    case "codex":
        return &CodexInvoker{path: llm.Path}
    case "copilot":
        return &CopilotInvoker{path: llm.Path}
    default:
        return nil
    }
}
```

### Task 2.2: Implement Each Invoker
**File:** `internal/cli/invoker.go`

```go
type CodexInvoker struct {
    path string
}

func (c *CodexInvoker) Review(ctx context.Context, diff string) (string, error) {
    // Get current commit hash
    commit := git.CurrentCommit() // new helper

    cmd := exec.CommandContext(ctx, c.path, "review", "--commit", commit)
    output, err := cmd.CombinedOutput()
    return string(output), err
}

type GeminiInvoker struct {
    path string
}

func (g *GeminiInvoker) Review(ctx context.Context, diff string) (string, error) {
    prompt := "Review these changes for bugs, security issues, and best practices"

    cmd := exec.CommandContext(ctx, g.path, "-p", prompt)
    cmd.Stdin = strings.NewReader(diff)

    output, err := cmd.CombinedOutput()
    return string(output), err
}

// TODO: ClaudeInvoker, CopilotInvoker (need to investigate exact patterns)
```

**Tests:**
- Mock CLI commands with fake scripts
- Test stdin/stdout handling
- Test error cases (CLI not found, timeout, auth failure)

### Task 2.3: Git Helper for Current Commit
**File:** `internal/git/diff.go` (add to existing file)

```go
// CurrentCommit returns the current HEAD commit hash (short form)
func CurrentCommit() string {
    cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
    out, err := cmd.Output()
    if err != nil {
        return "HEAD"
    }
    return strings.TrimSpace(string(out))
}

// CurrentBranch returns the current branch name
func CurrentBranch() string {
    cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
    out, err := cmd.Output()
    if err != nil {
        return "unknown"
    }
    return strings.TrimSpace(string(out))
}

// SanitizeBranchName replaces / with - for filesystem safety
func SanitizeBranchName(branch string) string {
    return strings.ReplaceAll(branch, "/", "-")
}
```

---

## Phase 3: Configuration Extension

**Goal:** Extend `.local-review.yml` to support multi-LLM settings.

### Task 3.1: Update Config Structs
**File:** `internal/config/config.go`

```go
type Config struct {
    Provider Provider `yaml:"provider"` // v0 compatibility
    Review   Review   `yaml:"review"`
    Org      Org      `yaml:"org"`

    // v0.1 additions
    LLMs    map[string]LLMConfig `yaml:"llms"`
    Merge   MergeConfig          `yaml:"merge"`
    Storage StorageConfig        `yaml:"storage"`
}

type LLMConfig struct {
    Enabled      bool   `yaml:"enabled"`
    Mode         string `yaml:"mode"`           // "cli" or "api"
    CLIPath      string `yaml:"cli_path"`
    Model        string `yaml:"model"`
    APIKeyEnv    string `yaml:"api_key_env"`
    TimeoutSec   int    `yaml:"timeout_seconds"`
}

type MergeConfig struct {
    PreferredLLM        string `yaml:"preferred_llm"`    // "auto" or LLM name
    Deduplicate         bool   `yaml:"deduplicate"`
    ConsensusThreshold  int    `yaml:"consensus_threshold"`
}

type StorageConfig struct {
    BasePath string `yaml:"base_path"`
}
```

### Task 3.2: Defaults for v0.1
**File:** `internal/config/config.go`

```go
func Defaults() Config {
    return Config{
        // v0 defaults (unchanged)
        Provider: Provider{...},
        Review:   Review{...},

        // v0.1 defaults
        LLMs: map[string]LLMConfig{
            "claude": {
                Enabled:    true,
                Mode:       "cli",
                CLIPath:    "claude",
                Model:      "claude-3.5-sonnet",
                APIKeyEnv:  "ANTHROPIC_API_KEY",
                TimeoutSec: 120,
            },
            "gemini": {
                Enabled:    true,
                Mode:       "cli",
                CLIPath:    "gemini",
                Model:      "gemini-1.5-pro",
                TimeoutSec: 120,
            },
            "codex": {
                Enabled:    true,
                Mode:       "cli",
                CLIPath:    "codex",
                Model:      "gpt-4",
                APIKeyEnv:  "OPENAI_API_KEY",
                TimeoutSec: 120,
            },
            "copilot": {
                Enabled:    true,
                Mode:       "cli",
                CLIPath:    "gh",
                TimeoutSec: 120,
            },
        },
        Merge: MergeConfig{
            PreferredLLM:       "auto",
            Deduplicate:        true,
            ConsensusThreshold: 3,
        },
        Storage: StorageConfig{
            BasePath: ".local-review/reviews",
        },
    }
}
```

### Task 3.3: Example Config
**File:** `examples/.local-review-multi.yml`

```yaml
# Example multi-LLM configuration for local-review v0.1+

llms:
  claude:
    enabled: true
    mode: cli
    model: claude-3.5-sonnet
    api_key_env: ANTHROPIC_API_KEY  # API fallback

  gemini:
    enabled: true
    mode: cli
    model: gemini-1.5-pro

  codex:
    enabled: true
    mode: cli
    model: gpt-4
    api_key_env: OPENAI_API_KEY

  copilot:
    enabled: false  # disable if not using GitHub Copilot
    mode: cli

merge:
  preferred_llm: auto  # or: claude, gemini, codex, copilot
  deduplicate: true
  consensus_threshold: 3

storage:
  base_path: .local-review/reviews
```

---

## Phase 4: Multi-LLM Orchestration

**Goal:** Run reviews in parallel, save outputs, track metadata.

### Task 4.1: Storage Module
**File:** `internal/multi/storage.go`

```go
package multi

import (
    "fmt"
    "os"
    "path/filepath"
)

// ReviewStorage handles saving review outputs
type ReviewStorage struct {
    basePath string
}

func NewStorage(basePath string) *ReviewStorage {
    return &ReviewStorage{basePath: basePath}
}

// SaveReview writes an LLM's output to disk
// Returns: path to saved file
func (s *ReviewStorage) SaveReview(branch, commit, llmName, llmVersion, content string) (string, error) {
    dir := filepath.Join(s.basePath, sanitizeBranch(branch))
    if err := os.MkdirAll(dir, 0755); err != nil {
        return "", err
    }

    filename := fmt.Sprintf("%s_%s_%s.md", commit, llmName, llmVersion)
    path := filepath.Join(dir, filename)

    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        return "", err
    }

    return path, nil
}

func sanitizeBranch(branch string) string {
    return strings.ReplaceAll(branch, "/", "-")
}
```

### Task 4.2: Metadata Module
**File:** `internal/multi/metadata.go`

```go
package multi

import (
    "encoding/json"
    "time"
)

type Metadata struct {
    Commit    string          `json:"commit"`
    Branch    string          `json:"branch"`
    Timestamp time.Time       `json:"timestamp"`
    Reviews   []ReviewMeta    `json:"reviews"`
    Merge     MergeMeta       `json:"merge"`
}

type ReviewMeta struct {
    LLM           string `json:"llm"`
    Version       string `json:"version"`
    Mode          string `json:"mode"`
    Status        string `json:"status"`
    DurationMs    int64  `json:"duration_ms"`
    FindingsCount int    `json:"findings_count,omitempty"`
    OutputFile    string `json:"output_file,omitempty"`
    Error         string `json:"error,omitempty"`
}

type MergeMeta struct {
    LLM                 string `json:"llm"`
    Status              string `json:"status"`
    FinalFindingsCount  int    `json:"final_findings_count"`
    DeduplicationRemoved int   `json:"deduplication_removed,omitempty"`
}

// Save writes metadata to JSON file
func (m *Metadata) Save(path string) error {
    data, err := json.MarshalIndent(m, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(path, data, 0644)
}
```

### Task 4.3: Orchestrator
**File:** `internal/multi/orchestrator.go`

```go
package multi

import (
    "context"
    "sync"
    "time"
    "github.com/mshykov/local-review/internal/cli"
    "github.com/mshykov/local-review/internal/git"
)

type Orchestrator struct {
    llms    []cli.LLM
    storage *ReviewStorage
}

func NewOrchestrator(llms []cli.LLM, storage *ReviewStorage) *Orchestrator {
    return &Orchestrator{llms: llms, storage: storage}
}

type ReviewResult struct {
    LLM      string
    Version  string
    Output   string
    Error    error
    Duration time.Duration
    FilePath string
}

// RunParallel executes reviews concurrently
func (o *Orchestrator) RunParallel(ctx context.Context, diff string) ([]ReviewResult, error) {
    var wg sync.WaitGroup
    results := make([]ReviewResult, len(o.llms))

    commit := git.CurrentCommit()
    branch := git.CurrentBranch()

    for i, llm := range o.llms {
        wg.Add(1)
        go func(idx int, l cli.LLM) {
            defer wg.Done()

            start := time.Now()
            invoker := cli.NewInvoker(l)
            output, err := invoker.Review(ctx, diff)
            duration := time.Since(start)

            result := ReviewResult{
                LLM:      l.Name,
                Version:  l.Version,
                Output:   output,
                Error:    err,
                Duration: duration,
            }

            // Save to disk (even if error, for debugging)
            if output != "" {
                path, _ := o.storage.SaveReview(branch, commit, l.Name, l.Version, output)
                result.FilePath = path
            }

            results[idx] = result
        }(i, llm)
    }

    wg.Wait()
    return results, nil
}
```

### Task 4.4: Multi Command
**File:** `cmd/local-review/multi.go`

```go
package main

import (
    "context"
    "github.com/spf13/cobra"
    "github.com/mshykov/local-review/internal/cli"
    "github.com/mshykov/local-review/internal/git"
    "github.com/mshykov/local-review/internal/multi"
)

func multiCmd(sf *sharedFlags) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "multi",
        Short: "Run multi-LLM parallel review",
    }

    cmd.AddCommand(multiStagedCmd(sf))
    cmd.AddCommand(multiCommitCmd(sf))
    cmd.AddCommand(multiBranchCmd(sf))

    return cmd
}

func multiStagedCmd(sf *sharedFlags) *cobra.Command {
    return &cobra.Command{
        Use:   "staged",
        Short: "Multi-LLM review of staged changes",
        RunE: func(cmd *cobra.Command, args []string) error {
            return runMultiReview(cmd.Context(), sf, git.ModeStaged, "")
        },
    }
}

func runMultiReview(ctx context.Context, sf *sharedFlags, mode git.Mode, ref string) error {
    // 1. Load config
    cfg, err := loadConfig()
    if err != nil {
        return err
    }

    // 2. Detect installed LLMs
    detected, _ := cli.DetectAll()
    enabled := filterEnabled(detected, cfg.LLMs)

    if len(enabled) == 0 {
        return fmt.Errorf("no LLMs available (run 'local-review doctor')")
    }

    // 3. Extract diff
    diffs, err := git.Extract(mode, ref)
    if err != nil {
        return err
    }
    diffStr := git.FormatDiff(diffs) // new helper

    // 4. Run parallel reviews
    storage := multi.NewStorage(cfg.Storage.BasePath)
    orch := multi.NewOrchestrator(enabled, storage)

    fmt.Printf("Running review with %d LLMs...\n", len(enabled))
    results, err := orch.RunParallel(ctx, diffStr)
    if err != nil {
        return err
    }

    // 5. Print status
    for i, r := range results {
        if r.Error != nil {
            fmt.Printf("[%d/%d] %s ✗ (%s)\n", i+1, len(results), r.LLM, r.Error)
        } else {
            fmt.Printf("[%d/%d] %s ✓ (%.1fs)\n", i+1, len(results), r.LLM, r.Duration.Seconds())
        }
    }

    // TODO: Phase 5 - merge reviews

    return nil
}

func filterEnabled(detected []cli.LLM, configs map[string]config.LLMConfig) []cli.LLM {
    var enabled []cli.LLM
    for _, llm := range detected {
        if llm.Available && configs[llm.Name].Enabled {
            enabled = append(enabled, llm)
        }
    }
    return enabled
}
```

**Hook into main.go:**
```go
root.AddCommand(multiCmd(&sf))
```

---

## Phase 5: Review Merging

**Goal:** Use an LLM to consolidate findings from all reviews.

### Task 5.1: Merge Prompt Template
**File:** `internal/multi/merge_prompt.md`

```markdown
You are merging code review findings from multiple AI reviewers.

You have received {{ .ReviewCount }} separate review reports from: {{ .LLMNames }}.

Your task:
1. **Deduplicate**: If {{ .ConsensusThreshold }}+ reviewers report the same issue (even with different wording), consolidate into 1 entry with a note "Confirmed by: LLM1, LLM2, LLM3"
2. **Consolidate similar findings**: Merge issues that are semantically the same (e.g., "missing error handling" from all LLMs)
3. **Prioritize**: If duplicates have different severity levels, keep the highest severity
4. **Format as markdown**: Use sections by severity level

Output format:
# Code Review (Merged from {{ .ReviewCount }} LLMs)

## Critical Issues
- [file:line] Issue title (Confirmed by: LLM1, LLM2)
  Explanation and suggested fix.

## Major Issues
...

## Summary
- Total unique findings: X
- LLMs that contributed: {{ .LLMNames }}
- High-confidence issues ({{ .ConsensusThreshold }}+ LLMs): Y

---

Input reviews:

{{ range .Reviews }}
## Review from {{ .LLM }}
{{ .Content }}

{{ end }}
```

### Task 5.2: Merger Implementation
**File:** `internal/multi/merger.go`

```go
package multi

import (
    "context"
    "fmt"
    "os"
    "text/template"
)

type Merger struct {
    llm      cli.Invoker  // which LLM to use for merging
    prompt   *template.Template
}

func NewMerger(llm cli.Invoker) (*Merger, error) {
    tmpl, err := template.ParseFiles("internal/multi/merge_prompt.md")
    if err != nil {
        return nil, err
    }
    return &Merger{llm: llm, prompt: tmpl}, nil
}

type MergeInput struct {
    ReviewCount         int
    LLMNames            string
    ConsensusThreshold  int
    Reviews             []ReviewContent
}

type ReviewContent struct {
    LLM     string
    Content string
}

func (m *Merger) Merge(ctx context.Context, input MergeInput) (string, error) {
    var buf bytes.Buffer
    if err := m.prompt.Execute(&buf, input); err != nil {
        return "", err
    }

    prompt := buf.String()
    return m.llm.Review(ctx, prompt)
}
```

### Task 5.3: Integrate Merge into Multi Command
**File:** `cmd/local-review/multi.go`

```go
func runMultiReview(ctx context.Context, sf *sharedFlags, mode git.Mode, ref string) error {
    // ... existing code ...

    // 6. Select merge LLM
    mergeLLM := selectMergeLLM(results, cfg.Merge.PreferredLLM, sf.mergeWith)
    if mergeLLM == nil {
        return fmt.Errorf("no LLM available for merging")
    }

    // 7. Merge reviews
    fmt.Printf("Merging reviews with %s...\n", mergeLLM.Name)

    merger, err := multi.NewMerger(cli.NewInvoker(*mergeLLM))
    if err != nil {
        return err
    }

    mergeInput := buildMergeInput(results, cfg.Merge.ConsensusThreshold)
    merged, err := merger.Merge(ctx, mergeInput)
    if err != nil {
        return err
    }

    // 8. Save merged review
    commit := git.CurrentCommit()
    branch := git.CurrentBranch()
    mergedPath := filepath.Join(
        cfg.Storage.BasePath,
        sanitizeBranch(branch),
        fmt.Sprintf("%s_merged.md", commit),
    )

    os.WriteFile(mergedPath, []byte(merged), 0644)

    fmt.Printf("✓ Review complete: %s\n", mergedPath)
    return nil
}

func selectMergeLLM(results []multi.ReviewResult, preferred, flagOverride string) *cli.LLM {
    // Priority: flag > config > auto
    if flagOverride != "" {
        return findSuccessfulLLM(results, flagOverride)
    }
    if preferred != "" && preferred != "auto" {
        return findSuccessfulLLM(results, preferred)
    }

    // Auto: Claude > GPT > Gemini > Copilot
    order := []string{"claude", "codex", "gemini", "copilot"}
    for _, name := range order {
        if llm := findSuccessfulLLM(results, name); llm != nil {
            return llm
        }
    }
    return nil
}
```

### Task 5.4: Add --merge-with Flag
**File:** `cmd/local-review/main.go`

```go
type sharedFlags struct {
    model       string
    baseURL     string
    minSeverity string
    maxFindings int
    jsonOut     bool
    mergeWith   string  // NEW
}

func multiCmd(sf *sharedFlags) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "multi",
        Short: "Run multi-LLM parallel review",
    }

    cmd.PersistentFlags().StringVar(&sf.mergeWith, "merge-with", "", "which LLM to use for merging (claude, gemini, codex, copilot)")

    // ... rest ...
}
```

---

## Phase 6: Testing & Documentation

### Task 6.1: Unit Tests
- `internal/cli/detector_test.go` — CLI detection with mock PATH
- `internal/cli/invoker_test.go` — invokers with fake CLIs
- `internal/multi/storage_test.go` — file saving, path sanitization
- `internal/multi/orchestrator_test.go` — parallel execution
- `internal/multi/merger_test.go` — merge logic

### Task 6.2: Integration Test
**File:** `internal/multi/integration_test.go`

Mock all 4 CLIs with shell scripts:
```sh
# test/mocks/claude
#!/bin/bash
echo "Mock Claude review: Found 3 issues"

# test/mocks/gemini
#!/bin/bash
cat - | grep "+" && echo "Mock Gemini review: Found 2 issues"
```

Test full flow: detect → review → merge

### Task 6.3: Update README
Add section:
```markdown
## v0.1: Multi-LLM Reviews (NEW)

Review your code with 4 AI models simultaneously:

```sh
local-review multi staged
```

This runs parallel reviews with Claude, Gemini, Codex, and GitHub Copilot, then merges findings into one consolidated report.

Setup:
```sh
# Install LLM CLIs
npm install -g @google/gemini-cli@0.40.0
npm install -g @openai/codex@0.128.0
npm install -g @anthropic/claude-cli
brew install gh && gh auth login

# Check status
local-review doctor
```

See [docs/multi-llm-architecture.md](docs/multi-llm-architecture.md) for details.
```

### Task 6.4: Update Examples
Create `examples/.local-review-multi.yml` (done in Phase 3.3)

---

## Release Checklist

- [ ] All tests pass (`go test ./...`)
- [ ] Manual test with real CLIs installed
- [ ] Manual test with some CLIs missing (graceful degradation)
- [ ] Manual test with `--merge-with` flag
- [ ] Update CHANGELOG.md
- [ ] Tag `v0.1.0`
- [ ] GitHub Release with binaries

---

## Success Criteria

**v0.1 is complete when:**
1. `local-review doctor` detects all 4 LLM CLIs
2. `local-review multi staged` runs reviews in parallel
3. Reviews are saved to `.local-review/reviews/<branch>/<commit>_*.md`
4. `<commit>_merged.md` consolidates findings with deduplication
5. `<commit>_metadata.json` tracks run details
6. `--merge-with <llm>` flag works
7. Error handling: continues if 1+ LLMs fail
8. Tests cover core functionality
9. Documentation updated (README, CLAUDE.md, architecture doc)

---

## Future Enhancements (v0.2+)

- Auto-install CLIs (detect npm, run `npm install -g ...`)
- Retry logic for transient failures
- GitHub integration (post `merged.md` as PR comment)
- Web UI for browsing review history
- Consensus scoring (weight by LLM agreement)
- Custom merge strategies (user-defined dedup rules)

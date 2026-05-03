# Multi-LLM Architecture (v0.1)

This document details the design decisions and architecture for local-review v0.1's multi-LLM parallel review feature.

## Problem Statement

v0 supports single-LLM reviews via API endpoints. v0.1 adds:
- **Parallel reviews** from multiple LLMs (Claude, GPT/Codex, Gemini, Copilot)
- **CLI-first approach** to use free tiers without API keys
- **Intelligent merging** to consolidate findings and remove duplicates
- **Local storage** to preserve review history per commit

## Design Decisions

### 1. Hybrid CLI + API Mode

**Decision:** Support both CLI and API modes with per-provider fallback.

**Rationale:**
- CLI tools allow free usage without API keys (user's existing accounts)
- API mode preserves v0's workflow for users with keys
- Fallback ensures resilience (CLI auth fails → try API)

**Implementation:**
```yaml
llms:
  claude:
    mode: cli              # prefer CLI
    api_key_env: ANTHROPIC_API_KEY  # fallback to API
```

### 2. Supported LLM CLIs

| LLM | CLI Tool | Install Command | Version |
|-----|----------|----------------|---------|
| Claude | `claude` | `npm install -g @anthropic/claude-cli` | Latest |
| Gemini | `gemini` | `npm install -g @google/gemini-cli@0.40.0` | 0.40.0 |
| Codex | `codex` | `npm install -g @openai/codex@0.128.0` | 0.128.0 |
| Copilot | `gh copilot` | `brew install gh` + `gh auth login` | Via gh CLI |

**Why these CLIs?**
- Official or widely-used community tools
- All support stdin/prompt-based invocation
- Authentication via web flows (no manual token setup)

### 3. CLI Invocation Patterns

Each CLI has different invocation syntax. We normalize in `internal/cli/invoker.go`:

```go
// Codex
codex review --commit <sha>

// Gemini
git diff | gemini -p "Review these changes for bugs and security issues"

// Claude
// TBD: investigate exact pattern (likely `claude < diff.txt` or `claude --prompt`)

// Copilot
gh copilot suggest -t shell "review this diff: <diff>"
```

**Challenge:** Copilot CLI is designed for suggestions, not structured code review. May need custom prompt engineering.

### 4. Storage Structure

**Decision:** Organize by branch and commit hash, sanitize branch names.

```
.local-review/
  reviews/
    feature-auth-fix/              # branch name with / → -
      abc123_claude_3.5.md
      abc123_gemini_0.40.md
      abc123_copilot_1.0.md
      abc123_codex_0.128.md
      abc123_merged.md
      abc123_metadata.json
```

**Rationale:**
- Branch-based folders allow tracking multiple commits per feature
- Commit hash prevents collisions (same branch, different commits)
- Sanitized names avoid filesystem issues (slashes, special chars)
- Metadata.json enables audit trail and debugging

### 5. Parallel Execution

**Decision:** Run all enabled LLMs in parallel via goroutines.

**Implementation sketch:**
```go
type ReviewResult struct {
    LLM      string
    Output   string
    Error    error
    Duration time.Duration
}

results := make(chan ReviewResult, len(enabledLLMs))

for _, llm := range enabledLLMs {
    go func(l LLM) {
        start := time.Now()
        output, err := l.Review(diff)
        results <- ReviewResult{l.Name, output, err, time.Since(start)}
    }(llm)
}

// Collect all results (continue on individual failures)
for i := 0; i < len(enabledLLMs); i++ {
    result := <-results
    // save to file, log metadata
}
```

**Error Handling:**
- Continue if one LLM fails (best-effort)
- Log failure details to metadata.json
- Include failure note in merged.md

### 6. Review Merging

**Decision:** Use an LLM to intelligently merge reviews (not simple concatenation).

**Merge LLM Selection (in priority order):**
1. `--merge-with <llm>` flag
2. `merge.preferred_llm` in config
3. Automatic best-available: Claude > GPT > Gemini > Copilot

**Merge Prompt:**
```
You are merging code review findings from multiple AI reviewers.

Input: 4 separate review reports (Claude, GPT, Gemini, Copilot).

Task:
1. Deduplicate: If 3+ LLMs report the same issue, keep 1 entry with note "Confirmed by: Claude, GPT, Gemini"
2. Consolidate: Merge similar findings (e.g., "missing error handling" from all LLMs)
3. Prioritize: Keep highest severity version if duplicates differ
4. Format: Return markdown with sections by severity (critical, major, warning, etc.)

Output format:
# Code Review (Merged)

## Critical Issues
- [file:line] Issue title (Confirmed by: Claude, GPT)
  Details...

## Major Issues
...

## Summary
- Total unique findings: X
- LLMs that contributed: Claude, GPT, Gemini, Copilot
- Consensus issues (3+ LLMs): Y
```

**Why LLM-powered merge?**
- Simple concatenation creates noise (duplicate findings)
- Manual dedup rules are brittle (same issue, different wording)
- LLMs excel at semantic similarity detection

### 7. Metadata Tracking

**Decision:** Store run details in `<commit>_metadata.json` for debugging and audit.

**Schema:**
```json
{
  "commit": "abc123",
  "branch": "feature-auth-fix",
  "timestamp": "2026-05-02T10:30:00Z",
  "reviews": [
    {
      "llm": "claude",
      "version": "3.5",
      "mode": "cli",
      "status": "success",
      "duration_ms": 4500,
      "findings_count": 12,
      "output_file": "abc123_claude_3.5.md"
    },
    {
      "llm": "gemini",
      "version": "0.40",
      "mode": "cli",
      "status": "failed",
      "error": "authentication required",
      "duration_ms": 200
    }
  ],
  "merge": {
    "llm": "claude",
    "status": "success",
    "final_findings_count": 8,
    "deduplication_removed": 4
  }
}
```

**Benefits:**
- Debug why a specific LLM failed
- Track performance (which LLM is fastest?)
- Analyze consensus (how often do LLMs agree?)

### 8. Configuration Schema

**Decision:** Extend existing `.local-review.yml` with `llms`, `merge`, and `storage` sections.

**Full schema:**
```yaml
# v0.1 multi-LLM configuration
llms:
  claude:
    enabled: true
    mode: cli                    # 'cli' or 'api'
    cli_path: claude             # auto-detect if empty
    model: claude-3.5-sonnet     # used in API mode
    api_key_env: ANTHROPIC_API_KEY
    timeout_seconds: 120

  gemini:
    enabled: true
    mode: cli
    cli_path: gemini
    model: gemini-1.5-pro
    timeout_seconds: 120

  copilot:
    enabled: true
    mode: cli
    cli_path: gh
    timeout_seconds: 120

  codex:
    enabled: true
    mode: cli
    cli_path: codex
    model: gpt-4
    api_key_env: OPENAI_API_KEY
    timeout_seconds: 120

merge:
  preferred_llm: auto            # 'auto' or specific LLM name
  deduplicate: true
  consensus_threshold: 3         # N LLMs agreeing = "Confirmed by N"

storage:
  base_path: .local-review/reviews

# v0 compatibility (still used by `local-review staged`)
provider:
  base_url: https://api.openai.com/v1
  model: gpt-4o-mini
  api_key_env: LOCAL_REVIEW_API_KEY

review:
  min_severity: warning
  max_findings: 20
```

**Backward Compatibility:**
- v0 commands (`local-review staged`) use `provider` section
- v0.1 commands (`local-review multi staged`) use `llms` section
- Users can migrate gradually

### 9. Command Structure

**New commands:**
```sh
# Multi-LLM review
local-review multi staged
local-review multi commit <ref>
local-review multi branch <base>
local-review multi staged --merge-with claude

# Utilities
local-review doctor              # check installations, show status
local-review merge <commit>      # re-run merge on existing reviews

# v0 compatibility (unchanged)
local-review staged
local-review commit
local-review branch
```

**Doctor command output:**
```
Checking LLM installations...

✓ Claude CLI    v2.1.0    (authenticated)
✓ Gemini CLI    v0.40.0   (authenticated)
✗ Copilot CLI   not found (install: brew install gh && gh auth login)
✓ Codex CLI     v0.128.0  (authenticated)

3/4 LLMs ready for multi-review.
Missing: copilot

API fallbacks configured:
✓ Claude    (ANTHROPIC_API_KEY set)
✗ Gemini    (no API key)
✓ Codex     (OPENAI_API_KEY set)
```

### 10. User Workflow

**Typical flow:**
```sh
# First run: setup
local-review doctor              # check what's installed
# ... follow prompts to install missing CLIs ...

# Make changes, commit
git commit -m "Add auth feature"

# Run multi-review
local-review multi commit

# Output:
# [1/3] Reviewing with Claude... ✓ (4.5s)
# [2/3] Reviewing with Gemini... ✓ (3.2s)
# [3/3] Reviewing with Codex... ✓ (5.1s)
# Merging 3 reviews with Claude...
# ✓ Review complete: .local-review/reviews/feature-auth/abc123_merged.md

# Read merged review
cat .local-review/reviews/feature-auth/abc123_merged.md

# Fix issues, commit again
git commit -m "Fix auth issues from review"

# Re-review
local-review multi commit
```

## Implementation Phases

### Phase 1: CLI Detection & Invocation
- [ ] `internal/cli/detector.go` — check which CLIs installed
- [ ] `internal/cli/version.go` — extract version numbers
- [ ] `internal/cli/invoker.go` — run CLI commands (codex, gemini, etc.)
- [ ] `cmd/local-review/doctor.go` — doctor command

### Phase 2: Parallel Orchestration
- [ ] `internal/multi/orchestrator.go` — parallel execution
- [ ] `internal/multi/storage.go` — save reviews to files
- [ ] `internal/multi/metadata.go` — track run details
- [ ] `cmd/local-review/multi.go` — multi command

### Phase 3: Review Merging
- [ ] `internal/multi/merger.go` — LLM-powered merge
- [ ] Merge prompt engineering (dedup, consolidate)
- [ ] `cmd/local-review/merge.go` — re-merge command

### Phase 4: Configuration
- [ ] Extend `internal/config/config.go` with LLMs, Merge, Storage
- [ ] Config validation (ensure at least 1 LLM enabled)
- [ ] Examples: `examples/.local-review-multi.yml`

### Phase 5: Testing & Polish
- [ ] Unit tests for CLI detection, invocation
- [ ] Integration test with mock CLIs
- [ ] Error handling polish (better messages)
- [ ] Update README, docs

## Open Questions

1. **Claude CLI invocation pattern** — need to investigate exact syntax
2. **Copilot structured output** — `gh copilot` is conversational; can it return JSON?
3. **Rate limits** — should we add delays between parallel calls?
4. **Installer automation** — v0.1 guides users; should v0.2 auto-install CLIs?

## Non-Goals (v0.1)

- Auto-installation of CLIs (guide only)
- Retry logic for transient failures
- Advanced consensus weighting (3+ LLMs agree = higher severity)
- GitHub integration (post merged.md as PR comment)
- Web UI for browsing review history

These can be added in future versions based on user feedback.

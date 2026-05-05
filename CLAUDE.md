# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

local-review is a local, BYOK (Bring Your Own Key) AI code reviewer. It's a single Go binary that:
- Runs against git diffs (staged changes, commits, or branches)
- **v0.1+**: Supports multi-LLM parallel reviews (Claude, Gemini, Codex)
- **v0**: Single-LLM via OpenAI-compatible API endpoints
- Saves reviews to local storage and merges findings intelligently
- Ships with language-aware prompt packs for TypeScript, Go, Python, Rust, and more

Key constraints:
- **No vendor SDK dependencies** — keeps the binary small and portable
- **No telemetry** — privacy first
- **Git CLI integration only** — no go-git to avoid binary bloat
- **Hybrid CLI + API mode** — prefer free CLI tools, fallback to API if configured

## v0.1 Multi-LLM Architecture

### Design Decisions (Clarified 2026-05-02)

**Dual Mode: CLI-first with API Fallback**
- Primary: Use free LLM CLIs (claude, gemini, codex)
- Fallback: Use API endpoints if CLI unavailable/fails
- Per-provider configuration allows mixing (e.g., Claude via CLI, GPT via API)

**Supported LLM CLIs**
1. **Claude CLI** — `npm install -g @anthropic-ai/claude-code` (auth: `claude login`)
2. **Gemini CLI** — `npm install -g @google/gemini-cli` (requires Node.js 20+)
3. **OpenAI Codex CLI** — `npm install -g @openai/codex` (auth via `codex login` with ChatGPT Plus, or `OPENAI_API_KEY` env var pay-per-token — usually cheaper for occasional use)

**CLI Invocation Patterns**
- Codex: `codex review --commit <sha>`
- Gemini: stdin diff piped to `gemini -p "<prompt>"`
- Claude: stdin diff piped to `claude` with the review prompt

**Storage Structure**
Reviews saved to `.local-review/reviews/<sanitized-branch>/<commit>_<llm>_<version>.md`:
```
.local-review/
  reviews/
    feature-auth-fix/              # branch name sanitized (/ → -)
      abc123_claude_3.5.md
      abc123_gemini_0.40.md
      abc123_codex_0.128.md
      abc123_merged.md             # LLM-powered merge
      abc123_metadata.json         # run details: timestamps, exit codes
```

**Merge Strategy**
- Use LLM to deduplicate and consolidate findings from all reviews
- Merge LLM selection (in priority order):
  1. User's `--merge-with <llm>` flag
  2. User's config `merge.preferred_llm: claude`
  3. Automatic best-available: Claude > Codex > Gemini
- Default: automatic mode

**Error Handling**
- Parallel execution: continue if one LLM fails
- Log failures to metadata.json
- Include failure notes in merged.md
- Skip not-installed LLMs silently (only log installed ones)

**Configuration Schema (v0.1)**
```yaml
# .local-review.yml
llms:
  claude:
    enabled: true
    mode: cli                    # 'cli' or 'api'
    cli_path: claude             # auto-detect if empty
    model: claude-3.5-sonnet
    api_key_env: ANTHROPIC_API_KEY

  gemini:
    enabled: true
    mode: cli
    cli_path: gemini
    model: gemini-1.5-pro

  codex:
    enabled: false               # paid; opt in if you have ChatGPT Plus
    mode: cli
    cli_path: codex
    model: gpt-4

merge:
  preferred_llm: auto            # 'auto' or specific LLM name
  deduplicate: true

storage:
  base_path: .local-review/reviews
```

**Command Structure (v0.5+)**
```sh
# Review — multi-LLM by default (every authenticated CLI runs in parallel and
# findings are merged). Falls back to single-LLM via configured provider when
# no CLI is active.
local-review review              # canonical: current branch vs main
local-review staged              # what would be committed next (pre-commit)
local-review commit [<rev>]      # one commit (default: HEAD)
local-review branch [<base>]     # alias of `review`
local-review review --only claude,gemini      # restrict the agent set
local-review review --claude-model claude-opus-4-7  # override one agent's model
local-review review --merge-with claude       # pick which agent merges

# Utilities
local-review init                # interactive setup wizard (writes .local-review.yml)
local-review doctor              # check LLM installations, auth status
local-review config              # print resolved config (API keys masked)
local-review version             # print version
```

There is no standalone "re-merge" command — the merge step runs as part of `local-review review`. To re-run the merge against an existing commit, re-run `local-review commit <ref>`.

## Development Commands

### Build and Test
```sh
# Run all tests
go test ./...

# Run tests with race detection (CI standard)
go test -race ./...

# Build the binary
go build -o local-review ./cmd/local-review

# Test the canonical review path (multi-LLM by default if any CLI is active)
./local-review review

# Test staged-only review (pre-commit shape)
./local-review staged

# Test doctor command (check LLM installations + auth)
./local-review doctor

# Test against a specific commit
./local-review commit HEAD
```

### v0.1 Development Prerequisites
```sh
# Install Node.js (required for LLM CLIs)
brew install node

# Install LLM CLIs for testing
npm install -g @google/gemini-cli
npm install -g @openai/codex
npm install -g @anthropic-ai/claude-code
```

### Configuration Testing
```sh
# View resolved config
./local-review config
```

### Required Environment
- Go 1.23+
- Git CLI available in PATH
- **v0**: API key set: `export LOCAL_REVIEW_API_KEY=sk-...` (for testing with real providers)
- **v0.1**: Node.js 20+ and npm (for LLM CLI installations)

## v0.1 Architecture

### New Packages for Multi-LLM Support

**internal/cli/** — LLM CLI wrapper and detection
- `detector.go` — Check which LLM CLIs are installed (claude, gemini, codex)
- `installer.go` — Guide users to install missing CLIs via npm/brew
- `invoker.go` — Execute CLI commands with proper patterns per LLM
- `version.go` — Extract CLI version numbers for metadata

**internal/multi/** — Multi-LLM orchestration
- `orchestrator.go` — Parallel execution coordinator
- `merger.go` — LLM-powered review consolidation
- `storage.go` — Save reviews to `.local-review/reviews/<branch>/<commit>_*.md`
- `metadata.go` — Track run details (timestamps, exit codes, versions)

**internal/config/** — Extended configuration
- Add `LLMs` map with per-provider settings (mode, cli_path, model)
- Add `Merge` config (preferred_llm, deduplicate)
- Add `Storage` config (base_path)

**cmd/local-review/** — New commands
- `runner.go` — Unified review dispatcher (multi-LLM with single-LLM fallback)
- `doctor.go` — Check LLM installations and auth status

### Review Flow (v0.5+)

1. **Command: `local-review review`** (or `staged|commit|branch`)
2. **Config Load** — Resolve cascade (~/.local-review.yml → ./.local-review.yml → flags)
3. **Active LLM Detection** — `pickAgents()` reuses doctor's `classify()` to find every
   LLM CLI that is installed AND authenticated. Config `enabled: false` is honored
   as an opt-out unless `--only` overrides.
4. **Multi-LLM path** (when ≥1 active):
   - Print agent roster with model + CLI version
   - Parallel `RunParallel()` over goroutines
   - Save each per-LLM output to `.local-review/reviews/<branch>/<commit>_<llm>_<version>.md`
   - Log metadata to `<commit>_metadata.json`
   - Pick merge LLM (`--merge-with` → `merge.preferred_llm` → auto: claude > codex > gemini)
   - Merge findings via the merge LLM, save `<commit>_merged.md`, print to stdout
5. **Single-LLM fallback** (when no CLI active):
   - Use the configured `provider:` (any OpenAI-compatible endpoint)
   - One review pass, print findings, exit 2 on major/critical

### Storage Schema

```
.local-review/
  reviews/
    feature-auth-fix/
      abc123_claude_3.5.md          # Individual reviews
      abc123_gemini_0.40.md
      abc123_codex_0.128.md
      abc123_merged.md              # Consolidated review
      abc123_metadata.json          # Run metadata
```

**metadata.json structure:**
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
      "findings_count": 12
    },
    {
      "llm": "gemini",
      "version": "0.40",
      "mode": "cli",
      "status": "failed",
      "error": "authentication required"
    }
  ],
  "merge": {
    "llm": "claude",
    "status": "success",
    "final_findings_count": 8
  }
}
```

## v0 Architecture (Current Stable)

### Configuration Cascade (internal/config/)
Config is loaded in order of precedence (lowest to highest):
1. Built-in defaults (compiled in)
2. `~/.local-review.yml` (per-user)
3. `./.local-review.yml` (per-repo, found by walking up from cwd)
4. CLI flags (per-invocation)

Each layer shallow-merges over the previous one. Slices like `exclude` are replaced wholesale, not appended.

### Core Flow (cmd/local-review/main.go → internal/review/review.go)
1. **CLI entry** (cobra) parses command (staged/commit/branch) + flags
2. **Config loader** resolves YAML cascade, applies flag overrides
3. **Git wrapper** shells out to `git diff` with appropriate args for mode
4. **Language detection** analyzes file extensions, picks dominant language
5. **Prompt pack loader** embeds markdown files via go:embed, selects pack by language
6. **LLM client** (internal/llm/) sends system prompt + diff to chat-completions endpoint
7. **Parser** extracts JSON findings from LLM response (tolerates markdown fences)
8. **Filtering** applies min_severity + max_findings
9. **Output** formats as text or JSON

Exit codes:
- `0` — success, no blocking findings
- `2` — major/critical findings present (blocks pre-commit hooks)
- non-zero — tool failure (hooks ignore this and let commits through)

### LLM Client (internal/llm/client.go)
- **No SDKs** — raw HTTP POST to `/v1/chat/completions`
- All major providers speak this API dialect (even Anthropic via their OpenAI-compat endpoint)
- Uses `response_format: {type: "json_object"}` for structured output
- Low temperature (0.2) for consistency

### Git Integration (internal/git/diff.go)
- Shells out to `git` CLI (not go-git library) to preserve user's repo state
- Uses `-U10` for 10 lines of context (sweet spot for LLM reasoning vs token cost)
- Returns structured Diffs with Hunks (path + line numbers + content)
- Modes:
  - `staged`: `git diff --cached`
  - `commit`: `git show --format= <rev>`
  - `branch`: `git diff <base>...HEAD` (three-dot: from common ancestor)

### Prompt Packs (internal/prompts/)
Embedded markdown files (packs/*.md) define language-specific review rules:
- `default.md` — fallback for unknown languages
- `typescript.md`, `go.md`, `python.md`, `rust.md` — language-specific packs

Each pack is a system prompt with:
1. Priority-ordered review criteria (correctness > security > perf > style)
2. Severity tiering rules (nit/info/warning/major/critical)
3. Hard rules (never comment outside the diff, never invent code)
4. JSON output schema

Language detection (internal/lang/detect.go) maps file extensions → language IDs → pack files.

### Output Filtering (internal/review/review.go)
Findings are:
1. Parsed from LLM's JSON response (tolerates `\`\`\`json` fences)
2. Filtered by min_severity (drops findings below threshold)
3. Sorted by severity desc, then file/line asc
4. Capped at max_findings
5. Formatted as text (internal/output/text.go) or JSON

Glob filtering (include/exclude) uses a custom `**` glob matcher (review.go:matchGlob).

## Adding a New Language

1. Create `internal/prompts/packs/<langid>.md` with the pack content
2. Add extension(s) to `byExt` map in `internal/lang/detect.go`
3. Add a constant for the language ID (e.g., `const Rust = "rust"`)
4. Run `go test ./internal/prompts/... ./internal/lang/...`

## Style Guidelines

- **No vendor SDKs** — internal/llm/ stays SDK-free to keep binary size down
- **Standard Go** — gofmt -s + go vet
- **Comment intent, not mechanics** — explain *why*, never *what*
- **Tests required** — new logic needs a unit test
- **One-line doc comments** — exported functions/types only

## Pre-push Workflow (dogfooding)

**Before pushing any branch to GitHub, run a self-review with the project's own tool.**

```sh
# For a feature branch (reviews full diff vs main with every active LLM)
./local-review review main

# To restrict to one LLM (e.g., when multi-LLM is too slow):
./local-review review main --only claude
```

Address any `major` or `critical` findings before pushing. This is non-negotiable: we eat our own dog food. If `local-review` produces a noisy, low-value review on this codebase, that's a bug — file an issue or fix the prompt pack.

Skip the self-review only for: pure docs changes (`*.md`, `docs/`), website-only changes (`docs/index.html`), or trivial config tweaks where the tool would have nothing to say.

## CI and Releases

- `.github/workflows/ci.yml` runs `go vet` + `go test -race` + build on every push
- Tag `vX.Y.Z` on main to trigger release workflow (cross-compiles binaries for darwin/linux/windows × amd64/arm64)

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- `--json` mode now honors the blocking-finding exit gate (was incorrectly exiting 0 on major/critical findings).
- JSON output now emits `severity` as a string (e.g. `"major"`) instead of an integer, matching the documented contract.
- `parseUnifiedDiff` now strips trailing carriage returns when given CRLF-formatted input, fixing `\r`-suffixed paths in saved patches.
- `doctor` now reports `Available=false` for CLI providers when version detection fails (previously a stale PATH symlink showed as "ready").
- `doctor` install hint for Claude CLI now uses the correct npm package name (`@anthropic-ai/claude-code`).
- `examples/pre-commit` now references `local-review` and `LOCAL_REVIEW_SKIP` (previously a leftover from the project's earlier name).
- `gofmt` formatting issue in `internal/cli/invoker.go`.

### Changed
- Documentation: corrected stale Claude CLI npm package name across README, CONTRIBUTING, CLAUDE.md, and example configs.
- Documentation: updated SECURITY.md supported-versions table.
- Documentation: removed stale gemini/codex CLI version pins from CLAUDE.md and CONTRIBUTING.md (pins were already removed from the codebase's own install hints in commit 7c739f7).
- Documentation: corrected `CLAUDE.md` references to `merge.go` (the file is `multi.go`).

### CI
- Added `.github/dependabot.yml` for github-actions and gomod ecosystems.
- Pinned all GitHub Actions in CI/release workflows to commit SHAs (defense against floating-tag tampering).

## [0.4.0] - 2026-05-04

### Added
- **`local-review init`** — interactive setup wizard. Walks through 5 questions (provider, model, API-key env var, severity floor, max findings) and writes a working `.local-review.yml`. Removes the biggest first-run friction.
- Provider presets shipped with `init`: OpenAI, Anthropic (Claude via OpenAI-compat), Mistral, DeepSeek, Ollama, plus a custom path for any OpenAI-compatible endpoint.
- `--force` flag on `init` for non-interactive script use.
- `--location=global` flag to write `~/.local-review.yml` instead of the project-local file.

### Changed
- **Website Quick Start** now leads with `local-review init` (3 steps) instead of the old manual 4-step npm-install flow. Matches the README.
- **Code review guidelines** (`docs/CODE_REVIEW_GUIDELINES.md`) significantly expanded based on FAANG, OWASP 2025, and 2026 industry research: added AI-generated code review as a first-class priority, observability, concurrency depth, API/backward-compat, comment-prefix conventions, process-norms numbers (PR size, review SLAs), automation-layer principle, PR template, and 60-second review checklist.
- **`docs/RELEASE_PROCESS.md`** rewritten to match the consolidated single-workflow pipeline (`release.yml`) instead of describing the old auto-release.yml + update-homebrew-formula.yml + release.yml split.
- **README provider table**: Anthropic row now links to the OpenAI-compat docs and notes the exact-model-name gotcha. Removed stale `v0.1:` and `v0.x` markers from section headings.
- **`CONTRIBUTING.md`** file structure block updated to match the actual `cmd/local-review/` and `internal/` tree (was listing `merge.go` which doesn't exist).

### Fixed
- Init wizard's input validation re-prompts on bad answers (severity, max-findings, provider choice) instead of aborting the whole wizard. Caps at 5 retries to avoid scripted infinite loops.
- Init wizard's rendered YAML quotes scalars via `strconv.Quote` so values containing `#`, leading reserved characters, or other YAML-special content produce a valid file.
- "Free tier via claude.ai" wording on the website corrected to "Free tier via the Claude CLI" (claude.ai is the consumer chat product, not the CLI auth path).

### Removed
- Stale launch-era docs (`DISTRIBUTION.md`, `OPEN_SOURCE_CHECKLIST.md`, `docs/RELEASE_SETUP_CHECKLIST.md`) moved to `do-not-merge/` (gitignored). They were planning artifacts from the v0.1.0 launch and made the project look pre-launch when GitHub visitors browsed it.

## [0.3.0] - 2026-05-04

### Added
- **Self-hosted fonts** on the website: Inter for body, JetBrains Mono for code (both SIL OFL). No third-party CDN requests; matches the "no telemetry" positioning.
- **DeepSeek and Mistral provider examples** in `examples/.local-review.deepseek.yml` and `examples/.local-review.mistral.yml` — copy-paste-ready configs.
- **Tests for `internal/llm/Client`** — 91% statement coverage on the HTTP client (constructor, request shape, error paths, context cancellation, network errors).

### Note on versioning
v0.2.0 → v0.3.0 was a label-discipline mistake on the release PR (`minor` applied where `patch` would have been more accurate). The published release notes on GitHub are accurate; this changelog covers what shipped. See [`docs/RELEASE_PROCESS.md`](docs/RELEASE_PROCESS.md) "Label cheat sheet" for the rule going forward.

## [0.2.0] - 2026-05-04

### Added
- **Rust prompt pack** (`internal/prompts/packs/rust.md`): Rust-specific review rules covering ownership/borrowing, lifetimes, async/futures, unsafe code, error handling, concurrency, and Cargo conventions. Activates automatically on `.rs` files.

## [0.1.1] - 2026-05-04

### Added
- **Homebrew distribution**: `brew install mshykov/tap/local-review` (macOS/Linux)

### Fixed
- `install.sh` now prints a copy-paste-ready one-liner for adding `~/.local/bin` to PATH, detecting the user's shell (zsh/bash/fish) instead of an abstract `export` line that left users with `command not found` after install.
- `local-review doctor` no longer prints hardcoded version pins (`@google/gemini-cli@0.40.0`, `@openai/codex@0.128.0`) in install hints.
- `local-review doctor` no longer probes for "Copilot" in the API fallback section (Copilot CLI support was removed earlier; this was leftover dead state).
- "OpenAI Plus" → "ChatGPT Plus" everywhere user-facing (`OpenAI Plus` is not an actual product).

### Removed
- Outdated planning docs: `docs/implementation-plan-v0.1.md`, `docs/multi-llm-architecture.md`. Both described the original 4-LLM design (including Copilot) and were superseded by `CLAUDE.md`.
- All remaining Copilot references from user-facing docs and code comments (CLAUDE.md, multi.go, CONTRIBUTING.md, OPEN_SOURCE_CHECKLIST.md).

## [0.1.0] - 2026-05-03

### Added
- **Multi-LLM Support**: Run code reviews in parallel with multiple AI models
  - Claude CLI integration (free tier available)
  - Gemini CLI integration (free API key required)
  - Codex CLI integration (ChatGPT Plus required, disabled by default)
- **Multi-LLM Commands**:
  - `local-review multi staged` - Review staged changes with all enabled LLMs
  - `local-review multi commit [ref]` - Review a specific commit
  - `local-review multi branch [base]` - Review current branch against base
  - `local-review doctor` - Check LLM installations and authentication
- **Intelligent Review Merging**: Automatically consolidate findings from multiple LLMs
- **Version Command**: `local-review version` - Print version information
- **Config Command**: `local-review config` - Show resolved configuration (with API key masking)
- **Release Automation**:
  - Auto-release workflow (creates tags on main merges based on PR labels)
  - Homebrew formula auto-update workflow
- **Documentation**:
  - Release process documentation
  - Release setup checklist
  - Multi-LLM architecture documentation
  - Code review guidelines (Google, Microsoft, OWASP 2025 standards)

### Changed
- Enhanced prompt packs with industry best practices:
  - Default pack: 10 → 50+ security patterns (OWASP 2025 aligned)
  - Go pack: Reorganized into 8 categories with 15+ new patterns
  - TypeScript pack: Comprehensive React/Next.js patterns
  - Python pack: Framework-specific patterns (Django, FastAPI, Pandas)
- Improved merge logic with 5-step consolidation process

### Fixed
- Security: API keys now masked in config output
- Path traversal vulnerabilities in review storage
- Timeout handling in git operations

### Removed
- GitHub Copilot support (interactive-only CLI, incompatible with automation)

---

## Version Labeling Convention

When creating PRs, add ONE of these labels to control version bumps:
- `major` - Breaking changes (v2.0.0)
- `minor` - New features, backward compatible (v1.1.0)
- `patch` - Bug fixes, backward compatible (v1.0.1)

If no label is present, defaults to `patch`.

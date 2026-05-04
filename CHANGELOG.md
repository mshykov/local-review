# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

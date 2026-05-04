<p align="center">
  <img src="docs/logo.svg" alt="local-review logo" width="200"/>
</p>

<h1 align="center">local-review</h1>

<p align="center">
  <strong>Local, BYOK code review for any language.</strong><br>
  Runs against a git diff, hands it to whichever LLM you point it at, prints findings.<br>
  No SaaS, no telemetry, no signup.
</p>

<p align="center">
  <a href="https://github.com/mshykov/local-review/releases"><img src="https://img.shields.io/github/v/release/mshykov/local-review?style=flat-square" alt="Release"></a>
  <a href="https://github.com/mshykov/local-review/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg?style=flat-square" alt="License"></a>
  <a href="https://github.com/mshykov/local-review/stargazers"><img src="https://img.shields.io/github/stars/mshykov/local-review?style=flat-square" alt="Stars"></a>
</p>

<p align="center">
  <a href="#install">Install</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#v01-multi-llm-reviews">Multi-LLM</a> •
  <a href="https://mshykov.github.io/local-review">Website</a>
</p>

---

## Why

Reviewer tools today are mostly:

- **SaaS** — your code leaves the building. Hard sell for enterprise.
- **CI-only** — you find out about issues after pushing the branch.
- **Tied to a vendor** — single-provider lock-in (OpenAI-only or Anthropic-only).
- **Runtime-heavy** — Node/Python/Java install required just to run the reviewer.

local-review is the opposite: a single static binary, runs locally on a diff, talks to whatever endpoint you configure (OpenAI, Anthropic, Together, Groq, OpenRouter, **Ollama for fully-offline review**), and ships language-aware prompt packs that you can override per-repo.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

Or grab a binary from [Releases](https://github.com/mshykov/local-review/releases).

Or build from source:

```sh
go install github.com/mshykov/local-review/cmd/local-review@latest
```

## Quick start

```sh
# Set an API key for whichever provider you choose.
export LOCAL_REVIEW_API_KEY=sk-...

# Review staged changes (this is the pre-commit hook flavour).
local-review staged

# Review the latest commit.
local-review commit

# Review the whole branch against main.
local-review branch main
```

By default, local-review shows `warning`+ findings and exits non-zero when `major`/`critical` are present (so it can gate a pre-commit hook).

## v0.1: Multi-LLM Reviews

**Run parallel reviews with multiple AI models simultaneously:**

```sh
# Review with Claude + Gemini (both free!)
local-review multi staged

# Check which LLMs are installed
local-review doctor

# Use a specific LLM for merging findings
local-review multi staged --merge-with claude
```

### Supported LLMs

| LLM | Free Option | Installation | Status |
|-----|-------------|--------------|--------|
| **Claude** | ✅ Free tier via [claude.ai](https://claude.ai) | `npm install -g @anthropic/claude-cli` | Default enabled |
| **Gemini** | ✅ Free API key from [Google AI Studio](https://aistudio.google.com/apikey) | `npm install -g @google/gemini-cli` | Default enabled |
| **Codex** | ⚠️ Requires ChatGPT Plus ($20/mo) | `npm install -g @openai/codex` | Disabled by default |

**How it works:**
1. Detects installed LLM CLIs (claude, gemini, codex)
2. Runs reviews in parallel using free CLI tools (or API fallback)
3. Saves each review to `.local-review/reviews/<branch>/<commit>_<llm>.md`
4. Merges findings intelligently (deduplicates, consolidates, notes consensus)
5. Outputs final report: `<commit>_merged.md`

**Quick Setup:**
```sh
# Install free LLM CLIs
npm install -g @anthropic/claude-cli
npm install -g @google/gemini-cli

# Authenticate
claude login                    # Follow prompts
export GEMINI_API_KEY=your-key  # Get from https://aistudio.google.com/apikey

# Verify installations
local-review doctor

# Run multi-LLM review
local-review multi staged
```

**Configuration:**
```yaml
# .local-review.yml
llms:
  claude:
    enabled: true
    mode: cli              # use CLI (free), fallback to API if configured

  gemini:
    enabled: true
    mode: cli
    api_key_env: GEMINI_API_KEY  # required for Gemini

  codex:
    enabled: false         # paid only, enable if you have ChatGPT Plus
    mode: cli

merge:
  preferred_llm: auto      # or: claude, gemini, codex
  deduplicate: true
```

See [`examples/.local-review-multi.yml`](examples/.local-review-multi.yml) for full multi-LLM config example.

## Configure

local-review loads YAML from a cascade — built-in defaults → `~/.local-review.yml` → `./.local-review.yml` → CLI flags.

A minimal `~/.local-review.yml`:

```yaml
provider:
  base_url: https://api.openai.com/v1
  model: gpt-4o-mini
  api_key_env: LOCAL_REVIEW_API_KEY
review:
  min_severity: warning
  max_findings: 20
```

See [`examples/.local-review.yml`](examples/.local-review.yml) for the full schema with comments.

### Switching providers

local-review speaks the OpenAI chat-completions API — every major provider supports it:

| Provider | `base_url` | Notes |
|---|---|---|
| OpenAI | `https://api.openai.com/v1` | Default |
| Anthropic | `https://api.anthropic.com/v1` | Use chat-completions, not messages |
| Groq | `https://api.groq.com/openai/v1` | Fast |
| Together | `https://api.together.xyz/v1` | Llama, Mixtral, etc. |
| OpenRouter | `https://openrouter.ai/api/v1` | One key, all models |
| Ollama | `http://localhost:11434/v1` | **Fully offline.** No data leaves your machine. |
| vLLM | `http://your-host/v1` | Self-hosted |

## Pre-commit hook

```sh
cp examples/pre-commit .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

Or with [husky](https://typicode.github.io/husky/) / [lefthook](https://github.com/evilmartians/lefthook): run `local-review staged` in the `pre-commit` step.

Bypass for emergencies: `LOCAL_REVIEW_SKIP=1 git commit ...`.

## CLI

```
# v0 Single-LLM mode
local-review staged                  # review git diff --cached (pre-commit)
local-review commit [<rev>]          # review one commit (default: HEAD)
local-review branch [<base>]         # review branch vs base (default: main)

# v0.1 Multi-LLM mode
local-review multi staged            # parallel review with all enabled LLMs
local-review multi commit [<rev>]    # multi-LLM review of one commit
local-review multi branch [<base>]   # multi-LLM review of branch

# Utilities
local-review doctor                  # check LLM installations
local-review config                  # print resolved config (API keys masked)
local-review version                 # print version
```

Common flags:

| Flag | Purpose |
|---|---|
| `--model <id>` | Override provider.model |
| `--base-url <url>` | Override provider.base_url |
| `--min-severity <tier>` | `nit` / `info` / `warning` / `major` / `critical` |
| `--max-findings <n>` | Cap output |
| `--json` | Emit JSON (for CI integration) |

Exit codes:

- `0` — no blocking findings
- `2` — `major` or `critical` findings present (pre-commit gate)
- non-zero — local-review itself failed (the hook ignores this and lets the commit through)

## Prompt packs

local-review ships with packs for `default`, `typescript`, `go`, `python`, `rust`, with more coming. The CLI auto-picks based on the dominant language in your diff. Force a specific pack with `review.prompt_pack: <id>` in your YAML config.

Each pack is a markdown file (in [`internal/prompts/packs/`](internal/prompts/packs/)) that defines:
- What to look for (priority-ordered)
- Severity tiering rules
- Hard rules ("never invent code that isn't in the diff")
- Output JSON schema

See [`docs/prompt-packs.md`](docs/prompt-packs.md) for how to write or override one.

## What it does NOT do (yet)

- **No multi-file refactor reasoning.** local-review reviews diffs, not architectures.
- **No auto-fix / auto-PR.** Findings are advisory.
- **No GitHub integration in the binary.** The `--json` output is structured for piping into your CI's PR-comment tool of choice. (A `local-review github` mode is on the roadmap.)
- **No telemetry.** None. Ever.

## For organizations

Distributing to a few hundred engineers? Two patterns work:

1. **Org config repo.** Drop a `.local-review.yml` in each project that sets `org.config_url: https://your-internal-host/local-review.yml`. (Org-config fetching is on the roadmap; today, just commit the YAML to each repo.)
2. **One install command in onboarding.** `curl -fsSL <install.sh> | sh` plus an env var = done.

## Privacy

The only network call local-review makes is to the chat-completions endpoint **you configure**. Nothing else — no telemetry, no analytics, no auto-update calls. Run against Ollama and your code never leaves the machine.

## Develop

```sh
go test ./...
go build -o local-review ./cmd/local-review
./local-review staged
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).

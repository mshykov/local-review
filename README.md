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
  <a href="#multi-llm-reviews">Multi-LLM</a> •
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
# 1. Install one or more LLM CLIs (each one is a "team mate" reviewing your code)
npm install -g @anthropic-ai/claude-code   # Claude   — free tier via `claude login`
npm install -g @google/gemini-cli          # Gemini   — free key from Google AI Studio
npm install -g @openai/codex               # Codex    — ChatGPT Plus or OPENAI_API_KEY

# 2. Authenticate the ones you want
claude login
export GEMINI_API_KEY=...
export OPENAI_API_KEY=...

# 3. Check who's ready
local-review doctor

# 4. Review your branch — every authenticated CLI runs in parallel and findings get merged
local-review review
```

That's it. `local-review review` is the canonical command — it runs every LLM CLI that's installed AND authenticated, in parallel, and prints a merged report.

By default findings at `warning`+ are shown and `major`/`critical` exit non-zero (good for pre-commit hooks).

If you don't have any LLM CLI installed, run `local-review init` to set up a single-LLM review against any OpenAI-compatible API instead.

## Multi-LLM is the default

**Every authenticated LLM CLI runs by default.** No opt-in, no enabling — if you `claude login`, claude runs. If you `export OPENAI_API_KEY=...`, codex runs. If you don't authenticate one, it's silently skipped.

```sh
# Default: all active LLMs
local-review review

# Restrict to a subset (overrides config)
local-review review --only claude,gemini

# Pick a specific model for one agent
local-review review --claude-model claude-opus-4-7

# Use a specific agent to do the merge
local-review review --merge-with claude
```

### Supported LLMs

| LLM | Free Option | Installation |
|-----|-------------|--------------|
| **Claude** | ✅ Free tier via `claude login` (claude.ai account) | `npm install -g @anthropic-ai/claude-code` |
| **Gemini** | ✅ Free API key from [Google AI Studio](https://aistudio.google.com/apikey) | `npm install -g @google/gemini-cli` |
| **Codex** | ⚠️ ChatGPT Plus ($20/mo) **or** OpenAI API key (pay-per-token) | `npm install -g @openai/codex` |

**How it works:**
1. Detects installed LLM CLIs and which are authenticated (`local-review doctor`)
2. Runs every authenticated CLI in parallel
3. Saves each review to `.local-review/reviews/<branch>/<commit>_<llm>.md`
4. Merges findings (dedup, consensus tagging) into one report
5. Prints the merged report to stdout (also saved as `<commit>_merged.md`)

**Authentication — what each LLM needs:**

| LLM | Default (preferred) | Alternative |
|---|---|---|
| **Claude** | `claude login` — Anthropic OAuth, works with the free tier on a claude.ai account | `export ANTHROPIC_API_KEY=...` (paid API access) |
| **Gemini** | `export GEMINI_API_KEY=...` — free key from [Google AI Studio](https://aistudio.google.com/apikey) | `gemini /auth` for Google OAuth |
| **Codex** | `codex login` — uses your ChatGPT Plus subscription ($20/mo) | `export OPENAI_API_KEY=...` — pay-per-token; usually **cheaper** for occasional review use |

Run `local-review doctor` to see which CLIs you have installed and authenticated. Each row that isn't ✓ ready tells you exactly what to fix.

**Configuration is optional.** If `~/.local-review.yml` or `./.local-review.yml` exists it overrides defaults; CLI flags override config:

```yaml
# .local-review.yml — example: pin specific models, disable codex
llms:
  claude:
    model: claude-opus-4-7

  gemini:
    model: gemini-2.0-flash
    api_key_env: GEMINI_API_KEY

  codex:
    enabled: false           # opt-out (still runs if --only codex is passed)

merge:
  preferred_llm: auto        # or: claude, gemini, codex
```

See [`examples/.local-review-multi.yml`](examples/.local-review-multi.yml) for full schema.

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
| OpenAI | `https://api.openai.com/v1` | Default. `gpt-4o-mini` is cheap; `gpt-4o` for harder reviews. |
| Anthropic | `https://api.anthropic.com/v1` | Anthropic's [OpenAI-compatible endpoint](https://docs.anthropic.com/en/api/openai-sdk). Use exact model names (e.g. `claude-sonnet-4-6`, `claude-opus-4-7`). |
| Mistral | `https://api.mistral.ai/v1` | EU-hosted; Codestral is code-tuned. See [`examples/.local-review.mistral.yml`](examples/.local-review.mistral.yml). |
| DeepSeek | `https://api.deepseek.com/v1` | Cheapest cloud option. See [`examples/.local-review.deepseek.yml`](examples/.local-review.deepseek.yml). |
| Groq | `https://api.groq.com/openai/v1` | Fast inference; Llama, Qwen, etc. |
| Together | `https://api.together.xyz/v1` | Llama, Mixtral, Qwen — many open-weights options. |
| OpenRouter | `https://openrouter.ai/api/v1` | One key, all models. |
| Ollama | `http://localhost:11434/v1` | **Fully offline.** No data leaves your machine. |
| vLLM | `http://your-host/v1` | Self-hosted. |

The fastest way to set any of these up is `local-review init`, which writes a working `.local-review.yml` from a preset.

## Pre-commit hook

```sh
cp examples/pre-commit .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

Or with [husky](https://typicode.github.io/husky/) / [lefthook](https://github.com/evilmartians/lefthook): run `local-review staged` in the `pre-commit` step.

Bypass for emergencies: `LOCAL_REVIEW_SKIP=1 git commit ...`.

## CLI

```
# Review (multi-LLM by default; falls back to single-LLM via configured provider if no CLI is active)
local-review review [<base>]         # canonical: current branch vs <base> (default: main)
local-review staged                  # review git diff --cached (pre-commit)
local-review commit [<rev>]          # review one commit (default: HEAD)
local-review branch [<base>]         # alias of `review` for muscle-memory

# Utilities
local-review init                    # interactive setup (writes .local-review.yml)
local-review doctor                  # check LLM installations + auth state
local-review config                  # print resolved config (API keys masked)
local-review version                 # print version
```

Common flags:

| Flag | Purpose |
|---|---|
| `--only <list>` | Comma-separated agents to run (e.g. `claude,gemini`); overrides config |
| `--claude-model <id>` | Override claude's model (same for `--gemini-model`, `--codex-model`) |
| `--merge-with <agent>` | Pick which agent merges findings (default: auto) |
| `--model <id>` | Override `provider.model` (single-LLM fallback only) |
| `--base-url <url>` | Override `provider.base_url` (single-LLM fallback only) |
| `--min-severity <tier>` | `nit` / `info` / `warning` / `major` / `critical` |
| `--max-findings <n>` | Cap output |
| `--json` | Emit JSON (for CI integration) |

Config wins by default; flags override config at runtime (e.g., `--only codex` runs codex even if your config sets `codex.enabled: false`).

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

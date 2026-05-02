# local-review

**Local, BYOK code review for any language.** Runs against a git diff, hands it to whichever LLM you point it at, prints findings. No SaaS, no telemetry, no signup.

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
local-review staged                  # review git diff --cached (pre-commit)
local-review commit [<rev>]          # review one commit (default: HEAD)
local-review branch [<base>]         # review branch vs base (default: main)
local-review config                  # print resolved config
local-review version
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

local-review ships with packs for `default`, `typescript`, `go`, `python`, with more coming. The CLI auto-picks based on the dominant language in your diff. Force a specific pack with `review.prompt_pack: <id>` in your YAML config.

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

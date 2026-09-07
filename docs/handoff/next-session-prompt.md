# Next-session starter prompt

Paste the block below into a fresh session opened in the `local-review` repo. It
carries only what isn't already discoverable from the repo — everything else the
assistant should read for itself.

---

```
We're working on local-review (this repo). Before doing anything, read:

  - CLAUDE.md (operating rules — rule 13 on personal data is load-bearing:
    this is a PUBLIC repo and a real Tailscale IP was committed once already)
  - docs/handoff/session-context.md (what the last sessions did and what's open)

Current state: v0.17.5 is released and verified. No open work-in-progress —
0 Dependabot/code-scanning/secret-scanning alerts, working tree clean.

Open items are listed at the end of session-context.md. The two smallest and
most worthwhile:

  1. docs/release.md still documents TAP_GITHUB_TOKEN as needing "repo" scope.
     That's stale — it's now a fine-grained PAT scoped to mshykov/homebrew-tap
     with Contents: read/write. Fix the doc so nobody recreates the
     over-privileged token.
  2. Two routine Dependabot PRs are open (#185, #190).

Working conventions: branch before editing; one concern per PR; full verify
(gofmt -s, go vet, golangci-lint, go test -race ./..., e2e) before push; open a
PR and merge when CI is green. Expect Copilot to re-review on every push and
required-conversation-resolution to block merge until threads are resolved.

Start by confirming the repo state yourself (git status, gh pr list, latest
release) rather than trusting this summary — then tell me what you'd tackle.
```

---

## Variants for specific work

**If picking up the local-model reviewing thread:**

```
Read docs/handoff/session-context.md, section "Local ollama setup". v0.17.5 added
warnings for silent truncation, slow local runs, provider timeouts, and malformed
merged reports. I want to verify they behave well in real use / tune the
thresholds. Note the config is ~/.local-review.yml (.yml only — .yaml is silently
ignored).
```

**If continuing the audit trail:**

```
Read docs/handoff/session-context.md. The 2026-07 external audit findings are all
fixed. I'd like a fresh audit pass — either the tool's own
`local-review audit --topic security` (output committed under audit/) or an
independent read. Verify any claim by reproducing it before reporting; last time
a "critical" finding was only credible because it was reproduced first.
```

**If working on zero-to-moat instead:**

```
Switch to ../zero-to-moat. It has 7 open Dependabot PRs including majors
(next 15→16, typescript 5→6) that need real testing, not rubber-stamping.
Its definition of done: pnpm lint && pnpm typecheck && pnpm test && pnpm build.
Don't run pnpm build while pnpm dev is running — they share .next/.
```

---

## Notes for whoever writes the next prompt

- **Point at the repo, don't restate it.** CLAUDE.md and the topic docs are the
  source of truth and are already loaded automatically; duplicating them into a
  prompt just creates drift.
- **Ask for verification, not trust.** The most valuable finding of the last cycle
  (a shipped command broken for 11 releases) came from *running* the thing, not
  from reading about it. Prompts that invite the assistant to confirm state
  produce better work than prompts that assert it.
- **Never paste secrets or real hosts into a prompt.** Same rule as the codebase.

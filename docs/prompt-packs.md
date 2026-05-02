# Prompt packs

A pack is a markdown file under `internal/prompts/packs/`. It's the entire system prompt sent to the LLM for a review run.

Filename = language id from `internal/lang/detect.go` (e.g. `typescript.md`, `go.md`, `python.md`). `default.md` is the fallback when no language-specific pack exists.

## Anatomy of a pack

A good pack has four parts:

1. **What to look for** — priority-ordered list of things that matter for this language. Correctness > security > performance > maintainability > style.
2. **Severity tiers** — five levels (`nit` / `info` / `warning` / `major` / `critical`). State the bar for each.
3. **Hard rules** — explicit prohibitions. The big ones:
   - Never comment on lines outside the diff.
   - Never invent code that isn't in the diff.
   - Never speculate about the rest of the file.
   - Return zero findings on trivial diffs.
4. **Output format** — the JSON schema the LLM must match.

Packs are stacked: language packs say "Apply the default rules. Plus: …" so the default's hard rules and JSON schema apply everywhere.

## Output schema

Every pack must instruct the LLM to return:

```json
{
  "findings": [
    {
      "file": "src/foo.ts",
      "line": 42,
      "severity": "major",
      "title": "Short imperative summary, < 80 chars",
      "body": "1–3 sentence explanation. Why and what to do.",
      "tag": "security"
    }
  ]
}
```

Fields:
- `file`, `line` — must come from the diff (the LLM can see line numbers in hunk headers).
- `severity` — one of `nit`, `info`, `warning`, `major`, `critical`.
- `title` — one-line imperative summary.
- `body` — explanation. Optional.
- `tag` — optional category. Suggested: `correctness`, `security`, `perf`, `maintainability`, `style`.

If there are no findings, the model must return `{"findings": []}`.

## Adding a new language

1. Create `internal/prompts/packs/<lang>.md`.
2. Add the file extension(s) to `byExt` in `internal/lang/detect.go`.
3. Add a constant for the language id alongside the others.
4. Run `go test ./internal/prompts/... ./internal/lang/...`.

That's it. The orchestrator picks the pack automatically based on the dominant language in the diff.

## Overriding per-repo

Set `review.prompt_pack: <id>` in your repo's `.local-review.yml` to force a specific pack regardless of detection. Useful when:

- The repo has mixed languages and you want one specific style.
- You've got a custom pack you maintain in your fork.

## Testing changes

The packs ship as Go embedded files. After editing markdown:

```sh
go test ./internal/prompts/...
go run ./cmd/local-review staged   # against a sample diff in some test repo
```

There's no harness for "did the LLM follow the new rules" yet — that's a known gap. For now, eyeball it on a few real diffs.

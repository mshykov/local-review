# Code Review Checklist

A practical, opinionated checklist for reviewing pull requests.

This is the same checklist that [local-review](https://github.com/mshykov/local-review)'s prompt packs operationalize — every item below is something the tool actively looks for in a diff. Use it as-is for human reviews, or run `local-review review` on a branch to get an LLM pass against these rules.

What makes this list different from the dozen other "code review checklist" repos:

- **OWASP 2025-aligned** security section
- **Concrete measurables** (function length, nesting depth) alongside open-ended questions
- **Severity tiers** so reviewers know what's blocking vs informational
- **Specialist-review prompt** for high-risk diffs (crypto, auth, payments, ML, a11y)
- **Living document** — published from the same repo as the tool that runs it, so the rules and the implementation evolve together

---

## Severity tiers

Use these consistently so blocking findings stand out from noise:

- **critical** — would break production, lose data, or open a security hole. Block merge.
- **major** — likely bug, performance issue, or security concern. Should be fixed before merge.
- **warning** — design or maintainability problem worth raising before merge. Author judgment.
- **info** — context the author probably wants to know. Not a gate.
- **nit** — style preference. Optional.

If you wouldn't merge with the issue still in the diff, it's `major` or higher. If it's "I'd write this differently," it's `nit`.

---

## 1. Correctness

- [ ] Does the code do what the description says it does?
- [ ] Are off-by-one errors, null/undefined handling, and edge cases covered?
- [ ] Are errors swallowed silently anywhere they should propagate?
- [ ] Are race conditions possible (shared state, concurrent goroutines/threads/promises, async timing)?
- [ ] Are resources (file handles, connections, contexts, subscriptions) closed on every code path including error paths?
- [ ] Do conditional branches make sense for every realistic input — including empty, negative, very large, malformed?
- [ ] Do state mutations in this change have side effects on callers that aren't visible in the diff?

## 2. Security and Data Privacy (OWASP 2025-aligned)

- [ ] Are user inputs validated, sanitized, and escaped against injection (SQL, command, path traversal, XSS, LDAP, NoSQL)?
- [ ] Are secrets (API keys, passwords, tokens, certs) absent from source, config, comments, and test fixtures?
- [ ] Is authentication and authorization enforced at the right layer for every new endpoint or capability?
- [ ] Have default credentials, admin endpoints, and debug routes been disabled or gated?
- [ ] Are third-party dependencies pinned, with lock files committed, and is provenance verifiable (signatures, hashes, audited sources)?
- [ ] Is cryptography using current standard primitives (AES-GCM, Argon2id, Ed25519) — not MD5/SHA1/DES/ECB?
- [ ] Is deserialization of untrusted input avoided, or strictly typed and validated? *Unsafe deserialization is a recurring RCE vector (Log4Shell, Spring4Shell).*
- [ ] Is security-relevant activity logged in a way that supports incident investigation (auth events, access denials, config changes) — without including the credentials themselves?
- [ ] Is PII or sensitive data (credit cards, health info) handled per applicable regulations and never in plaintext logs?
- [ ] Do error messages and stack traces avoid leaking internal paths, query fragments, or partial secrets to end users?

## 3. Performance

- [ ] Are there O(n²)+ algorithms running over user-scaled or unbounded inputs?
- [ ] Are there N+1 query patterns (loops that hit the database/API per iteration)?
- [ ] Is blocking I/O on a hot path (request handler, render loop, event loop)?
- [ ] Are memory allocations bounded? (Reading a whole file/response into memory is a common footgun.)
- [ ] Is caching used where it's cheap and high-leverage — and invalidated correctly?
- [ ] Is pagination or streaming applied to potentially-large result sets?

## 4. Maintainability

- [ ] Does each function/class have a single, clear responsibility? (SRP)
- [ ] Does the change avoid breaking existing callers in ways that would cascade through the codebase? (OCP, LSP)
- [ ] Are interfaces small enough that callers don't depend on methods they don't use? (ISP)
- [ ] Do high-level modules depend on abstractions, not concrete implementations? (DIP)
- [ ] Before adding a new helper, does similar functionality already exist in the codebase that could be reused?
- [ ] Are there meaningful duplications (not just lexical copy-paste) that should be consolidated?
- [ ] Are abstractions leaking implementation details that callers shouldn't need to know about?
- [ ] Is the code over-engineered for hypothetical future requirements that may never materialize?
- [ ] Is there dead code, unreachable branches, or commented-out blocks left behind?
- [ ] Are names clear enough that comments wouldn't be needed to understand them?
- [ ] Are comments accurate, useful, and current — or do they restate the code, lie about it, or describe behavior the code no longer has?
- [ ] Are functions or methods longer than ~50 lines? If so, can they be decomposed?
- [ ] Is any block of logic nested more than 3 levels deep? Can it be flattened with early returns or extracted into a helper?
- [ ] Is the new type / function in the right file/folder/package — matching the rest of the area's responsibility?

## 5. Error Handling and Logging

- [ ] Are errors handled at the right layer — close to the cause, not several frames up where context is lost?
- [ ] Are error messages actionable? Do they say *what* failed and *what to do*, not just "error: something went wrong"?
- [ ] Are log events sufficient to debug a production incident *without* re-running the code?
- [ ] Are PII, secrets, or tokens absent from log lines?
- [ ] Do log levels match the actual severity? (Don't INFO things that need to page; don't ERROR recoverable conditions.)

## 6. Testing and Testability

- [ ] Are there tests for the new feature or bug fix? Would they fail if the code broke?
- [ ] Are edge cases covered (empty inputs, max sizes, boundary values, concurrent access)?
- [ ] Are tests free of shared state, time dependence, or network dependence that would make them flaky?
- [ ] Is the production code's *shape* getting in the way of testing? (No seam to inject a fake, side effects in constructors, hidden globals, untestable static methods.)
- [ ] Do existing tests still pass — or were they "fixed" by weakening their assertions?

## 7. Backward Compatibility and Dependencies

- [ ] Does this change modify a public API (signature, return type, error shape)? If so, is the change opt-in, or has the old behavior been deprecated first?
- [ ] Does this change rename or remove a config field? If so, is there a migration path?
- [ ] Does this change alter persisted data shape (DB schema, file format, cache key)? If so, is there a compatibility shim or migration?
- [ ] Does the change require updates to docs, CHANGELOG, configuration examples, or README that aren't in this PR?
- [ ] Do new dependencies (or version bumps) introduce compile-time or runtime cost that's worth the value they add?

## 8. Usability and Accessibility

For code that ships UI or public APIs:

- [ ] Is the public API documented? Is the contract obvious from the name and signature alone?
- [ ] Is the API/UI intuitive — does it match the user's mental model of the task?
- [ ] (UI) Is keyboard navigation supported? Are screen-reader semantics correct (ARIA roles, alt text, focus order, skip links)?
- [ ] (UI) Does color contrast meet WCAG AA at minimum?
- [ ] (UI) Is the layout reasonable on small viewports?
- [ ] Do error states surface to the user clearly, or are they silently swallowed?
- [ ] Do defaults match the most common case? Is advanced behavior opt-in rather than opt-out?

## 9. Ethics and Fairness

When the change touches user data, ML/AI, or behavior design:

- [ ] Does the change introduce manipulative patterns (dark patterns, attention-extraction loops, addictive feedback)?
- [ ] Could the model, heuristic, or ranking introduce biased outcomes — systematically advantaging or disadvantaging a group?
- [ ] Does the change require capabilities (high bandwidth, modern browsers, English) that exclude a meaningful user segment?
- [ ] Are we collecting more user data than we need? Retaining it longer than necessary?
- [ ] Is the user informed about how their data will be used and stored?

## 10. Style

- [ ] Are naming, formatting, and idiom choices consistent with the rest of the codebase?

Mention only when style **actively obscures intent** or violates an enforced team convention. Don't nitpick what a linter should handle.

## 11. Specialist Review Needed?

When the diff touches:

- Cryptography or key management
- Authentication or authorization flows
- Payments or financial logic
- ML model behavior
- Accessibility-critical UI
- Database migrations on production-scale data

…recommend a specialist look it over before merge. Add this as an `info` note for the author, not as a blocking finding.

---

## Reviewer behavior

- **Acknowledge good practices**, not just errors. Reviews are signals to the author about what's working *and* what isn't.
- **Understand the intent** of the change before critiquing the implementation.
- **Ask clarifying questions** when something looks wrong but might not be — the author probably has context you don't.
- **Avoid accusatory language**: "consider", "we", "this line" land better than "you should" or "you forgot".
- **Prefer one sharp finding over five vague ones.** Reviewer fatigue is real; signal-to-noise matters.
- **Don't speculate about code outside the diff.** If you need broader context, say so explicitly.

---

## Want this checklist run on your code automatically?

[`local-review`](https://github.com/mshykov/local-review) implements every rule above as an LLM-driven prompt pack. Install it, run `local-review review` on a branch, and get findings in seconds — using whichever LLM CLI you've authenticated (Claude, Gemini, Codex), no SaaS, no telemetry, BYOK.

```sh
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
local-review review
```

---

## License

This checklist is published under the [MIT License](LICENSE) as part of the [local-review](https://github.com/mshykov/local-review) project. Copy, adapt, and redistribute freely.

If you have suggestions or find a gap, open an issue or PR on the local-review repository.

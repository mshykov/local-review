# Code Review Guidelines

Best-in-class code review standards for `local-review`, synthesized from Google's Engineering Practices, *Software Engineering at Google*, Microsoft's Engineering Playbook, OWASP Top 10:2025, DORA / Cisco / SmartBear empirical research, and the canon of software-engineering literature.

This document drives both the **prompt packs** (what `local-review` flags in user code) and the **process** (how contributors review changes to local-review itself).

---

## Philosophy

**Primary goal**: improve overall code health over time without blocking progress on non-essential polish. The bar is *clear net improvement to the codebase*, not perfection — Google's first principle of code review.

**Core principles**:

1. **Optimize for the team's velocity, not the individual's.** A clean, readable, tested change merged quickly beats a perfect change that sits for a week.
2. **Approve when the change improves the codebase, not when it's perfect.** There is no perfect code, only better code.
3. **Honesty over politeness, signal over noise.** One sharp finding beats five vague ones. Don't pad reviews with nitpicks to look thorough.
4. **Comment on code, not people.** Use *"this function"* / *"we"* / *"could we consider"* — never *"you broke this."*
5. **Distinguish blocking from non-blocking feedback.** Nits must never block a merge.
6. **Reviewers own the codebase too.** A reviewer who LGTMs bad code shares responsibility for the bug. Review with conviction.
7. **Escalate, don't stall.** If author and reviewer disagree, escalate within a day — to a tech lead, an OWNER, or a face-to-face. Never let a PR rot.
8. **Reviews are a teaching opportunity.** Sharing knowledge is the largest measured benefit of review (Google's nine-million-CL study).

---

## Review Priorities (Ordered by Impact)

### 1. Correctness

**Goal**: prevent bugs from reaching production.

- Business logic implements requirements correctly.
- Edge cases are handled: empty inputs, single-element inputs, very large inputs, zero, negative numbers, `null` / `undefined` / `None`, NaN, Infinity, empty strings, unicode/emoji, very long strings.
- Boundary conditions: off-by-one, range checks, pagination edges, array indexing.
- Error paths are correct: the code does the right thing when a dependency fails, a network call times out, or input is malformed.
- State transitions are valid; state machines cannot enter illegal states.
- Time / time-zone / DST / leap-year / clock-skew correctness.
- Resource leaks (file handles, connections, memory).
- The change does not introduce a regression in any related code path — the reviewer mentally traces at least the immediate callers.

**Critical patterns by language**:
- Unchecked errors (Go), unhandled promise rejections (TS), null pointer dereferences (anywhere), integer overflows in user-scaled operations, logic errors in conditional branches.

---

### 2. Security

**Goal**: prevent vulnerabilities and data exposure.

The 2025 OWASP Top 10 introduced **Software Supply Chain Failures** and **Mishandling of Exceptional Conditions** as new categories, and elevated **Security Misconfiguration**. Every reviewer should know the categories cold.

**A01 — Broken Access Control** (now includes SSRF)
- Every endpoint, function, and resource enforces **authorization on the server side**. Client-side checks do not count.
- Object-level access control prevents IDOR — a user cannot access another user's resource by guessing an ID.
- APIs verify ownership before returning data.
- Server-side URL fetches validate against an allowlist; no internal metadata endpoints (`169.254.169.254`) reachable.

**A02 — Security Misconfiguration**
- No default credentials, demo accounts, or debug endpoints in production paths.
- Verbose error messages and stack traces are not returned to clients.
- HTTP security headers set: `Content-Security-Policy`, `Strict-Transport-Security`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`.
- CORS is restrictive. `Access-Control-Allow-Origin: *` paired with credentials is rejected.

**A03 — Software Supply Chain Failures** *(new in 2025)*
- New dependencies pinned to specific versions; lockfile checksums verified by CI.
- No new dependency without an SCA scan (Snyk, Dependabot, OSV-Scanner, GitHub Advanced Security).
- **No "hallucinated" packages from AI assistants** — verify the package exists, is maintained, has reasonable adoption.
- Build provenance / SBOM is updated.

**Injection** (still real, just demoted)
- Every database call uses parameterized queries / prepared statements / an ORM. **No string concatenation into SQL ever.**
- OS commands constructed with argument arrays, not shell strings.
- User input rendered into HTML is context-appropriately escaped (HTML, attribute, JS, CSS, URL).
- No `eval`, no `Function()` constructors, no `pickle.loads` on untrusted data.

**A04 — Insecure Design**
- Threat modeling done for the feature ("what's the worst that could happen?").
- Rate limiting present on authentication, password reset, and any expensive endpoint.

**A05 — Cryptographic Failures**
- No MD5 or SHA-1 for security purposes. Use SHA-256/512 or BLAKE3.
- Passwords hashed with Argon2id (preferred), bcrypt, or scrypt — never raw hash functions.
- Secrets read from a secret manager (Vault, AWS Secrets Manager, KMS) — not committed, not in env files in source.
- Cryptographic randomness uses a CSPRNG — never `Math.random` for security.
- TLS enforced; weak cipher suites and TLS < 1.2 disabled.

**A06 — Identification & Authentication Failures**
- Sessions expire on a sensible timeout. Tokens rotated on privilege change. Logout actually invalidates the session.
- Cookies flagged `Secure`, `HttpOnly`, `SameSite=Lax` (or `Strict`).

**A07 — Software / Data Integrity Failures**
- Auto-update mechanisms verify signatures.
- Deserialization of untrusted data uses safe formats (JSON), never native pickle / ObjectInputStream.

**A08 — Security Logging & Alerting Failures**
- Authentication attempts (success and failure) logged with user, timestamp, source IP.
- **No password, token, secret, session ID, full credit-card number, or excessive PII appears in any log line** (CWE-532).

**A10 — Mishandling of Exceptional Conditions** *(new in 2025)*
- No "fail-open" logic. When auth service is down, the request fails — not succeeds.
- `catch` blocks do not silently swallow exceptions. They log, rethrow, or return a typed error.
- Edge cases (network failure mid-write, partial failure of a batch, out-of-disk) leave the system in a consistent state.

**Cross-cutting**:
- All input from outside the trust boundary is **validated, normalized, and bounded** before use.
- No user-controlled data flows into file paths without `realpath`-style normalization (path traversal).
- CSRF tokens present for state-changing operations from browsers.
- No secrets (API keys, passwords, tokens) in source code.

---

### 3. Performance & Scalability

**Goal**: prevent scalability issues and resource exhaustion.

- **No N+1 queries.** A loop that issues one DB query per item is rejected.
- Database queries hit appropriate indexes. New `WHERE` / `ORDER BY` / `JOIN` columns have indexes, or the absence is justified.
- Pagination wherever a result set could grow without bound. **No `SELECT *` from tables that can grow.**
- Caches have explicit invalidation strategies and bounded sizes. An unbounded cache is a memory leak.
- Hot paths avoid allocation-heavy patterns (boxing, unnecessary object creation in inner loops).
- Algorithmic complexity appropriate for the expected data size (no `O(n²)` over millions of items).
- Streaming / chunked processing for large payloads instead of loading everything into memory.
- Network calls batched where possible.
- Synchronous I/O does not block request-handling threads.
- Performance-critical changes include a benchmark or load-test result in the PR.

**Concurrency, async & distributed systems** (the bugs that kill production at 3 a.m.):
- Shared mutable state is protected by locks, atomics, or a queue — or genuinely immutable.
- No locks held across I/O, blocking calls, or `await` boundaries.
- No deadlocks: locks always acquired in the same order globally.
- Async functions correctly propagate errors and cancellation.
- Resources released on every code path including exceptions (RAII / `using` / `try-with-resources` / `defer`).
- Distributed operations are **idempotent**, or guarded by an idempotency key.
- Retries use exponential backoff with jitter and a cap. Naive retries cause thundering herd.
- Timeouts on every external call. **No call hangs indefinitely.**
- Race conditions that matter (TOCTOU, double-spend, double-create) eliminated by appropriate primitives (`SELECT ... FOR UPDATE`, unique constraints, atomic CAS).

---

### 4. Maintainability

**Goal**: ensure code remains understandable and evolvable.

The defining test: **can a teammate two years from now, on a Friday at 5pm, fix a bug here without paging anyone?**

**Design**:
- Single Responsibility Principle — functions/classes do one thing.
- DRY violations matter; meaningful duplication, not just textual copy-paste.
- Leaky abstractions: implementation details exposed in interfaces.
- God objects — classes with too many responsibilities.
- Premature optimization — complexity without measured need.
- Over-engineering — solving hypothetical future problems (YAGNI).
- Dead code, commented-out code, unused imports — removed.
- No "framework within a framework." Reviewers reject changes that reinvent libraries the project already uses.
- If a class has only one implementation, the interface is questionable.
- If an abstraction has only one caller, ask whether the abstraction earns its keep.

**Naming** (half of code review):
- Variable names reveal intent: `daysUntilExpiry`, not `d`.
- Function names describe what they do, not how: `calculateGrossRevenue()`, not `loopThroughOrders()`.
- Booleans read as predicates: `isActive`, `hasPermission`, `canEdit`.
- Functions that mutate state say so: `setX`, `updateX`, `appendX`. Functions that don't, don't.
- No misleading names. `getUser` must not also send an email.
- Use the project's domain language consistently.
- Acronyms are either fully uppercase (`URL`, `API`) or treated as words (`Url`, `Api`) — pick one, be consistent.

**Comments**:
- Explain **why**, not **what**. *What* is the code's job; if you need a comment to explain it, the code is unclear.
- Document non-obvious business rules.
- Remove commented-out code (use git history).
- TODOs must have owners and issue links.

**Function complexity**:
- Functions > ~20-50 lines should be scrutinized for decomposition (Clean Code's guideline: ~20).
- Cyclomatic complexity > 10 is a red flag (Google's readability reviewers flag ≥15).
- Deep nesting (>3 levels) hurts readability.
- Long parameter lists (>3-4 args) and flag arguments are flagged.
- Early returns / guard clauses preferred over deep nesting.

**API design & backward compatibility** (treat every API change as a separate review):
- Public API changes are backward compatible. Field removal, type changes, required-vs-optional flips are breaking.
- Old endpoints/fields marked deprecated, logged when used, removed only after a documented migration window.
- Errors follow the project's contract (HTTP status, error code, error body shape).
- OpenAPI / GraphQL / Protobuf schema updated and committed.

**Database & migrations**:
- Migrations are **safe to run on production**: no long-running locks, no full table rewrites on hot tables during business hours.
- Backwards-compatible deploys: new columns nullable or have defaults; column drops happen in a separate, later release.
- Indexes for new query patterns added in the same migration.
- No raw user input interpolated into queries.

---

### 5. Testing

**Goal**: ensure code is verifiable and changes don't break existing functionality.

- Every new behavior has at least one test.
- Test pyramid respected: many fast unit tests, fewer integration tests, even fewer E2E.
- Tests test **behavior, not implementation**. Refactoring should not break tests that still pass functionally.
- Tests are **deterministic**. No `sleep()` for synchronization, no real network, no real time, no random seeds without seeding.
- Tests are **fast**. Unit tests complete in milliseconds.
- Tests have **clear arrange / act / assert** structure and a name that describes the scenario.
- Tests cover **failure modes**, not just the happy path.
- Tests do not over-mock. A test that mocks the system under test is testing nothing.
- **No flaky tests.** If a test is flaky, it is fixed or quarantined, never ignored.
- Coverage **does not decrease**. Bar for AI-generated code is higher (see §7).
- Bug fixes include a regression test that would have caught the bug.
- Performance-sensitive code paths have a benchmark, not just a unit test.

**Coverage thresholds** (rough targets, not gates):
- Critical paths: 100%
- Business logic: 80%+
- Utilities: 70%+
- AI-generated code: 85-90%
- UI components: integration tests preferred over unit tests

---

### 6. Observability, Logging & Operability

**Goal**: a feature is not done until it is observable in production.

- Every meaningful operation emits a structured log line with: trace ID, user/account ID (or hash), feature name, outcome, latency.
- **No PII or secrets in logs.** Apply log redaction where structured fields could carry sensitive data.
- Log levels appropriate (`DEBUG`, `INFO`, `WARN`, `ERROR`). Production-noisy `INFO` logs in tight loops are flagged.
- Metrics emitted for the four golden signals: **latency, traffic, errors, saturation**.
- Trace context propagated across service calls.
- A dashboard or alert exists, or a follow-up ticket is filed.
- User-facing error messages are clear, actionable, and don't leak internals.
- Internal logs include correlation/trace IDs.
- Graceful degradation: if a non-critical dependency fails, the user-facing path still works in a reduced mode.

---

### 7. AI-Generated Code Review

**Why this is a first-class concern in 2026**: LinearB's 2026 benchmark found AI-generated PRs have a **32.7% acceptance rate vs. 84.4% for hand-written PRs**, and CodeRabbit's 470-PR analysis showed AI-generated code contains **1.75× more logic errors**. AI-generated code passes "looks reasonable" inspection while failing on edge cases, error paths, and architectural fit.

`local-review` itself is an AI code review tool. Reviewing AI-generated code with rigor is what we are.

**Disclosure**:
- The PR description discloses AI involvement: "first draft from Claude," "test stubs from Copilot," "refactor by Cursor agent."
- For substantial AI-generated changes (>100 LOC of AI output), the prompt or design intent is captured in the description.

**Logic correctness**:
- **Happy-path-only solutions**: LLMs produce beautiful happy-path code and weak edge-case handling. Reviewer asks: *what happens with `null`, `[]`, `""`, a 10-million-row input, a network failure?*
- **The "literal vs. practical" gap**: *"Get all users"* literally returns every row; in production it needs pagination, filtering, authorization. Question every AI-generated function through this lens.
- Error handling is real, not boilerplate. `catch (e) { console.log(e) }` is rejected.
- No silent failure; no catch-all that hides bugs.

**Hallucinations**:
- All imported libraries actually exist (npm/PyPI/crates.io check).
- All called APIs exist and accept the arguments shown.
- Cited RFCs, standards, and version numbers are real.

**Architecture**:
- AI did not invent a new abstraction layer when an existing one would do.
- AI did not duplicate logic that already exists in the codebase under a slightly different name.
- AI did not introduce a dependency that conflicts with an existing one.

**Security**:
- No hardcoded credentials in placeholder values that survived to the PR.
- AI-generated SQL is parameterized.
- AI-generated regex was tested against ReDoS-style inputs.

**Testing**:
- **Tests are not tautological.** If the same model wrote the implementation and the tests, they share blind spots. Test descriptions should be written *before* the implementation is generated.
- Coverage threshold for AI-generated code is **85-90%**.
- At least one adversarial test case per public function.

**Style**:
- AI did not invent new naming conventions, log formats, or import-ordering rules. The change matches surrounding code.
- Verbose, over-commented AI prose is trimmed to project norms.

---

## Severity Tiers

Use these consistently in review comments. They map to the severities `local-review` itself emits.

| Severity | Definition | Example | Action |
|---|---|---|---|
| **Critical** | Will break production, lose data, or create a security hole | SQL injection, null pointer crash, exposed credentials | **Block merge** |
| **Major** | Likely bug, performance issue, or security concern | N+1 query, missing error handling, race condition | **Block merge** |
| **Warning** | Design/maintainability problem worth addressing | Over-engineering, leaky abstraction, complex function | **Strong suggestion** |
| **Info** | Context the author should know | Better pattern exists, potential future issue | **FYI only** |
| **Nit** | Cosmetic/style; emitted by `local-review` only when `min_severity: nit` is configured | Naming preference, minor formatting | **Optional** |

### Comment-prefix conventions

When leaving inline review comments, prefix them with their intent. This eliminates ambiguity about whether a comment must block a merge:

```
nit:        — pure style / preference; author may ignore
suggestion: — useful but optional improvement
question:   — reviewer wants context, not necessarily a change
issue:      — real problem, should be addressed
blocking:   — will not merge until fixed
praise:     — explicit positive feedback (use this often)
fyi:        — informational, no action needed
```

These prefixes are widely adopted (Netlify's "feedback ladder," Microsoft's playbook). They turn a thread of equally-weighted comments into a sortable list.

---

## Process Norms (Size, Speed, SLAs)

Empirically derived from Cisco/SmartBear, Meta's internal research, Atlassian's published thresholds, and DORA's "elite performer" benchmarks.

| Metric | Target (elite) | Acceptable | Warning |
|---|---|---|---|
| **PR size** (LOC changed, excluding generated) | < 200 | < 400 | > 400 |
| **PR review session duration** | < 60 min | < 90 min | > 90 min |
| **Time to first review (P75)** | < 4 working hours | < 24 working hours | > 24 working hours |
| **Time to merge (P75)** | < 6 working hours | < 24 working hours | > 48 working hours |
| **Reviewers per PR** | 1-2 | 1-3 | > 3 |
| **Review velocity** (LOC reviewed per hour) | < 500 | < 1000 | > 1000 |

**Hard rules**:
- If a PR exceeds **400 lines**, the reviewer may decline review and ask for a split. PRs over 400 LOC get rubber-stamped — that is not review, it is theater.
- If the review queue exceeds **24 working hours** for first response, the team revisits its review SLA.
- If a reviewer cannot give a thorough review now, they say so immediately rather than rubber-stamping.

**Author responsibilities** (most slow reviews are caused by sloppy submissions, not slow reviewers):
- Self-review your own diff first. Authors who self-review catch ~30% of issues that reviewers would otherwise flag.
- Refactors and behavior changes go in **separate commits or separate PRs**.
- Auto-formatting / lint-fix changes go in their **own commit**, never mixed with logic.
- Title follows Conventional Commits: `feat(scope): …`, `fix(scope): …`, etc. Imperative mood, < 72 chars.
- Description answers, in order: **What** is changing? **Why**? **How was it tested?** **What is the rollout/rollback plan?**

---

## The Automation Layer

**Principle: by the time a human looks at a PR, every mechanical issue is already fixed.** Humans review logic, design, intent, and edge cases — nothing else.

If a human is reviewing it, automation has failed. Push everything below into CI:

- **Formatting** (`gofmt`, Prettier, Black, rustfmt) — non-negotiable, fails the build.
- **Linting** (`go vet`, ESLint, Pylint, golangci-lint, Clippy) — fails the build.
- **Type checking** (TypeScript strict, mypy, Sorbet) — fails the build.
- **Unit tests** — fails the build.
- **Coverage threshold** — fails the build below the team minimum.
- **SAST** (Semgrep, CodeQL, SonarQube) — fails on critical/high.
- **SCA / dependency scanning** (Snyk, Dependabot, OSV-Scanner) — fails on known CVEs.
- **Secret detection** (gitleaks, TruffleHog, GitHub secret scanning) — fails the build.
- **PR-template enforcement** — empty descriptions are rejected.
- **Branch protection** — direct pushes to `main` are blocked.
- **AI first-pass review** — `local-review` itself, or equivalent, leaves automated annotations *before* the human reviewer arrives.

Note: the automation layer is exactly what `local-review` is for many of its users. When users adopt it as a pre-commit hook or CI step, mechanical review issues are caught before the human reviewer even sees the PR.

---

## Code Review Checklist

A mental model, not a rigid template.

### Design
- [ ] Does the solution fit the codebase architecture?
- [ ] Is it over-engineered or solving future problems?
- [ ] Are there better patterns/libraries for this?
- [ ] Does it introduce tight coupling or hidden dependencies?

### Functionality
- [ ] Does the code do what it claims?
- [ ] Are edge cases handled (null, empty, boundary, very-large, unicode)?
- [ ] Will this work at scale?
- [ ] Are there race conditions, deadlocks, or retry-safety issues?

### Security
- [ ] Server-side authorization on every endpoint?
- [ ] Parameterized queries everywhere?
- [ ] No secrets in code, logs, or URLs?
- [ ] Input validation at the trust boundary?
- [ ] Output encoding for all rendered user content?
- [ ] No fail-open error handling?
- [ ] Dependency scan clean? (No hallucinated AI packages.)

### Performance
- [ ] No N+1 queries or blocking I/O on hot paths?
- [ ] Pagination on growing result sets?
- [ ] Caches bounded; no unbounded allocations?
- [ ] Algorithmic complexity appropriate?
- [ ] Resources released on every path including exceptions?

### Tests
- [ ] Tests cover happy path **and** failure modes?
- [ ] Would tests fail if the code breaks?
- [ ] Deterministic? No flaky tests?
- [ ] Coverage adequate for the risk level?

### Maintainability
- [ ] Can a teammate fix a bug here in two years?
- [ ] Functions/classes single-purpose?
- [ ] Names reveal intent?
- [ ] No clever code without a comment explaining why?

### AI-generated code (when applicable)
- [ ] Disclosed in the PR description?
- [ ] Edge cases / error paths actually handled?
- [ ] Imports and APIs verified to exist?
- [ ] Tests written from the requirement, not the implementation?

### Documentation
- [ ] Public API documented?
- [ ] README/docs updated if behavior changed?
- [ ] Comments explain why, not what?

---

## Reviewer Communication

### Tone

- Use **"we"** or **"this line"** instead of **"you"**.
- Ask questions: *"Could this handle the case where X?"*
- Explain reasoning: *"This could cause Y because Z."*
- Acknowledge good practices: *"Nice use of X pattern here."*
- Label nitpicks: *"Nit: consider renaming for clarity."*
- Avoid commands: *"Change this to X."*
- Avoid vague critique: *"This is messy."*

### Resolving disagreement

1. The author and reviewer try to converge in comments, citing principles or data.
2. If unresolved within a few rounds, **move to a 15-minute call**. Document the outcome as a final comment on the PR.
3. If still unresolved, escalate to a tech lead or code owner. **Don't let a PR rot.**
4. The principle: arguments based on **underlying principles** and **data** beat arguments based on personal preference. If both options are equally valid, defer to the author.

### Praise

Code review culture decays when only negative comments are posted. Call out clever solutions, clean tests, elegant naming. Use the `praise:` prefix.

---

## Anti-Patterns

**For reviewers**:
- ❌ **Rubber-stamping** — `LGTM` within 30 seconds on a >100-LOC PR. Meta's "Eyeball Time" metric exists to detect this.
- ❌ **The bikeshed** — 40 comments on a name, zero on the SQL injection.
- ❌ **The phantom reviewer** — adding 8 reviewers to dilute responsibility. More reviewers ≠ better review (Google's research).
- ❌ Style debates in PR threads — encode it in the linter, settle it once.
- ❌ Blocking PRs for out-of-scope improvements; file a follow-up issue instead.
- ❌ Performance-review-by-PR — using review comments to grade engineers. Code is reviewed, not the engineer.
- ❌ Velocity at the expense of comprehension — merging code you don't understand because the author is impatient. *You* are responsible for what you approve.
- ❌ Single-reviewer bus factor — only one person reviews a given area. Rotate.

**For authors**:
- ❌ **The 3,000-line "small refactor."** Split it.
- ❌ Mixing refactors with feature changes.
- ❌ Ignoring failing CI to "fix in a follow-up."
- ❌ Arguing in comments instead of jumping on a call.
- ❌ Taking feedback personally.
- ❌ The friday-evening merge — non-trivial deploys before a weekend or holiday.

---

## Language-Specific Supplements

This document provides cross-language principles. The prompt packs that ship with `local-review` apply these principles to specific languages:

- [Go](../internal/prompts/packs/go.md)
- [TypeScript / JavaScript](../internal/prompts/packs/typescript.md)
- [Python](../internal/prompts/packs/python.md)
- [Rust](../internal/prompts/packs/rust.md)

---

## Appendix A — PR Description Template

```markdown
## What
<!-- One paragraph on the change. A reviewer should be able to understand
     the scope without reading the diff. -->

## Why
<!-- Link the ticket. Describe the user/business problem and the chosen
     trade-off. If you considered alternative approaches, mention them. -->
Closes: <ISSUE-123>

## How it was tested
<!-- Unit tests added? Integration tests? Manual steps? Screenshots/GIF
     for UI changes. Load test result for performance work. -->

## Risk & rollout
<!-- Feature flag? Migration? Backward-compatibility? On-call note? -->

## Author checklist
- [ ] Self-reviewed the diff
- [ ] Tests added / updated; coverage not decreased
- [ ] Docs updated (README / ADR / public docs)
- [ ] No secrets, debug prints, or unrelated files
- [ ] Conventional Commit title
- [ ] CI green

## AI disclosure (if applicable)
- [ ] No AI used
- [ ] AI used for: <draft / refactor / tests / docs>
- [ ] Core prompt:
```

---

## Appendix B — The 60-Second Review

For when you have 5 minutes and 200 lines of diff.

1. Does the description tell me **what** and **why**?
2. Is the change **focused** — one concern only?
3. Is CI **green**?
4. Is the size **< 400 lines**?
5. Are there **tests** for the new behavior?
6. Any obvious **security** smells (SQL string concat, hard-coded secrets, missing auth)?
7. Any obvious **performance** smells (N+1, unbounded loop, missing index)?
8. Any **AI-generated** code? Apply the §7 lens.
9. Would I be **proud** of this code at 3 a.m. on call?
10. **LGTM** — or one specific, actionable, kindly-worded comment.

### The Author's 60-Second Pre-Flight

1. Did I **self-review** the diff?
2. **Tests** pass locally?
3. **Conventional Commit** title?
4. **What/Why/How tested** in description?
5. **Linked ticket**?
6. **Smallest** reasonable PR?
7. No **secrets**, no **debug prints**, no **unrelated files**?
8. **Screenshot/GIF** for UI changes?
9. **Deployment notes** for risky changes?
10. **Right reviewers** — the smallest set that covers the change?

---

## References

**Engineering practice**:
- Google, [Engineering Practices Documentation](https://google.github.io/eng-practices/) — Code Reviewer Guide and CL Author Guide.
- Winters, Manshreck, Wright, *Software Engineering at Google*, O'Reilly, 2020 — chapters 9 (Code Review) and 19 (Critique).
- Microsoft, [ISE Engineering Fundamentals Playbook](https://microsoft.github.io/code-with-engineering-playbook/code-reviews/).
- Microsoft / Greiler, *Code Reviews at Microsoft* — 900+ developer study.
- Meta Engineering, [*Move Faster, Wait Less: Improving Code Review Time at Meta*](https://engineering.fb.com/).
- Stripe, Shopify, Atlassian, GitLab, GitHub engineering blogs.

**Standards**:
- [OWASP Top 10:2025](https://owasp.org/Top10/2025/).
- [OWASP Application Security Verification Standard (ASVS) 5.0](https://owasp.org/www-project-application-security-verification-standard/).
- [Conventional Commits 1.0](https://conventionalcommits.org/).

**Empirical research**:
- SmartBear / Cisco, *Best Kept Secrets of Peer Code Review* — origin of the 200-400 LOC and 500-LOC/hour findings.
- Forsgren, Humble, Kim, *Accelerate* (2018) and the [DORA State of DevOps reports](https://dora.dev/).
- LinearB, *2026 Software Engineering Benchmarks Report* — AI-generated PR acceptance rates.

**The book canon**:
- Robert C. Martin, *Clean Code*; *Clean Architecture*.
- Hunt & Thomas, *The Pragmatic Programmer*.
- Steve McConnell, *Code Complete*.
- Michael Feathers, *Working Effectively with Legacy Code*.
- Martin Fowler, *Refactoring*.
- Martin Kleppmann, *Designing Data-Intensive Applications*.
- Beyer et al., *Site Reliability Engineering* (Google).
- Eric Evans, *Domain-Driven Design*.

---

**Last updated**: 2026-05-04
**Version**: 2.0

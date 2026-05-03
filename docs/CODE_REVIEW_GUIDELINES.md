# Code Review Guidelines

This document defines **best-in-class code review standards** for local-review, synthesized from Google's Engineering Practices, Microsoft's Engineering Playbook, and 2025 industry best practices.

---

## Philosophy

**Primary Goal**: Improve overall code health over time while maintaining development velocity.

**Core Principles**:
- **Honesty over politeness** — Be direct, specific, and useful
- **Signal over noise** — One sharp finding beats five vague ones
- **Context-aware** — Understand the business logic and constraints
- **Future-focused** — Help future developers understand this code

---

## Review Priorities (Ordered by Impact)

### 1. Correctness
**Goal**: Prevent bugs from reaching production.

**What to check**:
- ✅ Business logic implements requirements correctly
- ✅ Edge cases are handled (null/undefined, empty arrays, boundary values)
- ✅ Error handling doesn't swallow critical failures
- ✅ Race conditions in concurrent code
- ✅ Off-by-one errors in loops and array access
- ✅ Resource leaks (file handles, connections, memory)
- ✅ State mutations that could affect other parts of the system

**Critical patterns**:
- Unhandled promise rejections (JavaScript/TypeScript)
- Unchecked errors (Go)
- Null pointer dereferences
- Integer overflows in user-scaled operations
- Logic errors in conditional branches

---

### 2. Security
**Goal**: Prevent vulnerabilities and data exposure.

**OWASP 2025 Top Priorities**:
- ✅ **Injection vulnerabilities** — SQL, command, path traversal, XSS
- ✅ **Broken authentication** — session management, password policies, MFA
- ✅ **Security misconfiguration** — default credentials, exposed endpoints
- ✅ **Supply chain vulnerabilities** — untrusted dependencies (NEW in 2025)
- ✅ **Cryptographic failures** — weak algorithms, hardcoded keys
- ✅ **Insecure deserialization** — untrusted object instantiation
- ✅ **Missing authorization** — access control on sensitive operations
- ✅ **Insufficient logging** — audit trails for security events
- ✅ **Server-side request forgery (SSRF)**
- ✅ **Exceptional condition mishandling** — info leaks in errors (NEW in 2025)

**Data protection**:
- No secrets (API keys, passwords, tokens) in source code
- PII/PHI must be encrypted at rest and in transit
- Sensitive data must be scrubbed from logs
- Validate and sanitize ALL user inputs

**Dependency security**:
- No dependencies with known CVEs
- Verify package checksums and signatures
- Use lock files (package-lock.json, go.sum, requirements.txt)

---

### 3. Performance
**Goal**: Prevent scalability issues and resource exhaustion.

**What to check**:
- ✅ **Algorithmic complexity** — O(n²) or worse over user-scaled data
- ✅ **N+1 queries** — database calls in loops
- ✅ **Blocking I/O** — synchronous operations on hot paths
- ✅ **Unbounded allocations** — unlimited memory growth
- ✅ **Missing caching** — repeated expensive computations
- ✅ **Inefficient data structures** — wrong choice for access pattern
- ✅ **Missing pagination** — loading entire datasets into memory

**Database patterns**:
- SELECT N+1 queries (use JOINs or batching)
- Missing indexes on frequently queried columns
- Full table scans on large tables
- Transactions held open too long

**Frontend patterns**:
- Large bundle sizes without code splitting
- Missing React.memo / useMemo for expensive renders
- Unoptimized images or missing lazy loading
- Excessive re-renders from state mismanagement

---

### 4. Maintainability
**Goal**: Ensure code remains understandable and evolvable.

**Design quality**:
- ✅ **Single Responsibility Principle** — functions/classes do one thing
- ✅ **DRY violations** — meaningful duplication (not just copy-paste)
- ✅ **Leaky abstractions** — implementation details exposed in interfaces
- ✅ **God objects** — classes with too many responsibilities
- ✅ **Premature optimization** — complexity without measured need
- ✅ **Over-engineering** — solving hypothetical future problems
- ✅ **Dead code** — unreachable branches or unused exports

**Naming**:
- Variables/functions reveal intent without comments
- Avoid abbreviations unless industry-standard (HTTP, API, DB)
- Consistent naming conventions within the codebase
- Boolean variables start with `is`, `has`, `should`

**Comments**:
- Explain **WHY**, not **WHAT**
- Document non-obvious business rules
- Remove commented-out code (use git history)
- TODOs must have owners and issue links

**Function complexity**:
- Functions > 50 lines should be scrutinized for decomposition
- Cyclomatic complexity > 10 is a red flag
- Deep nesting (>3 levels) hurts readability

---

### 5. Testing
**Goal**: Ensure code is verifiable and changes don't break existing functionality.

**Test coverage**:
- ✅ New features include unit tests
- ✅ Bug fixes include regression tests
- ✅ Edge cases are tested (not just happy path)
- ✅ Tests would actually fail if the code breaks
- ✅ Integration tests for multi-component interactions
- ✅ E2E tests for critical user journeys

**Test quality**:
- Avoid testing implementation details
- Tests are independent (no shared state)
- Test names describe the scenario and expected outcome
- Mocks/stubs are realistic
- No flaky tests (random failures)

**Coverage thresholds**:
- Critical paths: 100% coverage
- Business logic: 80%+ coverage
- Utilities: 70%+ coverage
- UI components: integration tests preferred over unit tests

---

### 6. Style & Documentation
**Goal**: Maintain consistency and help onboarding.

**Style**:
- Code follows project style guide (linter enforced)
- Formatting is automated (Prettier, gofmt, Black)
- Language idioms are respected (e.g., Go receiver naming)

**Documentation**:
- Public APIs have doc comments (JSDoc, GoDoc, docstrings)
- README updated for new features/setup steps
- Architecture docs updated for design changes
- CHANGELOG updated for user-facing changes

**Only flag style issues when**:
- They actively obscure intent
- They violate team conventions
- They introduce inconsistency in touched files

---

## Review Process

### For Reviewers

**Before starting**:
1. Understand the PR's purpose (read description, linked issues)
2. Check that tests pass in CI
3. Pull the branch locally if needed for complex changes

**During review**:
1. **Read every line** — don't skim
2. **Follow logical flow** — start with entry points, not alphabetical file order
3. **Check context** — view surrounding code, not just the diff
4. **Run the code** — if the change is non-trivial
5. **Verify tests** — do they cover the changes?

**Communication style**:
- ✅ Use "we" or "this line" instead of "you"
- ✅ Ask questions: "Could this handle the case where X?"
- ✅ Explain reasoning: "This could cause Y because Z"
- ✅ Acknowledge good practices: "Nice use of X pattern here"
- ✅ Label nitpicks: "Nit: consider renaming for clarity"
- ❌ Avoid commands: "Change this to X"
- ❌ Avoid vague critique: "This is messy"

**Scope management**:
- Review **only the scope** of the PR
- File separate issues for pre-existing problems
- Don't block PRs for future improvements (file follow-up tickets)

**Response time**:
- Respond within **1 business day** (Google standard)
- For urgent fixes, respond within **1 hour**
- If you can't review fully, acknowledge receipt and provide ETA

---

### For Authors

**Before requesting review**:
- ✅ Self-review your own diff
- ✅ Run tests locally
- ✅ Run linter and fix style issues
- ✅ Write a clear PR description (what, why, how)
- ✅ Link related issues/tickets
- ✅ Keep PRs small (<400 lines when possible)

**During review**:
- Respond to all comments (even if just "Done")
- Ask clarifying questions if feedback is unclear
- Push back respectfully if you disagree (with reasoning)
- Mark conversations as resolved after addressing

**After approval**:
- Squash fixup commits before merging
- Ensure CI is green
- Merge promptly (don't leave approved PRs open)

---

## Severity Tiers

Use these consistently in reviews:

| Severity | Definition | Example | Action |
|----------|------------|---------|--------|
| **Critical** | Will break production, lose data, or create security hole | SQL injection, null pointer crash, exposed credentials | **Block merge** |
| **Major** | Likely bug, perf issue, or security concern | N+1 query, missing error handling, race condition | **Block merge** |
| **Warning** | Design/maintainability problem worth addressing | Over-engineering, leaky abstraction, complex function | **Strong suggestion** |
| **Info** | Context the author should know | Better pattern exists, potential future issue | **FYI only** |
| **Nit** | Cosmetic/style (also emitted by local-review when `min_severity: nit` is configured) | Naming preference, minor formatting | **Optional** |

---

## Code Review Checklist

Use this as a mental model (not a rigid template):

### Design
- [ ] Does the solution fit the codebase architecture?
- [ ] Is it over-engineered or solving future problems?
- [ ] Are there better patterns/libraries for this?
- [ ] Does it introduce tight coupling?

### Functionality
- [ ] Does the code do what it claims?
- [ ] Are edge cases handled?
- [ ] Will this work at scale?
- [ ] Are there race conditions or deadlocks?

### Security
- [ ] Is user input validated and sanitized?
- [ ] Are secrets kept out of source code?
- [ ] Is authentication/authorization correct?
- [ ] Are dependencies free of known CVEs?
- [ ] Is sensitive data encrypted?

### Performance
- [ ] Are there algorithmic inefficiencies?
- [ ] Are there N+1 queries or blocking I/O?
- [ ] Will this scale with user growth?
- [ ] Are resources released properly?

### Tests
- [ ] Do tests cover happy path and edge cases?
- [ ] Would tests fail if the code breaks?
- [ ] Are tests readable and maintainable?
- [ ] Is test coverage adequate for the risk level?

### Maintainability
- [ ] Can future developers understand this code?
- [ ] Are functions/classes single-purpose?
- [ ] Is there unnecessary duplication?
- [ ] Are names clear and consistent?

### Documentation
- [ ] Are complex decisions explained?
- [ ] Is public API documented?
- [ ] Is README/docs updated if needed?

---

## Anti-Patterns to Avoid

**For reviewers**:
- ❌ Nitpicking style when linters should handle it
- ❌ Bike-shedding (debating trivial details)
- ❌ Blocking PRs for out-of-scope improvements
- ❌ Being inconsistent with past reviews
- ❌ Rewriting code in your personal style

**For authors**:
- ❌ Making PRs too large (>500 lines)
- ❌ Mixing refactors with feature changes
- ❌ Ignoring CI failures
- ❌ Arguing in comments instead of talking
- ❌ Taking feedback personally

---

## Language-Specific Supplements

This document provides cross-language principles. See also:

- [Go Review Guidelines](../internal/prompts/packs/go.md)
- [TypeScript/JavaScript Guidelines](../internal/prompts/packs/typescript.md)
- [Python Guidelines](../internal/prompts/packs/python.md)

---

## References

- [Google Engineering Practices](https://google.github.io/eng-practices/review/)
- [Microsoft Code Review Guidance](https://microsoft.github.io/code-with-engineering-playbook/code-reviews/)
- [OWASP Top 10 2025](https://owasp.org/www-project-top-ten/)
- [Effective Code Reviews](https://www.swarmia.com/blog/a-complete-guide-to-code-reviews/)

---

**Last Updated**: 2026-05-03
**Version**: 1.0

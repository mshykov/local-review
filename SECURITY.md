# Security Policy

## Supported Versions

| Version | Supported                            |
| ------- | ------------------------------------ |
| 0.7.x   | ✅ active line — fixes shipped        |
| 0.6.x   | ⚠️ exception-only — see policy below |
| < 0.6   | ❌ unsupported                        |

**Active line (0.7.x):** every reported vulnerability gets a fix in a
patch release; the "Security Update Process" below applies as written.

**Exception-only (0.6.x):** a fix is *not* guaranteed. The maintainer
may backport at their discretion when the impact is severe (remote
code execution, credential disclosure, supply-chain compromise) AND
the patch is small. Anything else: upgrade to 0.7.x.

**Unsupported:** report against the last active line. We won't
investigate or patch.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, please report them via email to: [maksym.shykov@gmail.com]

You should receive a response within 48 hours. If for some reason you do not, please follow up to ensure we received your original message.

Please include the following information:

- Type of issue (e.g., buffer overflow, SQL injection, cross-site scripting, etc.)
- Full paths of source file(s) related to the manifestation of the issue
- The location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit it

## Preferred Languages

We prefer all communications to be in English.

## Security Update Process

1. The security issue is received and assigned a primary handler
2. The problem is confirmed and affected versions are determined
3. Code is audited to find any similar problems
4. Fixes are prepared for all supported releases
5. A security advisory is published with the release

## Disclosure Policy

We follow a coordinated disclosure model:

- Security issues are fixed in private
- A new release is published with the fix
- A security advisory is published 24 hours after the release
- Credit is given to the reporter (unless they wish to remain anonymous)

# Open-Source Launch Checklist

## ✅ Files Created (Done!)
- [x] LICENSE (MIT)
- [x] README.md
- [x] CONTRIBUTING.md
- [x] CODE_OF_CONDUCT.md
- [x] SECURITY.md
- [x] Issue templates (bug report, feature request)
- [x] Pull request template
- [x] CI/CD workflows

## 🔧 GitHub Repository Settings

### 1. General Settings
Go to **Settings** → **General**:

- [ ] **Description**: "Local, BYOK code review for any language. No SaaS, no telemetry."
- [ ] **Website**: (your docs site if you create one)
- [ ] **Topics/Tags**: Add these tags for discoverability:
  - `code-review`
  - `llm`
  - `ai`
  - `developer-tools`
  - `golang`
  - `cli`
  - `openai`
  - `anthropic`
  - `local-first`
  - `privacy`
  - `multi-llm`

- [ ] **Features**:
  - ✅ Issues
  - ✅ Discussions (optional, recommended for Q&A)
  - ❌ Projects (unless you want a roadmap board)
  - ❌ Wiki (use docs/ folder instead - see below)
  - ✅ Sponsorships (if you want GitHub Sponsors)

- [ ] **Pull Requests**:
  - ✅ Allow squash merging
  - ✅ Allow merge commits
  - ❌ Allow rebase merging (to keep history clean)
  - ✅ Automatically delete head branches
  - ✅ Allow auto-merge

### 2. Branch Protection Rules
Go to **Settings** → **Branches** → **Add rule**:

**Branch name pattern**: `main`

- [ ] **Require a pull request before merging**
  - ✅ Require approvals: **1** (start with 1, increase as team grows)
  - ✅ Dismiss stale pull request approvals when new commits are pushed
  - ❌ Require review from Code Owners (enable later if needed)
  - ✅ Require approval of the most recent reviewable push

- [ ] **Require status checks to pass before merging**
  - ✅ Require branches to be up to date before merging
  - Required checks (these come from .github/workflows/ci.yml):
    - ✅ `test` (go test)
    - ✅ `build` (go build)
    - ✅ `lint` (go vet, gofmt)

- [ ] **Require conversation resolution before merging** ✅

- [ ] **Require signed commits** (optional, recommended for security)
  - ❌ Start with this off, enable later if needed

- [ ] **Require linear history** ✅
  - This enforces squash or rebase merges (keeps main clean)

- [ ] **Do not allow bypassing the above settings** ✅
  - Even admins should follow the rules (you can always disable temporarily)

- [ ] **Restrict pushes that create matching branches** ✅
  - Only allow specific people/teams to push to main

### 3. Collaborators & Teams

**For now (solo maintainer)**:
- You're the only one with write access
- Anyone can fork and submit PRs

**When you add collaborators**:
- Go to **Settings** → **Collaborators**
- Add trusted contributors with **Write** access
- Create a CODEOWNERS file (optional):

```
# .github/CODEOWNERS
* @mshykov

# Multi-LLM subsystem
/internal/multi/ @mshykov
/cmd/local-review/multi.go @mshykov
```

### 4. Security

Go to **Settings** → **Security**:

- [ ] **Private vulnerability reporting** ✅ Enable
  - This lets people report security issues privately
  - Update SECURITY.md with your preferred contact email

- [ ] **Dependabot alerts** ✅ Enable
  - Automatically checks for vulnerable dependencies

- [ ] **Dependabot security updates** ✅ Enable
  - Auto-creates PRs to fix security issues

- [ ] **Dependabot version updates** (optional)
  - Auto-creates PRs for dependency updates
  - Can be noisy, but useful for keeping Go modules updated

### 5. Actions

Go to **Settings** → **Actions** → **General**:

- [ ] **Actions permissions**:
  - ✅ Allow all actions and reusable workflows

- [ ] **Workflow permissions**:
  - ✅ Read and write permissions
  - ✅ Allow GitHub Actions to create and approve pull requests

## 📚 Documentation Strategy: README vs Wiki vs /docs

### ✅ Use `/docs` folder (NOT GitHub Wiki)

**Why?**
- **Version controlled**: Changes go through PRs
- **Searchable**: Works with GitHub search and grep
- **Local**: Contributors can read docs offline
- **Consistent**: Follows the same workflow as code

**Structure**:
```
docs/
  prompt-packs.md           # How to write/override prompt packs ✅ exists
  configuration.md          # Deep dive on .local-review.yml (to add)
  troubleshooting.md        # Common issues (to add)
  development.md            # Dev environment setup (to add)
```

**What goes in README.md**:
- Quick overview
- Installation
- Quick start (5 minutes to first review)
- Links to `/docs` for deep dives

**When to use GitHub Wiki**:
- ❌ Never (it's not version-controlled with the repo)
- Exception: Community-driven content like "Awesome local-review" lists

**When to use GitHub Discussions**:
- ✅ Q&A ("How do I configure Ollama?")
- ✅ Show and tell ("Here's my prompt pack for Rust")
- ✅ Roadmap discussions
- ✅ RFCs (Request for Comments on big features)

### Example: Enable Discussions

Go to **Settings** → **General** → **Features** → Enable **Discussions**

Create categories:
- **Q&A**: Help and questions
- **Show and tell**: User configs, prompt packs, integrations
- **Ideas**: Feature requests (alternative to GitHub Issues)
- **General**: Everything else

## 🏷️ Labels for Issues/PRs

Go to **Issues** → **Labels**. Create these:

**Type**:
- `bug` (red) - Something isn't working
- `enhancement` (blue) - New feature or request
- `documentation` (light blue) - Improvements or additions to docs
- `question` (purple) - Further information is requested

**Area**:
- `area: cli` - CLI interface
- `area: multi-llm` - Multi-LLM features
- `area: prompts` - Prompt packs
- `area: config` - Configuration
- `area: performance` - Performance improvements

**Status**:
- `good first issue` (green) - Good for newcomers
- `help wanted` (green) - Extra attention is needed
- `wontfix` (white) - This will not be worked on
- `duplicate` (gray) - This issue or PR already exists

**Priority** (optional):
- `priority: critical` - Blocking issue
- `priority: high` - Important
- `priority: medium` - Normal
- `priority: low` - Nice to have

## 🚀 Release Process

### Current (manual):
1. Update version in code
2. Tag: `git tag v0.1.0`
3. Push: `git push origin v0.1.0`
4. GitHub Actions builds binaries and creates a release

### Recommended improvements:

#### A. Add version command
Update `cmd/local-review/version.go`:
```go
const Version = "0.1.0" // Update this for each release
```

#### B. Add CHANGELOG.md
Track changes between versions:
```markdown
# Changelog

## [0.1.0] - 2024-XX-XX

### Added
- Multi-LLM support (claude, gemini, codex)
- `local-review multi` command
- `local-review doctor` command
- Intelligent review merging

### Fixed
- Path traversal vulnerabilities
- Timeout handling in git operations

## [0.0.1] - 2024-XX-XX

### Added
- Initial release
- Single-LLM mode
- Prompt packs for Go, TypeScript, Python
```

#### C. Create pre-release checklist
Before tagging a release:
- [ ] Update `Version` constant
- [ ] Update CHANGELOG.md
- [ ] Run full test suite: `go test ./...`
- [ ] Test build: `go build ./cmd/local-review`
- [ ] Manual smoke test: `./local-review staged`
- [ ] Update README.md if features changed
- [ ] Tag and push

## 🤝 Community Building

### Day 1:
- [ ] Post to relevant subreddits (r/golang, r/MachineLearning, r/programming)
- [ ] Post to Hacker News (Show HN: local-review)
- [ ] Tweet about it
- [ ] Post on LinkedIn

### Week 1:
- [ ] Respond to all issues within 24 hours
- [ ] Merge first external PR quickly (builds momentum)
- [ ] Add top 3 requested features to roadmap

### Month 1:
- [ ] Write a blog post: "Building local-review: Lessons learned"
- [ ] Create a demo video (2-3 minutes)
- [ ] Reach out to developer tool newsletters

## 📊 Analytics (Optional)

**GitHub Insights** (built-in):
- Go to **Insights** tab to see:
  - Contributors
  - Traffic (views, clones)
  - Popular content
  - Stars over time

**Add badges to README.md**:
```markdown
[![GitHub stars](https://img.shields.io/github/stars/mshykov/local-review)](https://github.com/mshykov/local-review/stargazers)
[![GitHub issues](https://img.shields.io/github/issues/mshykov/local-review)](https://github.com/mshykov/local-review/issues)
[![GitHub license](https://img.shields.io/github/license/mshykov/local-review)](https://github.com/mshykov/local-review/blob/main/LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/mshykov/local-review)](https://goreportcard.com/report/github.com/mshykov/local-review)
```

## 🎯 First 100 Stars Strategy

1. **Make it easy to try**: One-line install that works
2. **Show, don't tell**: GIF/video in README
3. **Solve a real pain point**: You already do this (local AI code review)
4. **Respond fast**: Be active in issues/PRs
5. **Document everything**: Lower the barrier to contribution
6. **Share in the right places**: Dev tool communities, not general tech

---

**NOTE**: Don't try to do everything at once. Start with:
1. Branch protection on `main`
2. Respond to first issues
3. Merge first PR
4. Build from there!

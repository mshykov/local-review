# Distribution Strategy for local-review

## 🎯 Goal
Make it easy for developers to install `local-review` on macOS, Linux, and Windows.

---

## 📦 Distribution Methods (Ranked by Priority)

### 1. ✅ Homebrew (macOS & Linux) - **TOP PRIORITY**

**Why**:
- Most popular for macOS developer tools
- Works on Linux too
- Users expect `brew install` for CLI tools
- Auto-updates
- Easy to maintain

**How**:

#### A. Create a Homebrew formula

When you release v0.1.0, create a PR to Homebrew:

```ruby
# Formula/local-review.rb
class LocalReview < Formula
  desc "Local, BYOK code review for any language"
  homepage "https://github.com/mshykov/local-review"
  url "https://github.com/mshykov/local-review/archive/v0.1.0.tar.gz"
  sha256 "..." # Homebrew will calculate this
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/local-review"
  end

  test do
    assert_match "local-review", shell_output("#{bin}/local-review version")
  end
end
```

#### B. OR: Use Homebrew tap (faster for early releases)

Create your own tap before getting into official Homebrew:

```bash
# Create a new repo: homebrew-tap
# Add file: local-review.rb

# Users install via:
brew tap mshykov/tap
brew install local-review
```

This is faster to set up and you control updates.

**Steps**:
1. Create repo: `homebrew-tap`
2. Add formula: `local-review.rb`
3. Users run:
   ```bash
   brew tap mshykov/tap
   brew install local-review
   ```

#### C. Update release workflow to create bottles

Add to `.github/workflows/release.yml`:
```yaml
- name: Create Homebrew bottles
  run: |
    # Build for macOS and Linux
    # Upload bottles to GitHub Release
```

**Timeline**: Do this for v0.1.0 launch.

---

### 2. ✅ Go Install - **ALREADY WORKS**

**Current**:
```bash
go install github.com/mshykov/local-review/cmd/local-review@latest
```

**Pros**:
- Zero setup (you already have it)
- Works everywhere Go works
- Always up-to-date

**Cons**:
- Requires Go installed
- Not ideal for non-Go developers

**Action**: Keep this. Add it prominently in README.

---

### 3. ✅ Shell Script Installer (All platforms) - **YOU HAVE THIS**

**Current**:
```bash
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

**Your install.sh should**:
- Detect OS (macOS, Linux, Windows/WSL)
- Detect architecture (amd64, arm64)
- Download pre-built binary from GitHub Releases
- Install to `/usr/local/bin` or `~/.local/bin`
- Make it executable

**Pros**:
- Works everywhere (macOS, Linux, Windows/WSL)
- One command
- No dependencies

**Cons**:
- "Curl to bash" is scary for security-conscious users
- Requires manual updates (no auto-update)

**Action**: Keep this. It's your universal installer.

---

### 4. ❌ NPM - **DON'T DO THIS**

**You asked about npm/npnm**:

**Why NOT to use npm**:
- `local-review` is a Go binary, not a Node.js package
- Adding Node.js as a dependency defeats the "single static binary" value prop
- npm is for JavaScript packages, not general CLI tools
- Would require users to install Node just to run a Go binary

**Exception**: If you want to publish to npm as a *wrapper*:
```javascript
// package.json wrapper that downloads the Go binary
{
  "name": "local-review",
  "bin": {
    "local-review": "./bin/local-review"
  },
  "scripts": {
    "postinstall": "node download-binary.js"
  }
}
```

But this is **NOT recommended** because:
- Adds complexity
- npm users can use Homebrew or install.sh instead
- Your target audience (Go/Python/any devs) don't expect npm

**Verdict**: Skip npm entirely.

---

### 5. ⏳ Scoop (Windows) - **MEDIUM PRIORITY**

**Why**:
- Homebrew for Windows
- Popular with Windows developers
- Easy to maintain

**How**:

#### Create a Scoop manifest

```json
{
  "version": "0.1.0",
  "description": "Local, BYOK code review for any language",
  "homepage": "https://github.com/mshykov/local-review",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "https://github.com/mshykov/local-review/releases/download/v0.1.0/local-review_0.1.0_windows_amd64.zip",
      "hash": "sha256:...",
      "bin": "local-review.exe"
    }
  },
  "checkver": "github",
  "autoupdate": {
    "architecture": {
      "64bit": {
        "url": "https://github.com/mshykov/local-review/releases/download/v$version/local-review_$version_windows_amd64.zip"
      }
    }
  }
}
```

**Installation**:
```powershell
scoop bucket add mshykov https://github.com/mshykov/scoop-bucket
scoop install local-review
```

**Timeline**: Add this after 100+ stars or if Windows users request it.

---

### 6. ⏳ Chocolatey (Windows) - **LOW PRIORITY**

**Similar to Scoop but**:
- More complex to set up
- Requires package approval process
- Scoop is preferred by developers

**Verdict**: Only if you get many Windows user requests. Scoop is easier.

---

### 7. ⏳ APT/YUM/DNF (Linux distros) - **LOW PRIORITY**

**Debian/Ubuntu (apt)**:
- Create `.deb` packages
- Host your own apt repository OR submit to official repos
- Very complex process

**RedHat/Fedora (yum/dnf)**:
- Create `.rpm` packages
- Similar complexity

**Why NOT to do this early**:
- High maintenance burden
- Most Linux devs use Homebrew now (`brew works on Linux`)
- Or they use `go install` or `install.sh`

**When to consider**:
- If you have 1000+ stars
- If enterprise Linux users request it
- If you want to be in Ubuntu/Fedora official repos

**Alternative**: Use a PPA (Personal Package Archive) for Ubuntu:
- Easier than official repos
- Users add with `apt-add-repository`

---

### 8. ✅ AUR (Arch Linux) - **COMMUNITY DRIVEN**

**Arch User Repository**:
- Community can create AUR packages
- You don't need to maintain it yourself
- Arch users expect AUR

**Action**:
- Wait for an Arch user to create it
- Or create a basic `PKGBUILD` and submit to AUR

**Timeline**: After 50+ stars, check if anyone created an AUR package.

---

### 9. ⏳ asdf - **NICE TO HAVE**

**asdf-vm** (version manager for multiple languages):
- Users can manage `local-review` versions
- Popular with polyglot developers

**Plugin needed**:
```bash
asdf plugin add local-review https://github.com/mshykov/asdf-local-review
```

**Effort**: Medium. Only do this if requested.

---

### 10. ✅ Docker (Optional) - **FOR CI/CD USE**

**Why**:
- Some users want to run in CI without installing
- Useful for GitHub Actions, GitLab CI, etc.

**Example Dockerfile**:
```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o local-review ./cmd/local-review

FROM alpine:latest
RUN apk --no-cache add ca-certificates git
COPY --from=builder /app/local-review /usr/local/bin/
ENTRYPOINT ["local-review"]
```

**Publish to**:
- Docker Hub: `docker.io/mshykov/local-review`
- GitHub Container Registry: `ghcr.io/mshykov/local-review`

**Usage**:
```bash
docker run --rm -v $(pwd):/repo mshykov/local-review staged
```

**Timeline**: Add this when users ask for CI integration.

---

## 📊 Recommended Distribution Matrix

| Platform | Method | Priority | When to Add |
|----------|--------|----------|-------------|
| macOS | Homebrew tap | 🔴 **CRITICAL** | v0.1.0 launch |
| macOS | Go install | ✅ Already works | Keep |
| macOS | Shell script | ✅ Already works | Keep |
| Linux | Homebrew | 🔴 **CRITICAL** | v0.1.0 launch |
| Linux | Go install | ✅ Already works | Keep |
| Linux | Shell script | ✅ Already works | Keep |
| Linux | AUR (Arch) | 🟡 Community | Wait for contributor |
| Windows | Shell script (WSL/Git Bash) | ✅ Already works | Keep |
| Windows | Scoop | 🟡 Nice to have | After 100 stars |
| Windows | Chocolatey | ⚪ Low priority | Only if requested |
| All | GitHub Releases | ✅ Already works | Keep |
| All | Docker | 🟡 CI/CD | When CI users request |

---

## 🚀 Action Plan for v0.1.0 Launch

### Week 1: Essential
1. ✅ Ensure GitHub Releases workflow works
2. ✅ Test install.sh on macOS, Linux, Windows (WSL)
3. ✅ Create Homebrew tap (your own repo)
4. ✅ Update README with all install methods

### Week 2-4: Based on demand
- If Windows users complain → Add Scoop
- If Arch users request → Create AUR package
- If CI users ask → Add Docker image
- If 100+ stars → Submit to official Homebrew

### Month 2+: Scale
- Official Homebrew formula
- Windows installer (if needed)
- Linux package repositories (if enterprise demand)

---

## 🎯 Your Install Section Should Look Like

```markdown
## Install

### macOS / Linux

**Homebrew** (recommended):
```sh
brew tap mshykov/tap
brew install local-review
```

**One-line installer**:
```sh
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

**Go install** (if you have Go):
```sh
go install github.com/mshykov/local-review/cmd/local-review@latest
```

### Windows

**Scoop** (coming soon):
```powershell
scoop bucket add mshykov https://github.com/mshykov/scoop-bucket
scoop install local-review
```

**Git Bash / WSL**:
```sh
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

### All Platforms

**Download binary** from [Releases](https://github.com/mshykov/local-review/releases).
```

---

## 📌 Summary

**DO NOW**:
1. ✅ Homebrew tap (create `homebrew-tap` repo)
2. ✅ Test install.sh works everywhere
3. ✅ Ensure GitHub Releases builds for all platforms

**DON'T DO**:
- ❌ npm/npnm (wrong tool for Go binaries)
- ❌ Linux distro packages (too early, too complex)
- ❌ Chocolatey (Scoop is better for devs)

**DO LATER (when users ask)**:
- Scoop (Windows)
- Docker (CI/CD)
- Official Homebrew (after proving popularity)
- AUR (Arch Linux)

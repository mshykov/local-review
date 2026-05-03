# Homebrew & Auto-Release Setup Checklist

Quick checklist to set up automated releases and Homebrew distribution for `local-review`.

**Full details**: See [RELEASE_PROCESS.md](./RELEASE_PROCESS.md)

---

## ✅ Phase 1: Fix Missing Commands (Required First)

### 1.1 Add Version Command
**Create**: `cmd/local-review/version.go`
```bash
# Content provided in RELEASE_PROCESS.md
```

### 1.2 Add Config Command
**Create**: `cmd/local-review/config.go`
```bash
# Content provided in RELEASE_PROCESS.md
```

### 1.3 Test Locally
```bash
go build -ldflags "-X main.version=v0.1.0" -o local-review ./cmd/local-review
./local-review version     # Should print: local-review v0.1.0
./local-review config      # Should print current config
```

---

## ✅ Phase 2: Create Homebrew Tap

### 2.1 Create Repository
- [ ] Go to https://github.com/new
- [ ] Repository name: `homebrew-tap`
- [ ] Public repository
- [ ] Initialize with README
- [ ] Create repository

### 2.2 Add Formula
- [ ] In `homebrew-tap` repo, create `Formula/` directory
- [ ] Create `Formula/local-review.rb`
- [ ] Copy content from RELEASE_PROCESS.md
- [ ] Commit and push

---

## ✅ Phase 3: Set Up GitHub Secrets

### 3.1 Create Personal Access Token
- [ ] Go to https://github.com/settings/tokens
- [ ] Click "Generate new token (classic)"
- [ ] Name: `HOMEBREW_TAP_TOKEN`
- [ ] Select scope: `repo` (full control)
- [ ] Generate token
- [ ] **Copy token** (you won't see it again!)

### 3.2 Add Secret to local-review Repo
- [ ] Go to https://github.com/mshykov/local-review/settings/secrets/actions
- [ ] Click "New repository secret"
- [ ] Name: `TAP_GITHUB_TOKEN`
- [ ] Value: paste your token
- [ ] Add secret

---

## ✅ Phase 4: Add Workflows

### 4.1 Auto-Release Workflow
- [ ] Create `.github/workflows/auto-release.yml`
- [ ] Copy content from RELEASE_PROCESS.md
- [ ] Commit to a branch

### 4.2 Formula Update Workflow
- [ ] Create `.github/workflows/update-homebrew-formula.yml`
- [ ] Copy content from RELEASE_PROCESS.md
- [ ] Commit to same branch

---

## ✅ Phase 5: Create First Release

### 5.1 Option A: Manual Tag (Recommended for First Release)
```bash
# Make sure all changes are committed
git add .
git commit -m "Add automated release system"
git push origin main

# Create first tag
git tag v0.1.0
git push origin v0.1.0

# Watch GitHub Actions: https://github.com/mshykov/local-review/actions
```

### 5.2 Option B: Use Auto-Release Workflow
```bash
# After workflows are merged to main
# Go to Actions → Auto Release → Run workflow
# Select "patch" → Run
```

---

## ✅ Phase 6: Verify Everything Works

### 6.1 Check GitHub Release
- [ ] Go to https://github.com/mshykov/local-review/releases
- [ ] Verify v0.1.0 release exists
- [ ] Verify binaries are attached:
  - `local-review_darwin_amd64.tar.gz`
  - `local-review_darwin_arm64.tar.gz`
  - `local-review_linux_amd64.tar.gz`
  - `local-review_linux_arm64.tar.gz`
  - `local-review_windows_amd64.zip`

### 6.2 Check Homebrew Formula
- [ ] Go to https://github.com/mshykov/homebrew-tap
- [ ] Verify `Formula/local-review.rb` was updated
- [ ] Check that version = "0.1.0"
- [ ] Check that SHA256 placeholders were replaced

### 6.3 Test Installation
```bash
# Test Homebrew install
brew tap mshykov/tap
brew install local-review
local-review version

# Test shell installer
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

---

## ✅ Phase 7: Set Up PR Labels

### 7.1 Create Labels
Go to https://github.com/mshykov/local-review/labels and create:

| Label | Color | Description |
|-------|-------|-------------|
| `patch` | `#0e8a16` | Bug fixes, backward compatible |
| `minor` | `#fbca04` | New features, backward compatible |
| `major` | `#d73a4a` | Breaking changes |

---

## 🎉 Done!

### Daily Workflow (After Setup)

**For Contributors**:
1. Create PR as usual
2. Add ONE version label: `patch`, `minor`, or `major`
3. Merge PR

**Automatic Process**:
1. Auto-release workflow detects label
2. Creates new version tag
3. Release workflow builds binaries
4. GitHub Release is published
5. Homebrew formula updates automatically
6. Users can `brew upgrade local-review`

### Manual Override

If you need to create a release manually:
```bash
git tag v0.2.0
git push origin v0.2.0
# Everything else happens automatically
```

---

## Troubleshooting

### Release didn't trigger
- Check workflow permissions: Settings → Actions → General → Workflow permissions → "Read and write permissions"
- Verify `contents: write` in workflow file

### Formula update failed
- Check `TAP_GITHUB_TOKEN` secret exists
- Verify token has `repo` scope
- Check homebrew-tap repository exists

### Homebrew install fails
- Formula checksums may be wrong
- Manually trigger formula update workflow
- Or wait for next release (checksums will auto-update)

---

## Next Steps

After completing this setup, consider:
- [ ] Add to README: "Install via Homebrew: `brew install mshykov/tap/local-review`"
- [ ] Update CONTRIBUTING.md with PR labeling guidelines
- [ ] Add release badge to README
- [ ] Consider setting up Homebrew core submission (once stable)

---

**Questions?** See [RELEASE_PROCESS.md](./RELEASE_PROCESS.md) for full documentation.

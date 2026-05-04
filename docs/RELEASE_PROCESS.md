# Release Process & Homebrew Distribution

This document explains how to set up automated releases and Homebrew distribution for `local-review`.

## Overview

**Current State**:
- ✅ Release workflow exists (`.github/workflows/release.yml`)
- ✅ Manual tag triggers build and release
- ✅ Shell installer script exists (`install.sh`)
- ❌ No automatic version bumping
- ❌ No Homebrew formula yet

**Target State**:
- ✅ Automatic semantic versioning on merge to main
- ✅ Automated GitHub Releases with binaries
- ✅ Homebrew tap for easy `brew install local-review`
- ✅ Automatic formula updates on new releases

---

## Architecture

### 1. Versioning Strategy (Semantic Versioning)

We use **semantic versioning**: `vMAJOR.MINOR.PATCH`

- **MAJOR** (v2.0.0): Breaking changes
- **MINOR** (v1.1.0): New features, backward compatible
- **PATCH** (v1.0.1): Bug fixes, backward compatible

**How version is determined**:
- Automatically from **PR labels** or **conventional commits**
- Manual override possible via workflow dispatch

### Label cheat sheet

Apply these labels to a PR **only when you actually want a release on merge**. The pipeline ships when the `release` label is present *and* one of `major` / `minor` / `patch` (default: `patch`).

| Label | Use it for | Don't use it for |
|---|---|---|
| `release` | Mandatory on any PR you want to ship | Doc-only PRs, refactors, internal tweaks |
| `major` | Breaking CLI flag changes, removed commands, config schema rewrites | Anything that doesn't break existing user setups |
| `minor` | A new prompt pack, a new command, a new provider in core, a new flag | Bug fixes, polish, internal refactors |
| `patch` | Bug fixes, doc/config updates, dependency bumps, test additions | New user-visible functionality |

**Common mistakes to avoid:**

- ❌ **`minor` for "I want to ship this, the change feels notable"** → bumps the published version more than necessary. v0.1.1 → v0.2.0 implies a feature; if the PR is "fix typo in error message," use `patch`.
- ❌ **`release` on every merged PR** → ships a tag for every doc/CI/refactor PR. Wastes version numbers and inflates release frequency. Only label `release` when you specifically want to publish.
- ❌ **No bump label, just `release`** → defaults to `patch`. If you wanted a feature release, you'd skip a number. Always pair `release` with the explicit bump label.

**0.x special case:** while you're pre-1.0, "minor" still doesn't promise stability — anything can break. But the version number is still the public-facing signal of momentum, so bumping it casually inflates expectations. Default to `patch` unless the PR adds new user-visible functionality.

### 2. Release Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  1. PR Merged to main                                           │
│     - Labeled: major/minor/patch OR                             │
│     - Commit msg: feat!/feat/fix                                │
└────────────────────┬────────────────────────────────────────────┘
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│  2. Auto-Release Workflow Triggers                              │
│     - Reads last tag (e.g., v0.1.0)                             │
│     - Calculates next version (e.g., v0.2.0)                    │
│     - Creates git tag                                           │
└────────────────────┬────────────────────────────────────────────┘
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│  3. Release Workflow Triggers (on tag push)                     │
│     - Builds binaries for all platforms                         │
│     - Creates GitHub Release                                    │
│     - Uploads artifacts                                         │
└────────────────────┬────────────────────────────────────────────┘
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│  4. Homebrew Formula Update Workflow Triggers                   │
│     - Updates formula with new version & checksums              │
│     - Commits to homebrew-tap repository                        │
│     - Users can now: brew upgrade local-review                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Setup Instructions

### Step 1: Add Missing Commands to CLI

The CLI already references `versionCmd()` and `configCmd()` but they're not implemented.

**Create**: `cmd/local-review/version.go`

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("local-review %s\n", version)
		},
	}
}
```

**Create**: `cmd/local-review/config.go`

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show resolved configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			if err := enc.Encode(cfg); err != nil {
				return fmt.Errorf("encode config: %w", err)
			}
			return nil
		},
	}
}
```

### Step 2: Create Auto-Release Workflow

**Create**: `.github/workflows/auto-release.yml`

```yaml
name: Auto Release

on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      bump:
        description: 'Version bump type'
        required: true
        default: 'patch'
        type: choice
        options:
          - major
          - minor
          - patch

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # Need full history for tag detection

      - name: Determine version bump
        id: bump
        run: |
          # Get latest tag, default to v0.0.0 if none
          latest_tag=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
          echo "Latest tag: $latest_tag"

          # Parse version
          version="${latest_tag#v}"
          IFS='.' read -r major minor patch <<< "$version"

          # Determine bump type from PR labels or workflow input
          bump_type="${{ inputs.bump }}"
          if [ -z "$bump_type" ]; then
            # Check PR labels (if this is a merge commit)
            pr_number=$(git log -1 --pretty=%B | grep -oP '#\K\d+' || echo "")
            if [ -n "$pr_number" ]; then
              labels=$(gh pr view "$pr_number" --json labels --jq '.labels[].name' || echo "")
              if echo "$labels" | grep -q "major"; then
                bump_type="major"
              elif echo "$labels" | grep -q "minor"; then
                bump_type="minor"
              else
                bump_type="patch"
              fi
            else
              # Default to patch if no PR
              bump_type="patch"
            fi
          fi

          # Bump version
          case "$bump_type" in
            major)
              major=$((major + 1))
              minor=0
              patch=0
              ;;
            minor)
              minor=$((minor + 1))
              patch=0
              ;;
            patch)
              patch=$((patch + 1))
              ;;
          esac

          new_version="v${major}.${minor}.${patch}"
          echo "New version: $new_version"
          echo "version=$new_version" >> $GITHUB_OUTPUT
        env:
          GH_TOKEN: ${{ github.token }}

      - name: Create and push tag
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git tag -a "${{ steps.bump.outputs.version }}" -m "Release ${{ steps.bump.outputs.version }}"
          git push origin "${{ steps.bump.outputs.version }}"
```

### Step 3: Create Homebrew Tap Repository

1. **Create a new GitHub repository**: `homebrew-tap`
   - Repository name: `homebrew-tap`
   - Full path: `github.com/mshykov/homebrew-tap`
   - Public repository
   - Initialize with README

2. **Create the formula file** in that repository:

**File**: `Formula/local-review.rb`

```ruby
class LocalReview < Formula
  desc "Local, BYOK AI code reviewer for your git diffs"
  homepage "https://github.com/mshykov/local-review"
  version "0.1.0"  # This will be auto-updated by workflow

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/mshykov/local-review/releases/download/v#{version}/local-review_darwin_amd64.tar.gz"
      sha256 "PLACEHOLDER_SHA256_DARWIN_AMD64"  # Auto-updated
    elsif Hardware::CPU.arm?
      url "https://github.com/mshykov/local-review/releases/download/v#{version}/local-review_darwin_arm64.tar.gz"
      sha256 "PLACEHOLDER_SHA256_DARWIN_ARM64"  # Auto-updated
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/mshykov/local-review/releases/download/v#{version}/local-review_linux_amd64.tar.gz"
      sha256 "PLACEHOLDER_SHA256_LINUX_AMD64"  # Auto-updated
    elsif Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/mshykov/local-review/releases/download/v#{version}/local-review_linux_arm64.tar.gz"
      sha256 "PLACEHOLDER_SHA256_LINUX_ARM64"  # Auto-updated
    end
  end

  def install
    bin.install "local-review"
  end

  test do
    assert_match "local-review", shell_output("#{bin}/local-review version")
  end
end
```

### Step 4: Create Formula Update Workflow

**Add to**: `.github/workflows/update-homebrew-formula.yml`

```yaml
name: Update Homebrew Formula

on:
  release:
    types: [published]

permissions:
  contents: write

jobs:
  update-formula:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout homebrew-tap
        uses: actions/checkout@v4
        with:
          repository: mshykov/homebrew-tap
          token: ${{ secrets.TAP_GITHUB_TOKEN }}  # Need PAT with repo scope

      - name: Download release assets
        run: |
          version="${{ github.event.release.tag_name }}"
          version="${version#v}"  # Remove 'v' prefix

          # Download all assets
          base_url="https://github.com/mshykov/local-review/releases/download/${{ github.event.release.tag_name }}"

          curl -fsSL "${base_url}/local-review_darwin_amd64.tar.gz" -o darwin_amd64.tar.gz
          curl -fsSL "${base_url}/local-review_darwin_arm64.tar.gz" -o darwin_arm64.tar.gz
          curl -fsSL "${base_url}/local-review_linux_amd64.tar.gz" -o linux_amd64.tar.gz
          curl -fsSL "${base_url}/local-review_linux_arm64.tar.gz" -o linux_arm64.tar.gz

          # Calculate SHA256 checksums
          sha_darwin_amd64=$(shasum -a 256 darwin_amd64.tar.gz | awk '{print $1}')
          sha_darwin_arm64=$(shasum -a 256 darwin_arm64.tar.gz | awk '{print $1}')
          sha_linux_amd64=$(shasum -a 256 linux_amd64.tar.gz | awk '{print $1}')
          sha_linux_arm64=$(shasum -a 256 linux_arm64.tar.gz | awk '{print $1}')

          # Update formula
          sed -i "s/version \".*\"/version \"${version}\"/" Formula/local-review.rb
          sed -i "s/PLACEHOLDER_SHA256_DARWIN_AMD64/${sha_darwin_amd64}/" Formula/local-review.rb
          sed -i "s/PLACEHOLDER_SHA256_DARWIN_ARM64/${sha_darwin_arm64}/" Formula/local-review.rb
          sed -i "s/PLACEHOLDER_SHA256_LINUX_AMD64/${sha_linux_amd64}/" Formula/local-review.rb
          sed -i "s/PLACEHOLDER_SHA256_LINUX_ARM64/${sha_linux_arm64}/" Formula/local-review.rb

      - name: Commit and push
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git add Formula/local-review.rb
          git commit -m "Update local-review to ${{ github.event.release.tag_name }}"
          git push
```

### Step 5: Create Personal Access Token (PAT)

For the formula update workflow to work, you need a GitHub PAT:

1. Go to https://github.com/settings/tokens
2. Click "Generate new token (classic)"
3. Name: `HOMEBREW_TAP_TOKEN`
4. Scopes: `repo` (all)
5. Click "Generate token"
6. Copy the token
7. Go to `local-review` repository → Settings → Secrets → Actions
8. Add new secret: `TAP_GITHUB_TOKEN` = your token

---

## Usage

### For Users (Installation)

**Option 1: Homebrew (Recommended)**
```bash
# Add the tap
brew tap mshykov/tap

# Install
brew install local-review

# Upgrade
brew upgrade local-review
```

**Option 2: Shell Installer**
```bash
curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
```

**Option 3: Go Install**
```bash
go install github.com/mshykov/local-review/cmd/local-review@latest
```

### For Maintainers (Releasing)

**Automatic (Recommended)**:
1. Merge PR to `main` with label:
   - `patch` label → v0.1.1
   - `minor` label → v0.2.0
   - `major` label → v1.0.0
2. Auto-release workflow creates tag
3. Release workflow builds & publishes
4. Formula updates automatically

**Manual**:
```bash
# Trigger manual release via GitHub UI
# Actions → Auto Release → Run workflow → Select bump type
```

**Emergency Manual Tag**:
```bash
git tag v0.1.1
git push origin v0.1.1
# Release workflow will trigger automatically
```

---

## Version Labeling Convention

When creating PRs, add ONE of these labels:
- `major` - Breaking changes (v2.0.0)
- `minor` - New features (v1.1.0)
- `patch` - Bug fixes (v1.0.1)

If no label is present, defaults to `patch`.

---

## Testing the Setup

### Test 1: Version Command
```bash
go build -ldflags "-X main.version=v0.1.0" -o local-review ./cmd/local-review
./local-review version
# Should output: local-review v0.1.0
```

### Test 2: Manual Release
```bash
git tag v0.1.0
git push origin v0.1.0
# Check GitHub Actions to see release workflow trigger
```

### Test 3: Homebrew Install (After first release)
```bash
brew tap mshykov/tap
brew install local-review
local-review version
```

---

## Troubleshooting

### Q: Auto-release workflow didn't create a tag
**A**: Check if:
- The PR had a version label (major/minor/patch)
- The workflow has `contents: write` permission
- You're pushing to `main` branch

### Q: Formula update failed
**A**: Check if:
- `TAP_GITHUB_TOKEN` secret is set correctly
- Token has `repo` scope
- homebrew-tap repository exists and is accessible

### Q: Homebrew install fails with checksum mismatch
**A**:
- Formula wasn't updated properly
- Manually run the formula update workflow
- Or update checksums manually in Formula/local-review.rb

---

## References

- [Semantic Versioning](https://semver.org/)
- [Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)
- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [GoReleaser](https://goreleaser.com/) (Alternative automated approach)

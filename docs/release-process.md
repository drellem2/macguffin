# Release process

How a macguffin release is cut, and how to tell that it was. Versioning itself
(the tag *is* the version; nothing in source is bumped) is described at the top
of [`CHANGELOG.md`](../CHANGELOG.md).

**release.yml is no longer required (mg-38246).** GitHub Actions jobs on the
drellem2 account began to be refused on 2026-11-04 (billing). A pushed tag may
still fire `.github/workflows/release.yml`, or it may not. Either way the release
can be published from this box with `scripts/release-local.sh`, and a release
counts as cut **only when the GitHub Release exists with its assets** (step 3),
never because a workflow run went green.

## 1. Prepare

Move the `[Unreleased]` section of `CHANGELOG.md` under a `[X.Y.Z] — date`
heading and merge that to `main` through the refinery as usual.

## 2. Tag

From a checkout of the commit to release:

```bash
./release.sh X.Y.Z      # creates and pushes the vX.Y.Z tag; refuses if it exists
```

Pushing the tag fires `release.yml` **if Actions runs for this repo at all**. If
no release appears (step 3), publish it locally (step 2a).

### 2a. Publishing without GitHub Actions

`scripts/release-local.sh` runs the repo's `.goreleaser.yml` on this box against
the tag that is already pushed, and publishes through the GitHub API with
`$GH_TOKEN`. Every build is `CGO_ENABLED=0`, so the linux, darwin and freebsd
binaries cross-compile from darwin.

```bash
git fetch origin --tags
git worktree add --detach /tmp/mg-release-vX.Y.Z vX.Y.Z   # HEAD = the tag, clean tree
cd /tmp/mg-release-vX.Y.Z
scripts/release-local.sh --dry-run vX.Y.Z   # every check + snapshot build; publishes nothing
scripts/release-local.sh vX.Y.Z             # publish
cd - && git worktree remove /tmp/mg-release-vX.Y.Z
```

The script never pushes a tag. It refuses, and publishes nothing, when:

- HEAD is not exactly `git rev-parse "vX.Y.Z^{}"`.
- The tag is missing locally or from `origin`, or `origin`'s tag names a
  different commit.
- The work tree is dirty.
- `go.mod` has a `replace` directive.
- A GitHub Release for the tag **already exists**. This is what stops a double
  publish, including after `release.yml` has published.
- A `release.yml` run for the tag is still queued or in progress.
- `gh` cannot say whether the release exists. Only the text "release not found"
  counts as absent.
- Neither `GH_TOKEN` nor `GITHUB_TOKEN` is set.

**Homebrew cask.** `.goreleaser.yml` generates the cask but does not push it to
`drellem2/homebrew-tap` (`skip_upload: true`, mg-b0f0a). `release.yml` kept the
cask as its `homebrew-cask` workflow artifact; a local publish leaves it at
`dist/homebrew/Casks/mg.rb` in the release worktree. Copy it out before removing
the worktree if the tap is to be updated by hand.

**Provenance.** macguffin has no SLSA provenance workflow, so a local publish
ships exactly what a `release.yml` publish did.

## 3. Acceptance: the release exists with its assets

```bash
gh release view vX.Y.Z --repo drellem2/macguffin --json assets -q '.assets[].name' | sort
```

The cut is published when that lists these six assets (as v0.4.0 did):

```
checksums.txt
mg_darwin_amd64
mg_darwin_arm64
mg_freebsd_amd64
mg_linux_amd64
mg_linux_arm64
```

`gh run list --workflow=release.yml` is **not** the check. A run that was
refused (Actions billing) or never started leaves no release, and a run can go
red after the release and its assets have already published (v0.4.0, mg-b0f0a).
If the assets are missing, publish with `scripts/release-local.sh` (step 2a) and
re-run the check.

`install.sh` resolves `releases/latest`, so it picks up the new release as soon
as this check passes.

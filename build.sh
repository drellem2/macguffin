#!/bin/sh
# Build and install the mg binary to $GOPATH/bin
set -e

cd "$(dirname "$0")"

# Derive a version string for a source build, using the same ldflag names
# goreleaser stamps at release time.
#
# A source build is not a release, but it still has to be ORDERABLE against one:
# "is this host's mg newer than v0.3.0" needs an answer, and the `dev` sentinel
# gave none. The obvious candidate -- raw `git describe` output, e.g.
# v0.3.0-11-g0bbe682 -- is worse than no answer at all. A hyphen segment is a
# semver PRE-RELEASE, so that string sorts OLDER than the very tag it is eleven
# commits ahead of, and it is valid enough that a comparison would parse it and
# trust the wrong result. So the patch is bumped and the distance moved into the
# pre-release instead:
#
#     v0.3.0 + 11 commits  ->  v0.3.1-dev.11+g0bbe682
#
# which sorts after v0.3.0 and before v0.3.1 -- correct in both directions.
#
# This does not reintroduce the version constant that bc23153 deleted: nothing
# here is hand-maintained, the value is derived from the tag at build time. The
# tag is still the version.
#
# git is asked directly rather than relying on the VCS metadata the Go toolchain
# embeds by itself, because that stamping attributes a build made from a linked
# git worktree to the ENCLOSING repository (mg-b7fe). `git -C` is correct inside
# a worktree; the toolchain's guess is not, and it does not warn.
#
# Prints nothing when a version cannot be derived -- no git, not a repo, no tags,
# or a tag that is not a plain vN.N.N. The caller then builds unstamped and mg
# reports `dev`, exactly as it always has.
derive_version() {
    dir="${1:-.}"

    command -v git >/dev/null 2>&1 || return 0
    git -C "$dir" rev-parse --git-dir >/dev/null 2>&1 || return 0

    desc=$(git -C "$dir" describe --tags --long 2>/dev/null) || return 0

    # --long always yields <tag>-<distance>-g<sha>, even when distance is 0.
    tag=${desc%-*-*}
    rest=${desc#"$tag"-}
    dist=${rest%-*}
    gsha=${rest#*-}

    # Only plain vN.N.N tags can have their patch bumped meaningfully. Anything
    # else (a pre-release tag, a non-version tag) falls back to unstamped rather
    # than guessing at semantics it does not have.
    printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || return 0
    printf '%s' "$dist" | grep -Eq '^[0-9]+$' || return 0

    dirty=
    if [ -n "$(git -C "$dir" status --porcelain 2>/dev/null)" ]; then
        dirty=1
    fi

    # Built exactly on a tag, with nothing uncommitted: report the tag itself, so
    # a source build of a release commit is indistinguishable from the released
    # binary instead of inventing a -dev.0 string no release ever produced.
    if [ "$dist" -eq 0 ] && [ -z "$dirty" ]; then
        printf '%s\n' "$tag"
        return 0
    fi

    # A dirty tree takes the -dev path even at distance 0: it carries changes the
    # tag does not, so it must not claim to BE the tag. The dirty marker itself
    # goes in the build metadata after '+', which semver ignores for precedence
    # -- a dirty build is not a different version, it is an untrustworthy one.
    meta="$gsha"
    [ -n "$dirty" ] && meta="${meta}.dirty"

    base=${tag#v}
    maj=${base%%.*}
    tmp=${base#*.}
    min=${tmp%%.*}
    pat=${tmp##*.}

    printf 'v%s.%s.%s-dev.%s+%s\n' "$maj" "$min" "$((pat + 1))" "$dist" "$meta"
}

main() {
    # Check formatting
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
        echo "Error: the following files are not formatted with gofmt:"
        echo "$unformatted"
        echo ""
        echo "Run: gofmt -w ."
        exit 1
    fi

    version=$(derive_version .)

    # go install honours GOBIN over GOPATH/bin; report where it actually went.
    dest="${GOBIN:-$(go env GOPATH)/bin}"

    if [ -n "$version" ]; then
        commit=$(git rev-parse --short HEAD)
        date=$(git log -1 --format=%cs)
        go install -ldflags \
            "-X main.version=${version} -X main.commit=${commit} -X main.date=${date}" \
            ./cmd/mg
        echo "Installed: ${dest}/mg (${version})"
    else
        go install ./cmd/mg
        echo "Installed: ${dest}/mg (unstamped; reports 'dev')"
    fi
}

[ -n "${MG_BUILD_SKIP_MAIN:-}" ] || main "$@"

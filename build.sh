#!/bin/sh
# build.sh -- check formatting and compile mg into ./bin/mg.
#
# build.sh does NOT `go install` by default. It runs in polecat worktrees and as
# the refinery's merge gate, on the branch being merged, so an install here
# replaced the fleet's live ~/go/bin/mg with an unmerged -- and, from a gate's
# worktree, often ".dirty" -- branch build. It happened even when the branch
# then FAILED its gate, because the install ran first (mg-e42de). Installing is
# something you do on purpose, from a checkout you mean to deploy:
#
#   ./build.sh              # gofmt check + build into ./bin/mg
#   ./build.sh --install    # ...and also `go install ./cmd/mg` into GOBIN
#
# Environment:
#   MG_BUILD_DIR   Output directory for the built binary. Default: ./bin
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
# The `pat + 1` is NOT a prediction that v0.3.1 is the next release, and must not
# be "corrected" into one. It only has to land the build after the tag it
# descends from and before whatever ships next, which it does regardless of the
# next release's number: v0.3.1-dev.11+g0bbe682 sorts older than v0.4.0 and older
# than v1.0.0, as well as older than v0.3.1. Any bump would do; the patch is
# simply the smallest.
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
# CONTRACT: prints the version and returns 0 on success; prints NOTHING and
# returns NON-ZERO when no version can be derived -- no git, not a repo, no tags,
# or a tag that is not a plain vN.N.N. Failure is signalled by the exit code, not
# only by empty output, so that `if derive_version .; then` is correct rather
# than silently taking the success branch on an empty result. Deriving nothing is
# a normal outcome, not an error: the caller builds unstamped and mg reports
# `dev` exactly as it always has, so the call site suppresses the status
# deliberately.
derive_version() {
    dir="${1:-.}"

    command -v git >/dev/null 2>&1 || return 1
    git -C "$dir" rev-parse --git-dir >/dev/null 2>&1 || return 1

    desc=$(git -C "$dir" describe --tags --long 2>/dev/null) || return 1

    # --long always yields <tag>-<distance>-g<sha>, even when distance is 0.
    tag=${desc%-*-*}
    rest=${desc#"$tag"-}
    dist=${rest%-*}
    gsha=${rest#*-}

    # Only plain vN.N.N tags can have their patch bumped meaningfully. Anything
    # else (a pre-release tag, a non-version tag) falls back to unstamped rather
    # than guessing at semantics it does not have.
    printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || return 1
    printf '%s' "$dist" | grep -Eq '^[0-9]+$' || return 1

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

usage() {
    cat <<'EOF'
Usage:
  ./build.sh              # gofmt check + build into ./bin/mg
  ./build.sh --install    # ...and also `go install ./cmd/mg` into GOBIN

Environment:
  MG_BUILD_DIR   Output directory for the built binary. Default: ./bin
EOF
}

main() {
    do_install=0
    for arg in "$@"; do
        case "$arg" in
            --install) do_install=1 ;;
            -h|--help) usage; return 0 ;;
            *) echo "build.sh: unknown argument: $arg" >&2; usage >&2; exit 2 ;;
        esac
    done

    # Check formatting
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
        echo "Error: the following files are not formatted with gofmt:"
        echo "$unformatted"
        echo ""
        echo "Run: gofmt -w ."
        exit 1
    fi

    # Deriving nothing is a normal outcome (tarball, no tags), so the non-zero
    # status is suppressed on purpose rather than tripping `set -e`.
    version=$(derive_version .) || version=

    if [ -n "$version" ]; then
        commit=$(git rev-parse --short HEAD)
        date=$(git log -1 --format=%cs)
        ldflags="-X main.version=${version} -X main.commit=${commit} -X main.date=${date}"
        label="$version"
    else
        ldflags=
        label="unstamped; reports 'dev'"
    fi

    out="${MG_BUILD_DIR:-./bin}"
    mkdir -p "$out"
    go build -ldflags "$ldflags" -o "${out}/mg" ./cmd/mg
    echo "Built: ${out}/mg (${label})"

    [ "$do_install" = 1 ] || return 0

    # go install honours GOBIN over GOPATH/bin; report where it actually went.
    dest="${GOBIN:-$(go env GOPATH)/bin}"
    go install -ldflags "$ldflags" ./cmd/mg
    echo "Installed: ${dest}/mg (${label})"
}

[ -n "${MG_BUILD_SKIP_MAIN:-}" ] || main "$@"

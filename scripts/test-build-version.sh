#!/bin/sh
# Test derive_version() in build.sh against synthetic git repos, without
# building or installing anything. Sources build.sh with MG_BUILD_SKIP_MAIN so
# the function can be called directly.
#
# The property under test is ORDERING, not cosmetics: a source build has to sort
# correctly against the release it is ahead of. Raw `git describe` output does
# not (v0.3.0-11-g0bbe682 is a semver pre-release of v0.3.0 and sorts BEFORE it),
# which is the bug these tests exist to pin.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_SCRIPT="${SCRIPT_DIR}/build.sh"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export MG_BUILD_SKIP_MAIN=1
# shellcheck disable=SC1090
. "$BUILD_SCRIPT"

fail() { echo "FAIL: $*" >&2; exit 1; }

# A repo with one commit; $1 is the directory, $2 an optional tag.
mkrepo() {
    mkdir -p "$1"
    git -C "$1" init -q
    git -C "$1" config user.email test@example.com
    git -C "$1" config user.name test
    git -C "$1" config commit.gpgsign false
    echo one > "$1/f.txt"
    git -C "$1" add -A
    git -C "$1" commit -qm one
    [ -n "$2" ] && git -C "$1" tag "$2"
    return 0
}

commit_more() {
    n=$2
    i=0
    while [ "$i" -lt "$n" ]; do
        echo "$i" >> "$1/f.txt"
        git -C "$1" add -A
        git -C "$1" commit -qm "more $i"
        i=$((i + 1))
    done
}

echo "=== Test: clean tree exactly on a tag reports the bare tag ==="
repo="${tmpdir}/on-tag"
mkrepo "$repo" v0.3.0
got=$(derive_version "$repo")
[ "$got" = "v0.3.0" ] || fail "expected v0.3.0, got '$got'"
echo "  PASS"

echo "=== Test: commits past a tag bump the PATCH and use a -dev pre-release ==="
repo="${tmpdir}/ahead"
mkrepo "$repo" v0.3.0
commit_more "$repo" 11
got=$(derive_version "$repo")
sha=$(git -C "$repo" rev-parse --short HEAD)
[ "$got" = "v0.3.1-dev.11+g${sha}" ] || fail "expected v0.3.1-dev.11+g${sha}, got '$got'"
echo "  PASS"

echo "=== Test: the derived version sorts NEWER than the tag it is ahead of ==="
# The whole point. Guarded with Go's own semver implementation rather than a
# hand-rolled comparison, and asserted against raw `git describe` output too so
# the regression this prevents is visible in the test itself.
repo="${tmpdir}/ordering"
mkrepo "$repo" v0.3.0
commit_more "$repo" 11
derived=$(derive_version "$repo")
describe=$(git -C "$repo" describe --tags 2>/dev/null)
cmpdir="${tmpdir}/semvercmp"
mkdir -p "$cmpdir"
cat > "${cmpdir}/main.go" <<'GOEOF'
package main

import (
	"fmt"
	"os"

	"golang.org/x/mod/semver"
)

func main() {
	fmt.Println(semver.Compare(os.Args[1], os.Args[2]))
}
GOEOF
cat > "${cmpdir}/go.mod" <<'GOEOF'
module semvercmp

go 1.24
GOEOF
if (cd "$cmpdir" && GOFLAGS=-mod=mod go get golang.org/x/mod/semver >/dev/null 2>&1); then
    got=$(cd "$cmpdir" && go run . "$derived" v0.3.0)
    [ "$got" = "1" ] || fail "derived '$derived' should sort NEWER than v0.3.0 (got compare=$got)"
    got=$(cd "$cmpdir" && go run . "$derived" v0.3.1)
    [ "$got" = "-1" ] || fail "derived '$derived' should sort OLDER than v0.3.1 (got compare=$got)"
    # The regression being prevented: raw describe output sorts the wrong way.
    got=$(cd "$cmpdir" && go run . "$describe" v0.3.0)
    [ "$got" = "-1" ] || fail "expected raw describe '$describe' to sort OLDER than v0.3.0 (the bug); got compare=$got"
    echo "  PASS (and confirmed raw describe '$describe' sorts the wrong way)"
else
    echo "  SKIP (golang.org/x/mod unavailable offline)"
fi

echo "=== Test: a dirty tree is marked, and never claims to BE the tag ==="
repo="${tmpdir}/dirty"
mkrepo "$repo" v0.3.0
echo scratch >> "$repo/f.txt"
got=$(derive_version "$repo")
sha=$(git -C "$repo" rev-parse --short HEAD)
[ "$got" = "v0.3.1-dev.0+g${sha}.dirty" ] || fail "expected v0.3.1-dev.0+g${sha}.dirty, got '$got'"
echo "  PASS"

echo "=== Test: untagged repo derives nothing (caller builds unstamped) ==="
repo="${tmpdir}/untagged"
mkrepo "$repo"
got=$(derive_version "$repo")
[ -z "$got" ] || fail "expected empty for untagged repo, got '$got'"
echo "  PASS"

echo "=== Test: non-repo derives nothing ==="
repo="${tmpdir}/plain"
mkdir -p "$repo"
got=$(derive_version "$repo")
[ -z "$got" ] || fail "expected empty for non-repo, got '$got'"
echo "  PASS"

echo "=== Test: a non-vN.N.N tag derives nothing rather than guessing ==="
repo="${tmpdir}/oddtag"
mkrepo "$repo" v0.3.0-rc1
commit_more "$repo" 2
got=$(derive_version "$repo")
[ -z "$got" ] || fail "expected empty for pre-release tag, got '$got'"
echo "  PASS"

echo "=== Test: a linked worktree derives ITS OWN version, not the enclosing repo's ==="
# mg-b7fe: Go's automatic VCS stamping gets this exact case wrong, attributing
# the build to the enclosing repository. Asking git directly is what makes this
# correct, so it is pinned.
outer="${tmpdir}/outer"
mkrepo "$outer" v9.9.9
inner="${outer}/inner"
mkrepo "$inner" v0.3.0
commit_more "$inner" 3
git -C "$inner" branch -q wt
wt="${outer}/wtree"
git -C "$inner" worktree add -q "$wt" wt
[ -f "${wt}/.git" ] || fail "expected .git to be a FILE in a linked worktree"
got=$(derive_version "$wt")
sha=$(git -C "$wt" rev-parse --short HEAD)
[ "$got" = "v0.3.1-dev.3+g${sha}" ] || fail "worktree should derive from its own repo; got '$got'"
case "$got" in
    *9.9.9*) fail "derived version leaked the ENCLOSING repo's tag: $got" ;;
esac
echo "  PASS"

echo ""
echo "All build-version tests passed."

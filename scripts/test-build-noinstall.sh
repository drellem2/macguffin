#!/bin/sh
# ./build.sh must build into the repo and install NOTHING unless asked to.
#
# build.sh runs as the refinery's merge gate on the branch being merged, and in
# every polecat worktree. When it ran `go install`, each of those runs replaced
# the fleet's live ~/go/bin/mg with an unmerged branch build -- even for a branch
# that then failed its gate (mg-e42de). Installing is now `--install`, opt-in.
#
# GOBIN points at a scratch directory for every run below, so a regression here
# lands in the fixture and fails the test instead of reinstalling the live mg.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/testtmp.sh
. "${SCRIPT_DIR}/scripts/lib/testtmp.sh"
tmpdir="$(testtmp_dir test-build-noinstall)" || exit 1
trap 'testtmp_remove "$tmpdir"' EXIT INT TERM HUP

fail() { echo "FAIL: $*" >&2; exit 1; }

gobin="${tmpdir}/gobin"
mkdir -p "$gobin"

echo "=== Test: ./build.sh builds into MG_BUILD_DIR and installs nothing ==="
GOBIN="$gobin" MG_BUILD_DIR="${tmpdir}/out" sh "${SCRIPT_DIR}/build.sh" >"${tmpdir}/log" 2>&1 \
    || { cat "${tmpdir}/log" >&2; fail "build.sh exited non-zero"; }
[ -x "${tmpdir}/out/mg" ] || fail "no binary at \$MG_BUILD_DIR/mg"
"${tmpdir}/out/mg" --version >/dev/null 2>&1 || fail "built binary does not run"
[ -z "$(ls -A "$gobin")" ] || fail "build.sh without --install wrote into GOBIN: $(ls -A "$gobin")"
grep -q '^Installed:' "${tmpdir}/log" && fail "build.sh without --install claims it installed"
echo "  PASS"

echo "=== Test: ./build.sh --install also installs into GOBIN (positive control) ==="
GOBIN="$gobin" MG_BUILD_DIR="${tmpdir}/out2" sh "${SCRIPT_DIR}/build.sh" --install >"${tmpdir}/log" 2>&1 \
    || { cat "${tmpdir}/log" >&2; fail "build.sh --install exited non-zero"; }
[ -x "${gobin}/mg" ] || fail "--install did not put mg in GOBIN"
[ -x "${tmpdir}/out2/mg" ] || fail "--install skipped the ./bin build"
echo "  PASS"

echo "=== Test: an unknown argument is refused, not ignored ==="
set +e
GOBIN="$gobin" MG_BUILD_DIR="${tmpdir}/out3" sh "${SCRIPT_DIR}/build.sh" --instal >/dev/null 2>&1
rc=$?
set -e
[ "$rc" -eq 2 ] || fail "misspelled flag exited $rc, want 2"
[ ! -e "${tmpdir}/out3/mg" ] || fail "misspelled flag still built"
echo "  PASS"

echo "All build.sh no-install tests passed."

#!/bin/sh
# Guard: nothing in this repo may recursively delete the real user home or the
# live mg store under it.
#
# WHY. On 2026-09-27 a polecat ran scripts/e2e_milestones_test.sh by hand. Its
# clean() helper recursively removed the default store under the invoking
# user's home — every work item and every mailbox on the machine, with no
# backup — and scripts/event_test.sh carried the identical line (mg-9a40).
# Those scripts now run against a scratch HOME and MG_ROOT (see
# scripts/lib/scratchstore.sh). This guard is what stops the line coming back:
# ./test.sh runs it, so the refinery gate refuses any branch that reintroduces
# it, in a script, a Makefile, a Go test or a doc example alike.
#
# WHAT IS FLAGGED (see live_rm_scan for the exact expressions)
#
#   - a recursive rm (any flag spelling carrying r/R, or --recursive) whose
#     argument starts with a tilde, $HOME or ${HOME...}, or names .macguffin;
#   - find rooted at a tilde or $HOME that -deletes;
#   - RemoveAll / rmtree / rmSync / rimraf whose argument mentions .macguffin,
#     the home directory (UserHomeDir, Getenv("HOME"), expanduser, homedir), or
#     DefaultRoot().
#
# A path under $HOME is flagged even where the script has already pointed HOME
# at a temp dir: the scanner cannot tell the two apart, and a scratch tree has a
# variable of its own ($SCRATCHSTORE_DIR, $tmpdir) that is the right thing to
# delete. What it does NOT catch is indirection — a variable assigned the live
# path on one line and removed on another. That is named here rather than left
# quiet; scratchstore_assert_safe is the runtime half that covers it for the
# suites that use a store.
#
# NO EXAMPLES IN THIS FILE. The scan covers this file too, so the positive
# controls below are assembled at runtime from split strings.
#
# USAGE
#
#   sh scripts/test-no-live-store-rm.sh          # self-test, then scan the repo
#   sh scripts/test-no-live-store-rm.sh DIR      # self-test, then scan DIR
#
# Exit 0 = clean. Exit 1 = a hit (printed as file:line:text) or a self-test
# failure.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
REPO_ROOT="$SCRIPT_DIR"
PASS=0
FAIL=0

# shellcheck source=lib/testtmp.sh
. "${REPO_ROOT}/scripts/lib/testtmp.sh"
# shellcheck source=lib/scratchstore.sh
. "${REPO_ROOT}/scripts/lib/scratchstore.sh"

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1" >&2; }

WORK="$(testtmp_dir no-live-store-rm)" || exit 1
trap 'testtmp_remove "$WORK"' EXIT INT TERM HUP

# The expressions. Kept in variables so the self-test and the real scan run the
# identical instrument.
_B='(^|[^[:alnum:]_.-])'
_RMFLAGS='rm([[:space:]]+-[^[:space:]]*)*[[:space:]]+(-[[:alpha:]]*[rR][[:alpha:]]*|--recursive)([[:space:]]+-[^[:space:]]*)*[[:space:]]+'
_HOMEARG='["'"'"']?(~|\$HOME|\$\{HOME)'
_STOREARG='[^[:space:];|&]*\.macguffin'
RE_RM_HOME="${_B}${_RMFLAGS}${_HOMEARG}"
RE_RM_STORE="${_B}${_RMFLAGS}${_STOREARG}"
RE_FIND_HOME="${_B}find[[:space:]]+${_HOMEARG}[^|;]*-delete"
RE_API='(RemoveAll|rmtree|rmSync|rimraf)[[:space:]]*\(.*(\.macguffin|UserHomeDir|Getenv\("HOME"\)|expanduser|homedir\(|DefaultRoot\()'

# live_rm_scan DIR prints every hit as path:line:text and returns 0 iff there
# was at least one. In a git tree it reads tracked plus untracked-not-ignored
# files, so a new file is guarded before it is committed and build output is
# not; anywhere else it reads every regular file. -I skips binaries.
live_rm_scan() {
    _dir=$1
    _out="$WORK/scan.$$.out"
    if git -C "$_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1 &&
        [ "$(cd "$_dir" && pwd -P)" = "$(cd "$(git -C "$_dir" rev-parse --show-toplevel)" && pwd -P)" ]; then
        (cd "$_dir" && git ls-files -z -co --exclude-standard |
            xargs -0 grep -nIE -e "$RE_RM_HOME" -e "$RE_RM_STORE" -e "$RE_FIND_HOME" -e "$RE_API" -- \
            >"$_out") || :
    else
        (cd "$_dir" && find . -type f -print0 |
            xargs -0 grep -nIE -e "$RE_RM_HOME" -e "$RE_RM_STORE" -e "$RE_FIND_HOME" -e "$RE_API" -- \
            >"$_out") || :
    fi
    if [ -s "$_out" ]; then
        cat "$_out"
        return 0
    fi
    return 1
}

echo "=== Self-test: the scanner fires on every form it claims to catch ==="
# POSITIVE CONTROL. A scan that finds nothing is only evidence if the same
# instrument, pointed at a known hit, finds it. Each case is written to a file
# of its own and scanned alone, so one case firing cannot cover for another.
# "r%sm" and friends split the words so this file does not match itself.
R='r''m'
n=0
while IFS= read -r tmpl; do
    [ -n "$tmpl" ] || continue
    n=$((n + 1))
    d="$WORK/pos.$n"
    mkdir -p "$d"
    # shellcheck disable=SC2059
    printf "$tmpl\n" "$R" >"$d/case"
    if live_rm_scan "$d" >/dev/null; then
        pass "fires: $(cat "$d/case")"
    else
        fail "does NOT fire: $(cat "$d/case")"
    fi
done <<'EOF'
clean() { %s -rf ~/.macguffin; }
%s -rf $HOME/.macguffin
%s -rf "$HOME/.macguffin"
%s -rf "${HOME}/.macguffin"
%s -fr ~/.macguffin/work
%s -r -f ~
%s -Rf -- "$HOME"
%s --recursive --force ~/.macguffin
%s -rf /Users/someone/.macguffin
	%s -rf ~
EOF
# Non-shell forms, and find. Assembled from halves for the same reason.
n=$((n + 1)); d="$WORK/pos.$n"; mkdir -p "$d"
printf '\tos.Remove%s(filepath.Join(home, ".macguffin"))\n' 'All' >"$d/case.go"
live_rm_scan "$d" >/dev/null && pass "fires: Go removal of the store" || fail "does NOT fire: Go removal of the store"
n=$((n + 1)); d="$WORK/pos.$n"; mkdir -p "$d"
printf 'shutil.rm%s(os.path.expanduser("~/.macguffin"))\n' 'tree' >"$d/case.py"
live_rm_scan "$d" >/dev/null && pass "fires: Python rmtree of the store" || fail "does NOT fire: Python rmtree of the store"
n=$((n + 1)); d="$WORK/pos.$n"; mkdir -p "$d"
printf 'fi%s ~/.macguffin -type f -delete\n' 'nd' >"$d/case"
live_rm_scan "$d" >/dev/null && pass "fires: find ... -delete under home" || fail "does NOT fire: find ... -delete under home"

# The pre-fix scripts, when history is available: the guard must reject the
# exact files that deleted the store. Skipped (and said so) in a shallow clone.
PREFIX=8e012fd
if git -C "$REPO_ROOT" cat-file -e "${PREFIX}^{commit}" 2>/dev/null; then
    d="$WORK/prefix"
    mkdir -p "$d"
    git -C "$REPO_ROOT" show "${PREFIX}:scripts/e2e_milestones_test.sh" >"$d/e2e_milestones_test.sh"
    git -C "$REPO_ROOT" show "${PREFIX}:scripts/event_test.sh" >"$d/event_test.sh"
    hits=$(live_rm_scan "$d" | wc -l | tr -d ' ')
    [ "$hits" -eq 2 ] && pass "fires on both pre-fix scripts ($hits hits)" ||
        fail "expected 2 hits on the pre-fix scripts, got $hits"
else
    echo "  SKIP: pre-fix commit $PREFIX not in this clone (shallow?) — synthetic cases above still ran"
fi

echo "=== Self-test: the scanner does not fire on the safe forms ==="
d="$WORK/neg"
mkdir -p "$d"
cat >"$d/case" <<'EOF'
trap 'rm -rf "$tmpdir"' EXIT INT TERM HUP
rm -rf "$SCRATCHSTORE_DIR"
rm -f ~/.config/something.lock
confirm -rf ~/ok
testtmp_remove "$MG_ROOT"
os.RemoveAll(dir)
echo "never write rm -rf of the home"
EOF
if out=$(live_rm_scan "$d"); then
    fail "fires on a safe form: $out"
else
    pass "no hits on the safe forms"
fi

echo "=== Self-test: scratchstore refuses the real home and its store ==="
# Pure path checks against the helper — nothing here deletes or runs mg.
(
    SCRATCHSTORE_REAL_HOME=$(scratchstore_real_home) || SCRATCHSTORE_REAL_HOME=""
    SCRATCHSTORE_ENTRY_HOME=$HOME
    SCRATCHSTORE_DIR="$WORK/ss"
    mkdir -p "$SCRATCHSTORE_DIR/home" "$SCRATCHSTORE_DIR/store"
    rc=0
    HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="$SCRATCHSTORE_DIR/store" scratchstore_assert_safe 2>/dev/null ||
        { echo "  FAIL: refused a genuine scratch store" >&2; rc=1; }
    HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="$SCRATCHSTORE_ENTRY_HOME/.macguffin" scratchstore_assert_safe 2>/dev/null &&
        { echo "  FAIL: accepted MG_ROOT = the live store" >&2; rc=1; }
    HOME="$SCRATCHSTORE_ENTRY_HOME" MG_ROOT="$SCRATCHSTORE_DIR/store" scratchstore_assert_safe 2>/dev/null &&
        { echo "  FAIL: accepted HOME = the real home" >&2; rc=1; }
    HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="$SCRATCHSTORE_ENTRY_HOME" scratchstore_assert_safe 2>/dev/null &&
        { echo "  FAIL: accepted MG_ROOT = the real home" >&2; rc=1; }
    HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="" scratchstore_assert_safe 2>/dev/null &&
        { echo "  FAIL: accepted an unset MG_ROOT (which falls through to the live store)" >&2; rc=1; }
    # The home check on its own. Every case above is ALSO outside the scratch
    # dir, so the containment check refuses them regardless and they cannot
    # tell a broken home check from a working one. Here the "real home" sits
    # inside the scratch dir, so only the home check can refuse.
    (
        SCRATCHSTORE_REAL_HOME=""
        SCRATCHSTORE_ENTRY_HOME="$SCRATCHSTORE_DIR/fakehome"
        mkdir -p "$SCRATCHSTORE_ENTRY_HOME/.macguffin"
        HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="$SCRATCHSTORE_ENTRY_HOME/.macguffin" scratchstore_assert_safe 2>/dev/null &&
            exit 1
        HOME="$SCRATCHSTORE_ENTRY_HOME" MG_ROOT="$SCRATCHSTORE_DIR/store" scratchstore_assert_safe 2>/dev/null &&
            exit 1
        exit 0
    ) || { echo "  FAIL: the home check alone accepted the home or its store" >&2; rc=1; }
    if [ -n "$SCRATCHSTORE_REAL_HOME" ]; then
        # The passwd home is protected even when the caller's HOME already lies.
        SCRATCHSTORE_ENTRY_HOME="$SCRATCHSTORE_DIR/elsewhere"
        HOME="$SCRATCHSTORE_DIR/home" MG_ROOT="$SCRATCHSTORE_REAL_HOME/.macguffin/work" scratchstore_assert_safe 2>/dev/null &&
            { echo "  FAIL: accepted a path inside the passwd home's store" >&2; rc=1; }
    fi
    exit $rc
) && pass "scratchstore accepts scratch, refuses home / store / unset" || fail "scratchstore refusal cases (see above)"

TARGET=${1:-$REPO_ROOT}
echo "=== Scan: $TARGET ==="
if hits=$(live_rm_scan "$TARGET"); then
    echo "$hits" >&2
    fail "recursive delete of the real home or the live store (mg-9a40) — point the script at a scratch HOME/MG_ROOT (scripts/lib/scratchstore.sh) and delete that instead"
else
    pass "no recursive delete of the real home or the live store"
fi

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]

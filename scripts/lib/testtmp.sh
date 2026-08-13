# shellcheck shell=sh
# The shell half of internal/testtmp — the same root, the same names, the same
# ownership rule, for the *_test.sh scripts that ./test.sh runs.
#
# WHY A SHELL HALF AT ALL. Every one of those scripts opened with
#
#     tmpdir="$(mktemp -d)"
#     trap 'rm -rf "$tmpdir"' EXIT
#
# which puts a directory straight into the shared $TMPDIR and removes it from a
# trap that does NOT fire on SIGTERM, SIGINT or SIGHUP — only on a normal exit.
# So the cleanup ran on the success path and leaked on a kill, a Ctrl-C, or a
# refinery gate timing out the suite, which is exactly when it matters. On
# 2026-08-13 the host disk reached 100% with 204Mi free and every merge gate on
# the box began failing with Errno 28; the branch that gets rejected is whichever
# one happens to run when the disk crosses (mg-cc3f).
#
# The trap stays — it is what reclaims the space promptly on the ordinary path.
# What this file adds is the guarantee that does not depend on it: the directory
# is nested inside one swept root and carries the pid that owns it, so the next
# run reclaims whatever a kill left behind.
#
# USAGE
#
#     . "${REPO_ROOT}/scripts/lib/testtmp.sh"
#     tmpdir="$(testtmp_dir test-shadow)" || exit 1
#     trap 'testtmp_remove "$tmpdir"' EXIT INT TERM HUP
#
# Name the signals. `trap ... EXIT` alone does not fire on SIGTERM.

# The root, and it must equal internal/testtmp.RootName. It is a literal rather
# than a grep of the Go source, because a runtime grep only moves the failure: it
# turns a rename into an empty variable and a sweep of $TMPDIR/. The agreement is
# pinned by a test instead — TestShellHalfAgreesOnTheRoot in internal/testtmp —
# which fails by name on a rename at either end.
TESTTMP_ROOT_NAME=macguffin-test-tmp

# testtmp_root prints the swept root, creating it on first use.
testtmp_root() {
    _tt_root="${TMPDIR:-/tmp}"
    _tt_root="${_tt_root%/}/${TESTTMP_ROOT_NAME}"
    # -L before mkdir, not instead of it. $TMPDIR is per-user on darwin, but it
    # falls back to a world-writable /tmp when unset — which is the case in CI —
    # and there a pre-planted symlink at this name would have the sweep deleting
    # a tree of somebody else's choosing. mkdir -p follows the link and reports
    # success, so the refusal has to be explicit.
    if [ -L "$_tt_root" ]; then
        echo "testtmp: $_tt_root is a symlink; refusing to create or sweep through it" >&2
        return 1
    fi
    mkdir -p "$_tt_root" 2>/dev/null || {
        echo "testtmp: cannot create $_tt_root" >&2
        return 1
    }
    chmod 700 "$_tt_root" 2>/dev/null || :
    printf '%s\n' "$_tt_root"
}

# testtmp_pid_alive reports whether $1 names a live process.
#
# ps, not `kill -0`. A process owned by another user answers EPERM to kill -0,
# which the shell reports as a non-zero exit and which reads as "gone" — and this
# box runs several agents at once, so reading another user's LIVE test run as
# dead would delete a running suite's fixtures and surface as a branch defect
# against code that is fine. ps answers existence without needing permission to
# signal.
testtmp_pid_alive() {
    ps -p "$1" >/dev/null 2>&1
}

# testtmp_remove deletes a tree that `rm -rf` alone may not be able to.
#
# A scratch root that stands in for $HOME collects $HOME/go/pkg/mod the moment
# anything under it shells out to `go build`, and Go writes its module cache
# read-only: 0444 files inside 0555 directories. rm cannot unlink a child of a
# directory it may not write, so it stops at the first one and leaves the largest
# thing in the nest behind. The chmod pass is the fix; the message is the other
# half of it, because the original failure was not that the removal stopped, it
# was that nothing said so.
testtmp_remove() {
    [ -n "$1" ] || return 0
    [ -e "$1" ] || return 0
    rm -rf "$1" 2>/dev/null && return 0
    chmod -R u+rwX "$1" 2>/dev/null || :
    rm -rf "$1" 2>/dev/null && return 0
    echo "testtmp: could not remove $1" >&2
    return 1
}

# testtmp_reap removes entries in the root that no live process owns.
#
#   - the name encodes a pid and that process is alive — keep, at any age;
#   - the name encodes a pid and that process is gone — remove;
#   - the name encodes no pid — remove once it is older than TESTTMP_STALE_MIN.
#
# Ownership rather than age, because age is the reading that gets this wrong in
# the expensive direction — see testtmp_pid_alive. Errors are swallowed: a sweep
# that cannot delete something has lost nothing the caller can act on, and it
# must never be the reason a test fails.
TESTTMP_STALE_MIN=120

testtmp_reap() {
    _tt_reap_root=$1
    [ -d "$_tt_reap_root" ] || return 0
    for _tt_e in "$_tt_reap_root"/*; do
        [ -e "$_tt_e" ] || continue
        _tt_name=${_tt_e##*/}
        # purpose.pid.tail — three dot-separated fields with a numeric pid in the
        # middle. Only the middle field is read; internal/testtmp writes a
        # counter in the tail and this file writes mktemp's suffix, and neither
        # end parses the other's.
        _tt_pid=$(printf '%s\n' "$_tt_name" | awk -F. 'NF == 3 && $2 ~ /^[1-9][0-9]*$/ { print $2 }')
        if [ -n "$_tt_pid" ]; then
            testtmp_pid_alive "$_tt_pid" || testtmp_remove "$_tt_e" >/dev/null 2>&1 || :
            continue
        fi
        # No pid: the fallback rule. -maxdepth 0 so the age read is the entry's
        # own, and -mmin because a nested write does not advance a directory's
        # mtime — hence a margin measured in hours against a suite measured in
        # minutes.
        if [ -n "$(find "$_tt_e" -maxdepth 0 -mmin "+${TESTTMP_STALE_MIN}" 2>/dev/null)" ]; then
            testtmp_remove "$_tt_e" >/dev/null 2>&1 || :
        fi
    done
}

# testtmp_dir prints a fresh directory inside the swept root, owned by this
# script's process.
#
# $1 is a short label naming what the directory holds ("test-shadow"), not a
# path: it appears verbatim in the name, so a dot or a separator in it would
# produce a name the sweep cannot parse — and an unparseable name is one that can
# only be aged out, which silently converts a pid-owned entry into a two-hour
# one. Such a label is refused rather than mangled.
testtmp_dir() {
    _tt_purpose=$1
    case "$_tt_purpose" in
        '' | *.* | */*)
            echo "testtmp: purpose '$_tt_purpose' must be non-empty and contain no dot or separator" >&2
            return 1
            ;;
    esac
    _tt_root=$(testtmp_root) || return 1
    testtmp_reap "$_tt_root"

    # mktemp, not a counter: the name has to be unique even when one script asks
    # twice, and $$ is not — a shell function called inside $( ) sees the parent
    # shell's pid, which is what makes the entry reapable in the first place. The
    # suffix is the tail field, which nothing parses.
    _tt_dir=$(mktemp -d "${_tt_root}/${_tt_purpose}.$$.XXXXXX") || {
        echo "testtmp: cannot create a directory under $_tt_root" >&2
        return 1
    }
    chmod 700 "$_tt_dir" 2>/dev/null || :
    printf '%s\n' "$_tt_dir"
}

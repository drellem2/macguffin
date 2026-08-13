#!/bin/sh
# The $TMPDIR leak guard (mg-cc3f).
#
# THE MEASUREMENT IS THE TEST. Count $TMPDIR's entries, run a suite that creates
# fixtures, count again. That is the whole acceptance criterion, and it is the
# only detector this failure has: nothing on this host reported the disk at all
# until a build died. On 2026-08-13 the disk reached 100% capacity with 204Mi
# free and every merge gate on the box began failing with Errno 28 — which
# presents as a random branch defect, because the gate that dies is whichever one
# happens to run when the disk crosses.
#
# WHAT IS ASSERTED
#
#   Test 2  POSITIVE CONTROL, and it runs first. The check is a count, so "the
#           count did not grow" is worth nothing until "the count grows when
#           something leaks" has been shown against the same counting code.
#   Test 3  A COLD $TMPDIR gains EXACTLY ONE entry — the testtmp root.
#   Test 4  A WARM $TMPDIR gains NOTHING. The acceptance criterion, verbatim.
#   Test 5  The sweep RECLAIMS. Nesting alone would only move the problem one
#           level down, so repeated runs must not grow the root's contents.
#   Test 6  The shell half's sweep keeps a LIVE owner's entry and removes a dead
#           one. The keeping direction is the load-bearing one: this box runs
#           several agents at once, so a sweep that deleted a running suite's
#           fixtures would be this same failure arriving by a new route.
#
# WHAT IS NOT COVERED, AND WHY IT IS NAMED HERE RATHER THAN LEFT QUIET
#
#   install.sh's own `mktemp -d` is untouched. It is the PRODUCTION installer,
#   not the harness, and it runs once on a user's machine rather than on every
#   suite run on this box.
#
#   scripts/event_test.sh and scripts/e2e_milestones_test.sh create no temp
#   directories at all — they operate on ~/.macguffin directly, which is a
#   separate problem and not this one.
#
#   $TMPDIR entries of other provenance (tmp.*, and other agents' prefixes) are
#   deliberately left alone. Reclaiming what has already leaked is a different,
#   more careful operation from stopping the leak.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
REPO_ROOT="$SCRIPT_DIR"
PASS=0
FAIL=0

# shellcheck source=lib/testtmp.sh
. "${REPO_ROOT}/scripts/lib/testtmp.sh"

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1" >&2; }

# This suite's own scratch space goes through the helper it is testing. A guard
# against leaked temp directories that leaked its own would be its own defect.
WORK="$(testtmp_dir test-tmpdir-leak)" || exit 1
# HANG_PID is armed BEFORE anything can set it, and the trap names the signals.
# `trap ... EXIT` alone does not fire on SIGTERM, and a background process that
# outlives this script reparents to init and is nobody's to collect.
HANG_PID=""
cleanup() {
    [ -n "$HANG_PID" ] && kill -9 "$HANG_PID" 2>/dev/null
    testtmp_remove "$WORK"
}
trap cleanup EXIT INT TERM HUP

# The root name is read from the Go constant rather than duplicated a third time,
# so a rename there cannot leave this suite asserting a name nothing writes.
# internal/testtmp's TestShellHalfAgreesOnTheRoot pins the other pairing.
ROOT_NAME="$(grep -E '^const RootName = ' "${REPO_ROOT}/internal/testtmp/testtmp.go" | sed -E 's/.*"(.*)".*/\1/')"
if [ -z "$ROOT_NAME" ]; then
    echo "SETUP FAILURE: could not read RootName from internal/testtmp/testtmp.go" >&2
    exit 1
fi

# count_entries prints the number of TOP-LEVEL entries in a directory. Top-level
# is the whole point: the defect is $TMPDIR's entry count, not its depth.
# A missing directory counts zero rather than printing find's complaint: the
# root's absence is a legitimate reading here (nothing was created), and a stderr
# line in the middle of a passing run reads as a defect.
count_entries() { find "$1" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l | tr -d ' '; }

# list_entries prints them, so a failure names what appeared rather than only how
# many.
list_entries() { find "$1" -mindepth 1 -maxdepth 1 -exec basename {} \; 2>/dev/null | sort; }

# run_slice runs a fixture-creating suite with $TMPDIR pinned to its argument.
#
# The slice is chosen to touch every adopter in seconds: ./internal/... is all
# seven package TestMains, the cmd/mg names reach the TestMain that owned the
# largest leak (and t.TempDir, via buildBinary), and test-shadow.sh is the shell
# half. A new leak in any of them fails this file by name.
#
# Output is captured and a failing suite is reported as a SETUP failure below: a
# suite that did not run creates no fixtures and would sail through every count
# in this file.
run_slice() {
    tmp=$1
    log=$2
    (
        cd "$REPO_ROOT" &&
        TMPDIR="$tmp" go test -count=1 -timeout 5m ./internal/... >"$log" 2>&1 &&
        TMPDIR="$tmp" go test -count=1 -timeout 5m -run 'TestCLI_Version$|TestCLI_VersionFlag$' ./cmd/mg/ >>"$log" 2>&1 &&
        TMPDIR="$tmp" sh scripts/test-shadow.sh >>"$log" 2>&1
    )
}

echo "=== \$TMPDIR leak guard (mg-cc3f) ==="
echo "    testtmp root: $ROOT_NAME"

# --- Test 1: this file's own syntax ----------------------------------------
echo ""
echo "Test 1: Script syntax check"
if sh -n "$0" 2>/dev/null && sh -n "${REPO_ROOT}/scripts/lib/testtmp.sh" 2>/dev/null; then
    pass "test-tmpdir-leak.sh and lib/testtmp.sh have valid sh syntax"
else
    fail "sh syntax errors in test-tmpdir-leak.sh or lib/testtmp.sh"
fi

# --- Test 2: POSITIVE CONTROL ----------------------------------------------
# Before "the count did not grow" is allowed to mean anything, the same counting
# code has to be shown reporting growth. This plants a directory of exactly the
# shape the defect produced: an entry sitting directly in $TMPDIR that nothing
# will ever remove.
echo ""
echo "Test 2: POSITIVE CONTROL — the count detects a planted leak"
control="${WORK}/control"
mkdir -p "$control"
before=$(count_entries "$control")
mktemp -d "${control}/mg-leaked-fixture.XXXXXX" >/dev/null
after=$(count_entries "$control")
if [ "$after" -gt "$before" ]; then
    pass "a planted directory moves the count $before -> $after"
else
    fail "the count did not move when a directory was planted ($before -> $after); every other assertion in this file is vacuous"
fi

# --- Test 3: a cold TMPDIR gains exactly one entry --------------------------
echo ""
echo "Test 3: a COLD \$TMPDIR gains exactly one entry, and it is the testtmp root"
cold="${WORK}/cold"
mkdir -p "$cold"
if ! run_slice "$cold" "${WORK}/cold.log"; then
    fail "SETUP: the fixture-creating slice did not pass, so this file measured nothing"
    sed -n '1,40p' "${WORK}/cold.log" >&2
else
    n=$(count_entries "$cold")
    if [ "$n" -eq 1 ] && [ "$(list_entries "$cold")" = "$ROOT_NAME" ]; then
        pass "one entry after a cold run, and it is $ROOT_NAME"
    else
        fail "a cold run left $n top-level entries, want exactly 1 ($ROOT_NAME):"
        list_entries "$cold" | sed 's/^/        /' >&2
    fi
fi

# --- Test 4: the acceptance criterion, verbatim -----------------------------
echo ""
echo "Test 4: a WARM \$TMPDIR is UNCHANGED by a run that creates fixtures"
before=$(count_entries "$cold")
if ! run_slice "$cold" "${WORK}/warm.log"; then
    fail "SETUP: the fixture-creating slice did not pass on the warm run"
    sed -n '1,40p' "${WORK}/warm.log" >&2
else
    after=$(count_entries "$cold")
    if [ "$after" -eq "$before" ]; then
        pass "entry count unchanged across a run: $before -> $after"
    else
        fail "entry count grew across a run: $before -> $after. Entries now:"
        list_entries "$cold" | sed 's/^/        /' >&2
    fi
fi

# --- Test 5: the sweep reclaims --------------------------------------------
# Nesting alone would turn N entries in $TMPDIR into N entries one level down.
# The sweep is what makes the fix a fix, so it gets its own assertion.
echo ""
echo "Test 5: repeated runs do not grow the testtmp root"
inner=$(count_entries "${cold}/${ROOT_NAME}")
if ! run_slice "$cold" "${WORK}/sweep.log"; then
    fail "SETUP: the fixture-creating slice did not pass on the third run"
    sed -n '1,40p' "${WORK}/sweep.log" >&2
else
    grown=$(count_entries "${cold}/${ROOT_NAME}")
    # Not "== inner": a run that ends abnormally leaves its own entry for the
    # next run to reap, so the steady state is a small constant rather than
    # zero. What must not happen is growth PER RUN.
    if [ "$grown" -le "$((inner + 1))" ]; then
        pass "root contents steady across runs: $inner -> $grown"
    else
        fail "root contents grew $inner -> $grown across one run; the sweep is not reclaiming:"
        list_entries "${cold}/${ROOT_NAME}" | sed 's/^/        /' >&2
    fi
fi

# --- Test 6: the shell half's ownership rule, both directions ---------------
echo ""
echo "Test 6: the shell sweep keeps a LIVE owner's entry and removes a dead one"
fixture="${WORK}/fixture-root"
mkdir -p "$fixture"
# A pid that is certainly gone: a child that has already been waited on.
(exit 0) &
dead_pid=$!
wait "$dead_pid" 2>/dev/null || :
live="${fixture}/probe.$$.aaaaaa"
dead="${fixture}/probe.${dead_pid}.bbbbbb"
mkdir -p "$live" "$dead"
# Old enough that any age-based rule would delete the live one. Ownership must
# win: this is the direction whose failure surfaces as somebody else's branch
# defect.
touch -t 200001010000 "$live" 2>/dev/null || :
if testtmp_pid_alive "$dead_pid"; then
    echo "  SKIP: pid $dead_pid was reused before the assertion could run"
else
    testtmp_reap "$fixture"
    if [ -d "$live" ]; then
        pass "a LIVE owner's entry survives the sweep, at any age"
    else
        fail "the sweep deleted a LIVE process's directory ($live) — a running suite would lose its fixtures and report a branch defect"
    fi
    if [ ! -e "$dead" ]; then
        pass "a dead owner's entry is reclaimed"
    else
        fail "the sweep kept a dead process's directory ($dead); nesting without reclaiming only renames the problem"
    fi
fi

# --- Test 7: THE ONE THAT FAILED BEFORE THE FIX ----------------------------
# Tests 3 and 4 above pass on the pre-fix tree, and that is not a flaw in them —
# it is the defect's defining property. Every cleanup this repo had ran on the
# SUCCESS path: TestMain removed its directory after m.Run(), t.TempDir() removed
# its own from a t.Cleanup, and the shell scripts removed theirs from a trap. A
# clean run therefore leaked nothing, and the leak arrived only when a run was
# killed, panicked, or hit its -timeout — which is when suites are run most.
#
# So this case ABORTS a run and counts. Measured on the pre-fix tree, an aborted
# `go test ./cmd/mg/` left three entries in $TMPDIR: mg-test-cwd-<n> from
# TestMain, TestCLI_Version<n> from t.TempDir, and go-build<n> from the `go build`
# child it was in the middle of. After the fix it leaves one — the root — with
# the dead process's directory inside it, which the next run reclaims by
# ownership.
#
# -timeout is the abort, because Go implements a test timeout by panicking: it
# reproduces the real failure exactly, and it does so on a deterministic clock
# rather than a sleep-then-kill race.
echo ""
echo "Test 7: an ABORTED run leaks nothing into \$TMPDIR, and the next run reclaims it"
abort="${WORK}/abort"
mkdir -p "$abort"
if (cd "$REPO_ROOT" && TMPDIR="$abort" go test -count=1 -timeout 10ms -run 'TestCLI_Version$' ./cmd/mg/ >"${WORK}/abort.log" 2>&1); then
    fail "SETUP: the run under -timeout 10ms PASSED, so nothing was aborted and this case measured nothing"
else
    n=$(count_entries "$abort")
    if [ "$n" -eq 1 ] && [ "$(list_entries "$abort")" = "$ROOT_NAME" ]; then
        pass "an aborted run left exactly 1 entry, and it is $ROOT_NAME"
    else
        fail "an aborted run left $n top-level entries in \$TMPDIR, want exactly 1 ($ROOT_NAME):"
        list_entries "$abort" | sed 's/^/        /' >&2
    fi
    # The other half: what it left INSIDE the root is owned by a pid that is now
    # gone, so the next run must reclaim it. Without this, the fix would be a
    # rename of the leak rather than a repair of it.
    orphans=$(count_entries "${abort}/${ROOT_NAME}")
    if [ "$orphans" -eq 0 ]; then
        fail "the aborted run left nothing inside the root, so the reclaim below would prove nothing"
    else
        (cd "$REPO_ROOT" && TMPDIR="$abort" go test -count=1 -timeout 2m -run TestOwnerPIDReadsBackWhatEntryNameWrote ./internal/testtmp/ >>"${WORK}/abort.log" 2>&1) || :
        after=$(count_entries "${abort}/${ROOT_NAME}")
        if [ "$after" -eq 0 ]; then
            pass "the next run reclaimed the dead run's $orphans leftover(s)"
        else
            fail "the dead run's leftovers survived the next run ($orphans -> $after):"
            list_entries "${abort}/${ROOT_NAME}" | sed 's/^/        /' >&2
        fi
    fi
fi

# --- Test 8: the same guarantee for the shell half, under SIGKILL -----------
# The trap in each *_test.sh names INT, TERM and HUP now, so those are handled.
# Nothing whatsoever survives SIGKILL, which is exactly why the guarantee cannot
# rest on a trap: the directory has to be somewhere a later run will find and
# reclaim it.
echo ""
echo "Test 8: a SIGKILLed script's directory is nested, and reclaimed by the next run"
killdir="${WORK}/killed"
mkdir -p "$killdir"
cat > "${WORK}/hang.sh" <<HANGEOF
. "${REPO_ROOT}/scripts/lib/testtmp.sh"
d="\$(testtmp_dir hangprobe)" || exit 1
printf '%s\n' "\$d" > "${WORK}/hang.path"
# exec, so this shell IS the process that gets killed and no child outlives it.
exec sleep 120
HANGEOF
TMPDIR="$killdir" sh "${WORK}/hang.sh" &
HANG_PID=$!
waited=0
while [ ! -s "${WORK}/hang.path" ] && [ "$waited" -lt 100 ]; do
    sleep 0.1
    waited=$((waited + 1))
done
if [ ! -s "${WORK}/hang.path" ]; then
    fail "SETUP: the probe script never reported a directory, so nothing was measured"
    kill -9 "$HANG_PID" 2>/dev/null || :
    HANG_PID=""
else
    hang_path=$(cat "${WORK}/hang.path")
    kill -9 "$HANG_PID" 2>/dev/null || :
    wait "$HANG_PID" 2>/dev/null || :
    HANG_PID=""

    n=$(count_entries "$killdir")
    if [ "$n" -eq 1 ] && [ "$(list_entries "$killdir")" = "$ROOT_NAME" ]; then
        pass "a SIGKILLed script left exactly 1 entry, and it is $ROOT_NAME"
    else
        fail "a SIGKILLed script left $n top-level entries in \$TMPDIR, want exactly 1 ($ROOT_NAME):"
        list_entries "$killdir" | sed 's/^/        /' >&2
    fi
    if [ ! -d "$hang_path" ]; then
        fail "SETUP: the probe's directory ($hang_path) was gone before the sweep ran"
    else
        testtmp_reap "${killdir}/${ROOT_NAME}"
        if [ ! -e "$hang_path" ]; then
            pass "the sweep reclaimed it once its owner was gone — which no trap could have done"
        else
            fail "the killed script's directory survived the sweep: $hang_path"
        fi
    fi
fi

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]

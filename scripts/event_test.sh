#!/usr/bin/env bash
# Shell script tests for mg event append and mg event list
# Usage: ./scripts/event_test.sh [./mg]
set -euo pipefail

MG="${1:-./mg}"
PASS=0
FAIL=0
FAILURES=""

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); FAILURES="${FAILURES}\n  - $1"; }
# Every run gets a throwaway HOME and MG_ROOT, and clean() never deletes: it
# points MG_ROOT at a fresh empty store. This line used to recursively delete
# the live store under the real home, and on 2026-09-27 it did (mg-9a40).
# scratchstore refuses to proceed if HOME or MG_ROOT resolves to the real home.
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/testtmp.sh
. "${REPO_ROOT}/scripts/lib/testtmp.sh"
# shellcheck source=lib/scratchstore.sh
. "${REPO_ROOT}/scripts/lib/scratchstore.sh"
scratchstore_setup event-test || exit 1
trap 'testtmp_remove "$SCRATCHSTORE_DIR"' EXIT INT TERM HUP
clean() { scratchstore_fresh || exit 1; }

echo "=== MacGuffin Event Tests ==="
echo "Binary: $MG"
echo ""

# ---------------------------------------------------------------------------
echo "--- Event Append: basic ---"
clean
$MG init >/dev/null 2>&1
OUT=$($MG event append agent.start --agent=crew-arch --role=crew 2>&1)
echo "$OUT" | grep -q '"type":"agent.start"' && pass "output has correct type" || fail "output missing type"
echo "$OUT" | grep -q '"ts"' && pass "output has timestamp" || fail "output missing timestamp"
echo "$OUT" | grep -q '"agent":"crew-arch"' && pass "output has agent field" || fail "output missing agent field"
echo "$OUT" | grep -q '"role":"crew"' && pass "output has role field" || fail "output missing role field"
test -f "$MG_ROOT"/events.jsonl && pass "events.jsonl created" || fail "events.jsonl not created"

# ---------------------------------------------------------------------------
echo "--- Event Append: multiple events ---"
clean
$MG init >/dev/null 2>&1
$MG event append agent.start --agent=cat-a3f >/dev/null 2>&1
$MG event append work.claim --agent=cat-a3f --item=mg-abc >/dev/null 2>&1
$MG event append work.done --agent=cat-a3f --item=mg-abc >/dev/null 2>&1
LINES=$(wc -l < "$MG_ROOT"/events.jsonl | tr -d ' ')
test "$LINES" -eq 3 && pass "3 lines in events.jsonl" || fail "expected 3 lines, got $LINES"

# ---------------------------------------------------------------------------
echo "--- Event List: all ---"
# Reuse workspace from above
OUT=$($MG event list 2>&1)
COUNT=$(echo "$OUT" | wc -l | tr -d ' ')
test "$COUNT" -eq 3 && pass "list shows 3 events" || fail "expected 3 events, got $COUNT"

# ---------------------------------------------------------------------------
echo "--- Event List: filter by type ---"
OUT=$($MG event list --type=agent.start 2>&1)
COUNT=$(echo "$OUT" | wc -l | tr -d ' ')
test "$COUNT" -eq 1 && pass "type filter returns 1 event" || fail "expected 1 event, got $COUNT"
echo "$OUT" | grep -q '"type":"agent.start"' && pass "filtered event has correct type" || fail "filtered event wrong type"

# ---------------------------------------------------------------------------
echo "--- Event List: tail ---"
OUT=$($MG event list --tail=2 2>&1)
COUNT=$(echo "$OUT" | wc -l | tr -d ' ')
test "$COUNT" -eq 2 && pass "tail=2 returns 2 events" || fail "expected 2 events, got $COUNT"

# ---------------------------------------------------------------------------
echo "--- Event List: empty workspace ---"
clean
$MG init >/dev/null 2>&1
# stdout only: the "no events found" notice is on stderr, and a pipeline reading
# this output should get nothing.
OUT=$($MG event list 2>/dev/null)
test -z "$OUT" && pass "empty workspace returns no output" || fail "expected no output, got: $OUT"

# ---------------------------------------------------------------------------
echo "--- Event Append: no extra fields ---"
clean
$MG init >/dev/null 2>&1
OUT=$($MG event append simple.event 2>&1)
echo "$OUT" | grep -q '"type":"simple.event"' && pass "bare event has type" || fail "bare event missing type"
echo "$OUT" | grep -q '"ts"' && pass "bare event has timestamp" || fail "bare event missing timestamp"

# ---------------------------------------------------------------------------
echo "--- Event Append: invalid flag rejected ---"
clean
$MG init >/dev/null 2>&1
if ! $MG event append bad.event notaflag >/dev/null 2>&1; then
  pass "positional arg rejected (non-zero exit)"
else
  fail "positional arg not rejected"
fi

# ---------------------------------------------------------------------------
echo "--- Event Append: bad --key (no =value) rejected ---"
clean
$MG init >/dev/null 2>&1
if ! $MG event append bad.event --novalue >/dev/null 2>&1; then
  pass "--key without =value rejected (non-zero exit)"
else
  fail "--key without =value not rejected"
fi

# ---------------------------------------------------------------------------
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ $FAIL -gt 0 ]; then
  echo -e "Failures:$FAILURES"
  exit 1
fi

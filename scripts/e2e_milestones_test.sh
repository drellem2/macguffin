#!/usr/bin/env bash
# End-to-end milestone tests for mg, derived from MILESTONES.md
# Usage: ./scripts/e2e_milestones_test.sh [./mg]
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
scratchstore_setup e2e-milestones || exit 1
trap 'testtmp_remove "$SCRATCHSTORE_DIR"' EXIT INT TERM HUP
clean() { scratchstore_fresh || exit 1; }
extract_id() { grep -oE 'mg-[a-f0-9]+' | head -1; }

echo "=== MacGuffin E2E Milestone Tests ==="
echo "Binary: $MG"
echo ""

# ---------------------------------------------------------------------------
echo "--- M0: Init + Layout ---"
clean
$MG init >/dev/null 2>&1
for dir in work/available work/claimed work/done mail log; do
  test -d "$MG_ROOT"/$dir && pass "$dir exists" || fail "$dir missing"
done

# ---------------------------------------------------------------------------
echo "--- M1: Work Item Create + Read ---"
clean
$MG init >/dev/null 2>&1
OUT=$($MG new --type=bug "Auth tokens not refreshing" 2>&1)
id=$(echo "$OUT" | extract_id)
test -f "$MG_ROOT"/work/available/${id}.md && pass "file created in available/" || fail "file not in available/"
head -8 "$MG_ROOT"/work/available/${id}.md | grep -q "^id:" && pass "frontmatter has id" || fail "frontmatter missing id"
head -8 "$MG_ROOT"/work/available/${id}.md | grep -q "^type:" && pass "frontmatter has type" || fail "frontmatter missing type"
$MG show "$id" >/dev/null 2>&1 && pass "mg show works" || fail "mg show failed"

# ---------------------------------------------------------------------------
echo "--- M2: Atomic Claim (10 concurrent) ---"
clean
$MG init >/dev/null 2>&1
$MG new "Race target" >/dev/null 2>&1
id=$(ls "$MG_ROOT"/work/available/ | head -1 | sed 's/.md//')
for i in $(seq 10); do
  $MG claim "$id" >/dev/null 2>&1 &
done
wait
CLAIMED=$(ls "$MG_ROOT"/work/claimed/ 2>/dev/null | wc -l | tr -d ' ')
AVAIL=$(ls "$MG_ROOT"/work/available/ 2>/dev/null | wc -l | tr -d ' ')
test "$CLAIMED" -eq 1 && pass "exactly 1 in claimed/" || fail "expected 1 claimed, got $CLAIMED"
test "$AVAIL" -eq 0 && pass "0 in available/" || fail "expected 0 available, got $AVAIL"

# ---------------------------------------------------------------------------
echo "--- M3: Complete + Lifecycle ---"
clean
$MG init >/dev/null 2>&1
OUT=$($MG new "Full lifecycle" 2>&1)
id=$(echo "$OUT" | extract_id)
$MG claim "$id" >/dev/null 2>&1
$MG done "$id" --result '{"status":"fixed","commit":"abc123"}' >/dev/null 2>&1
test -f "$MG_ROOT"/work/done/${id}.md && pass "item in done/" || fail "item not in done/"
test -f "$MG_ROOT"/work/done/${id}.result.json && pass "result sidecar exists" || fail "result sidecar missing"
$MG list --status=done 2>&1 | grep -q "$id" && pass "list --status=done shows item" || fail "list --status=done missing item"
AVAIL=$($MG list --status=available 2>&1 | grep -c "mg-" || true)
test "$AVAIL" -eq 0 && pass "nothing available after done" || fail "still items available"

# ---------------------------------------------------------------------------
echo "--- M4: Targeted Claim Release ---"
clean
$MG init >/dev/null 2>&1
OUT=$($MG new "Will be abandoned" 2>&1)
id=$(echo "$OUT" | extract_id)
$MG claim "$id" >/dev/null 2>&1
$MG unclaim "$id" >/dev/null 2>&1
test -f "$MG_ROOT"/work/available/${id}.md && pass "unclaimed item back in available/" || fail "unclaim failed"
CLAIMED_AFTER=$(ls "$MG_ROOT"/work/claimed/ 2>/dev/null | wc -l | tr -d ' ')
test "$CLAIMED_AFTER" -eq 0 && pass "claimed/ empty after unclaim" || fail "claimed/ not empty after unclaim"

# ---------------------------------------------------------------------------
echo "--- M5: Mail ---"
clean
$MG init >/dev/null 2>&1
$MG mail send arch --create --from=mayor --subject="Review needed" --body="Please review." >/dev/null 2>&1
NEW_COUNT=$(ls "$MG_ROOT"/mail/arch/new/ 2>/dev/null | wc -l | tr -d ' ')
test "$NEW_COUNT" -eq 1 && pass "1 message in new/" || fail "expected 1 in new/, got $NEW_COUNT"
$MG mail list arch 2>&1 | grep -q "Review needed" && pass "mail list shows subject" || fail "mail list missing subject"
MSGID=$(ls "$MG_ROOT"/mail/arch/new/ | head -1 | sed 's/.md//')
$MG mail read --force arch "$MSGID" >/dev/null 2>&1
NEW_AFTER=$(ls "$MG_ROOT"/mail/arch/new/ 2>/dev/null | wc -l | tr -d ' ')
CUR_AFTER=$(ls "$MG_ROOT"/mail/arch/cur/ 2>/dev/null | wc -l | tr -d ' ')
test "$NEW_AFTER" -eq 0 && pass "new/ empty after read" || fail "new/ not empty after read"
test "$CUR_AFTER" -eq 1 && pass "1 in cur/ after read" || fail "expected 1 in cur/, got $CUR_AFTER"

# ---------------------------------------------------------------------------
echo "--- M6: Dependency Scheduling ---"
clean
$MG init >/dev/null 2>&1
OUT1=$($MG new "Phase 1" 2>&1)
id1=$(echo "$OUT1" | extract_id)
OUT2=$($MG new "Phase 2" --depends="$id1" 2>&1)
id2=$(echo "$OUT2" | extract_id)
test ! -f "$MG_ROOT"/work/available/${id2}.md && pass "Phase 2 not in available (gated)" || fail "Phase 2 in available prematurely"
test -f "$MG_ROOT"/work/pending/${id2}.md && pass "Phase 2 in pending/" || fail "Phase 2 not in pending/"
$MG claim "$id1" >/dev/null 2>&1
$MG done "$id1" >/dev/null 2>&1
$MG schedule >/dev/null 2>&1
test -f "$MG_ROOT"/work/available/${id2}.md && pass "Phase 2 promoted to available" || fail "Phase 2 not promoted"

# ---------------------------------------------------------------------------
echo "--- M7: Git Cold Path ---"
clean
$MG init --git >/dev/null 2>&1
OUT=$($MG new "Tracked item" 2>&1)
id=$(echo "$OUT" | extract_id)
$MG snapshot >/dev/null 2>&1
COMMITS1=$(git -C "$MG_ROOT" log --oneline 2>&1 | wc -l | tr -d ' ')
test "$COMMITS1" -ge 1 && pass "snapshot created commit" || fail "no snapshot commit"
$MG claim "$id" >/dev/null 2>&1
$MG done "$id" >/dev/null 2>&1
$MG snapshot >/dev/null 2>&1
COMMITS2=$(git -C "$MG_ROOT" log --oneline 2>&1 | wc -l | tr -d ' ')
test "$COMMITS2" -ge 2 && pass ">= 2 commits showing lifecycle" || fail "expected >= 2 commits, got $COMMITS2"

# ---------------------------------------------------------------------------
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ $FAIL -gt 0 ]; then
  echo -e "Failures:$FAILURES"
  exit 1
fi

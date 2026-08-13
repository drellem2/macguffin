package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// WHAT THESE TESTS ARE FOR (mg-3386).
//
// The ticket reported that `mg done --successor` recorded the link nowhere
// queryable, so an enforced declares-remainder gate and a bypassed one were
// indistinguishable. Half of that was already false when it was filed — the
// forward `successor:` tag had shipped in mg-9259 and was sitting on both items
// the report cited. The report was written anyway, by a careful actor, because
// they inspected `mg show --json | jq 'keys'` and the result sidecar and the
// link was in neither.
//
// So the thing under test here is NOT "is a link recorded". mg-9259's tests next
// door already pin that. It is the two properties whose absence let a working
// mechanism be reported as broken:
//
//	READABLE FROM WHERE PEOPLE STAND — the link is a FIELD, not only a tag
//	                                   value, so a reader who does not know the
//	                                   tag convention still finds it.
//	WALKABLE IN BOTH DIRECTIONS      — the successor names its predecessor back,
//	                                   so a chain can be followed from either end.
//
// And the one query the ticket named as its acceptance condition, asserted by
// running it rather than by describing it: LIST EVERY DECLARED ITEM CLOSED
// WITHOUT A SUCCESSOR. TestCLI_DeclaredWithoutSuccessorIsAWritableQuery is that
// test, and it is the one that would have to fail for this ticket to reopen.

// --- 1. Readable from where people stand ------------------------------------

// TestCLI_SuccessorIsAJSONFieldNotOnlyATag reproduces the exact inspection that
// produced the false report. `jq 'keys'` on the completed item must now contain
// a key matching the thing the reader greps for, and it must name the id.
//
// The tag is still there and still authoritative — this asserts the projection,
// not a move.
func TestCLI_SuccessorIsAJSONFieldNotOnlyATag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "carry the remainder forward")
	triage := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")

	if out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build); code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}

	item := showItemJSON(t, bin, root, triage)

	raw, ok := item["successor"]
	if !ok {
		t.Fatalf("mg show %s --json has no 'successor' key; keys = %v\n"+
			"this is the inspection that produced a false bypass report", triage, keysOf(item))
	}
	if got := stringsOf(t, raw); len(got) != 1 || got[0] != build {
		t.Errorf("successor = %v, want [%s]", got, build)
	}

	// The tag is not replaced by the field. The guards read the tag, and a
	// projection that removed its source would move the defect rather than fix it.
	if tags := stringsOf(t, item["tags"]); !hasString(tags, "successor:"+build) {
		t.Errorf("tags = %v, want the successor: tag still present", tags)
	}
}

// TestCLI_LinkFieldsAreAlwaysArrays: the audit query filters on
// (.successor|length)==0, so an item with no links must carry an EMPTY ARRAY and
// not null. A null there makes `length` fail on exactly the items the query is
// looking for — the ones with no successor — which would make the check report
// an error instead of an answer, or worse, silently select nothing.
func TestCLI_LinkFieldsAreAlwaysArrays(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedAvailable(t, bin, root, "task", "an ordinary item with no links")
	item := showItemJSON(t, bin, root, id)

	for _, field := range []string{"successor", "predecessor"} {
		raw, ok := item[field]
		if !ok {
			t.Fatalf("mg show --json has no %q key; keys = %v", field, keysOf(item))
		}
		var arr []string
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Errorf("%s = %s, want an array (empty, not null): %v", field, raw, err)
		}
		if len(arr) != 0 {
			t.Errorf("%s = %v on an item with no links, want []", field, arr)
		}
	}
	if b := boolOf(t, item["declares_remainder"]); b {
		t.Errorf("declares_remainder = true on an item that declared nothing")
	}
}

// TestCLI_ShowResolvesTheSuccessorLine: `mg done` prints the successor's title
// and status at the moment of the link, and that line was the only place a
// wrong-but-real id was ever visible — it scrolls away with the terminal. The
// same line must be obtainable later, which is what "the chain cannot be walked"
// meant in practice.
func TestCLI_ShowResolvesTheSuccessorLine(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "build the thing the triage recommended")
	triage := seedClaimedTagged(t, bin, root, "triage", "--declares-remainder")
	if out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build); code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "show", triage)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", triage, code, out)
	}
	if !strings.Contains(out, "Successor:") {
		t.Errorf("mg show = %q, want a labelled Successor line — a reader who greps "+
			"for the word must find it without knowing the tag spelling", out)
	}
	if !strings.Contains(out, "build the thing the triage recommended") {
		t.Errorf("mg show = %q, want the successor's TITLE — the id alone is what a "+
			"wrong link already looks like", out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("mg show = %q, want the successor's CURRENT status", out)
	}
}

// TestCLI_ShowNamesAnUnresolvableSuccessor: the guards refuse a dangling
// successor at close time, so a dangling one at read time means the target was
// deleted AFTERWARDS — a link that has rotted since it was checked. That is the
// one state a bare tag list cannot tell apart from a healthy link, so it is the
// state most worth naming.
func TestCLI_ShowNamesAnUnresolvableSuccessor(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	// An item that does NOT declare a remainder may carry a successor: tag; the
	// guard fires on the declaration, never on the tag. That is the only route
	// to a dangling link without forcing anything.
	id := seedClaimedTagged(t, bin, root, "no declaration, rotted link", "--tags=successor:mg-ffff")
	if out, code := mgArchive(t, bin, root, "done", id); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, out)
	}
	if !strings.Contains(out, "UNRESOLVED") {
		t.Errorf("mg show = %q, want the rotted link named as unresolved", out)
	}
}

// --- 2. Walkable in both directions -----------------------------------------

// TestCLI_SuccessorRecordsItsPredecessor is the reciprocity control. Given the
// tracker, a reader must be able to find what it inherited.
func TestCLI_SuccessorRecordsItsPredecessor(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "carry the remainder forward")
	triage := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")

	if out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build); code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}

	item := showItemJSON(t, bin, root, build)
	if got := stringsOf(t, item["predecessor"]); len(got) != 1 || got[0] != triage {
		t.Errorf("predecessor on %s = %v, want [%s] — the chain is walkable in one "+
			"direction only, which is half of what was reported missing", build, got, triage)
	}

	out, _ := mgArchive(t, bin, root, "show", build)
	if !strings.Contains(out, "Predecessor:") || !strings.Contains(out, triage) {
		t.Errorf("mg show %s = %q, want a Predecessor line naming %s", build, out, triage)
	}
}

// TestCLI_BacklinkIsWrittenForATagFiledAnyOtherWay: the successor: tag has three
// routes onto an item — --successor, `mg edit --add-tags`, `mg new --tags` — and
// a reverse link that exists for one and not the others is a chain that breaks
// depending on how it was filed, which is the failure mode rather than a variant
// of it. So completion reconciles every successor: tag it finds, not just the
// one this run supplied.
func TestCLI_BacklinkIsWrittenForATagFiledAnyOtherWay(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "filed as a tag, not as a flag")
	triage := seedClaimedTagged(t, bin, root, "triage", "--declares-remainder", "--tags=successor:"+build)

	// No --successor here: the tag was already on the item.
	if out, code := mgArchive(t, bin, root, "done", triage); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}

	item := showItemJSON(t, bin, root, build)
	if got := stringsOf(t, item["predecessor"]); len(got) != 1 || got[0] != triage {
		t.Errorf("predecessor on %s = %v, want [%s]", build, got, triage)
	}
}

// TestCLI_ArchiveSuccessorAlsoWritesTheBacklink: `mg archive --successor` is the
// other writer of the forward link, and a reciprocity that held for `mg done`
// alone would be a chain that breaks at the archive boundary — the exact place
// the record is meant to become permanent.
func TestCLI_ArchiveSuccessorAlsoWritesTheBacklink(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "the build the design recommended")
	design := seedDoneOfType(t, bin, root, "design", "recommend building the thing")

	if out, code := mgArchive(t, bin, root, "archive", design, "--successor="+build); code != 0 {
		t.Fatalf("mg archive --successor: exit %d\n%s", code, out)
	}

	item := showItemJSON(t, bin, root, build)
	if got := stringsOf(t, item["predecessor"]); len(got) != 1 || got[0] != design {
		t.Errorf("predecessor on %s = %v, want [%s]", build, got, design)
	}
}

// TestCLI_ArchiveSweepClosesAnOlderLink: the sweep is the last moment an item is
// ever written, and the only route by which a link filed BEFORE reciprocity
// existed becomes walkable from both ends. Without it the gap is not "history is
// one-directional" but "history is one-directional and there is no way to tell
// which of those chains anything ever tried to close".
//
// The link is planted as a tag with no reverse half, exactly as one written by
// an older binary would be, and the sweep must close it.
func TestCLI_ArchiveSweepClosesAnOlderLink(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "the tracker of an older link")
	old := seedClaimedTagged(t, bin, root, "completed before reciprocity existed")

	if out, code := mgArchive(t, bin, root, "done", old); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	// Added AFTER completion, so nothing reconciled it on the way through.
	if out, code := mgArchive(t, bin, root, "edit", old, "--add-tags=successor:"+build); code != 0 {
		t.Fatalf("mg edit --add-tags: exit %d\n%s", code, out)
	}
	if got := stringsOf(t, showItemJSON(t, bin, root, build)["predecessor"]); len(got) != 0 {
		t.Fatalf("predecessor on %s = %v before the sweep, want [] — the fixture is not "+
			"reproducing an older one-directional link", build, got)
	}

	// --days=0 takes every done item.
	if out, code := mgArchive(t, bin, root, "archive", "--days=0"); code != 0 {
		t.Fatalf("mg archive --days=0: exit %d\n%s", code, out)
	}

	if got := stringsOf(t, showItemJSON(t, bin, root, build)["predecessor"]); len(got) != 1 || got[0] != old {
		t.Errorf("predecessor on %s = %v after the sweep, want [%s]", build, got, old)
	}
}

// TestCLI_BacklinkIsIdempotent: `mg done` refuses and is retried, items are
// reopened and completed again, and `mg archive` runs after `mg done`. Every one
// of those reconciles the same link a second time. A tag appended per pass would
// turn a working retry into a growing pile of duplicates.
func TestCLI_BacklinkIsIdempotent(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "linked more than once")
	triage := seedClaimedTagged(t, bin, root, "triage", "--declares-remainder")

	if out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build); code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}
	// Archiving reconciles the same link again, from the archive writer.
	if out, code := mgArchive(t, bin, root, "archive", triage); code != 0 {
		t.Fatalf("mg archive: exit %d\n%s", code, out)
	}

	item := showItemJSON(t, bin, root, build)
	if got := stringsOf(t, item["predecessor"]); len(got) != 1 {
		t.Errorf("predecessor on %s = %v after two reconciles, want exactly one entry", build, got)
	}
}

// --- 3. A reverse link that cannot be written says so, and refuses nothing ---

// TestCLI_UnwritableBacklinkDoesNotFailTheCompletion is the direction that
// matters most. The forward link is the half the gate reads and the half that is
// already on disk by the time the reverse one is attempted; turning a completion
// that satisfied its gate into a refusal because a SECOND item could not be
// updated would be a new way to lose work.
//
// And it must not be silent, because a best-effort link that fails quietly is
// this ticket's own defect one level down: absences with nothing saying which
// were failures.
func TestCLI_UnwritableBacklinkDoesNotFailTheCompletion(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	// A successor: tag naming nothing. The declaration guard is what refuses a
	// dangling link, so an item that does not declare gets through to the point
	// where the reverse write is attempted and fails.
	id := seedClaimedTagged(t, bin, root, "reverse link cannot land", "--tags=successor:mg-ffff")

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("an unwritable reverse link failed the completion: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "reverse link could not be recorded") {
		t.Errorf("output = %q, want the failed reverse link named — a silent one is "+
			"indistinguishable from a link nobody tried to write", out)
	}
	if !strings.Contains(out, "mg-ffff") {
		t.Errorf("output = %q, want the note to name the end it could not reach", out)
	}
	// The completion still happened.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s did not reach done/:\n%s", id, listOut)
	}
}

// --- 4. The acceptance condition the ticket named ---------------------------

// TestCLI_DeclaredWithoutSuccessorIsAWritableQuery runs the ticket's own
// criterion: "list every declared item closed without a successor". If this
// cannot be written, the gate is still unauditable and the ticket is not fixed.
//
// The predicate is evaluated here against `mg list --all --json` FIELDS ONLY —
// no tag-string parsing — because "you can recover it from tags if you know the
// convention" was true the whole time and is precisely what did not work.
//
// The store is seeded with all three populations, so a filter that is merely
// permissive fails too:
//
//	declared + closed + successor        must NOT be selected (the gate held)
//	declared + closed + no successor     MUST be selected     (the gate escaped)
//	declared + still open + no successor must NOT be selected (owes nothing yet)
func TestCLI_DeclaredWithoutSuccessorIsAWritableQuery(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	// Held: declared, closed, named a successor.
	build := seedAvailable(t, bin, root, "task", "carries the remainder")
	held := seedClaimedTagged(t, bin, root, "declared and discharged", "--declares-remainder")
	if out, code := mgArchive(t, bin, root, "done", held, "--successor="+build); code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}

	// Escaped: closed first, declaration added afterwards. That is the shape the
	// audit exists to find and the only one reachable without a bypass flag —
	// the guard itself refuses this combination at close time, which is why the
	// query's job is to catch what got past it some other way (an older binary,
	// a hand edit, a forced archive).
	escaped := seedClaimedTagged(t, bin, root, "declared after the fact")
	if out, code := mgArchive(t, bin, root, "done", escaped); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "edit", escaped, "--add-tags=declares-remainder"); code != 0 {
		t.Fatalf("mg edit --add-tags: exit %d\n%s", code, out)
	}

	// Open: declared, not closed. Owes a successor eventually, not now.
	open := seedClaimedTagged(t, bin, root, "declared and still working", "--declares-remainder")

	got := declaredClosedWithoutSuccessor(t, bin, root)

	if !hasString(got, escaped) {
		t.Errorf("the query missed %s — a declared item closed naming nothing.\n"+
			"selected: %v\nthis is the check the ticket asked to be made possible", escaped, got)
	}
	if hasString(got, held) {
		t.Errorf("the query selected %s, which named %s: a gate that held reads as "+
			"one that escaped, which is the reported defect pointed the other way", held, build)
	}
	if hasString(got, open) {
		t.Errorf("the query selected %s, which is still claimed and owes nothing yet", open)
	}
}

// TestCLI_TheAuditQueryPairCoversARottedLink pins the BOUNDARY of the query
// above, and the boundary matters as much as the query does.
//
// "Closed without a successor" asks whether one was NAMED. An item naming a
// successor that has since been deleted passes it — it has a non-empty
// .successor and reads as discharged — so an operator running only that query
// and getting an empty result would conclude the gate is clean while a link
// that tracks nothing sits in the store. That is this ticket's own failure
// shape (an empty trace read as a healthy one), reproduced inside its remedy,
// which is why `mg list --help` documents both halves and why both are asserted
// here rather than only the first.
//
// The second half is written over the same fields: a successor id that appears
// in no item's `id` names nothing.
func TestCLI_TheAuditQueryPairCoversARottedLink(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	rotted := seedClaimedTagged(t, bin, root, "names a link that tracks nothing", "--tags=successor:mg-ffff")
	if out, code := mgArchive(t, bin, root, "done", rotted); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}

	if got := declaredClosedWithoutSuccessor(t, bin, root); hasString(got, rotted) {
		t.Errorf("the named-nothing query selected %s, which names an id: the two "+
			"queries are being conflated, and their remedies differ", rotted)
	}

	if got := namesAVanishedSuccessor(t, bin, root); !hasString(got, rotted) {
		t.Errorf("the rotted-link query missed %s -> mg-ffff.\nselected: %v\n"+
			"an operator running only the first query would read an empty result as clean", rotted, got)
	}
}

// namesAVanishedSuccessor is the companion query documented in `mg list --help`,
// in Go, over the same fields:
//
//	mg list --all --json | jq -sr '[.[].id] as $ids
//	  | .[] | select(.successor - $ids | length > 0) | .id'
func namesAVanishedSuccessor(t *testing.T, bin, root string) []string {
	t.Helper()
	rows := listJSONRows(t, bin, root)

	known := make(map[string]bool, len(rows))
	for _, r := range rows {
		known[r.ID] = true
	}

	var ids []string
	for _, r := range rows {
		for _, sid := range r.Successor {
			if !known[sid] {
				ids = append(ids, r.ID)
				break
			}
		}
	}
	return ids
}

// declaredClosedWithoutSuccessor is the ticket's query, in Go, over the same
// fields the documented jq one-liner reads:
//
//	mg list --all --json | jq -r 'select(.declares_remainder
//	  and (.status=="done" or .status=="archived")
//	  and (.successor|length)==0) | .id'
func declaredClosedWithoutSuccessor(t *testing.T, bin, root string) []string {
	t.Helper()
	var ids []string
	for _, row := range listJSONRows(t, bin, root) {
		if row.DeclaresRemainder && (row.Status == "done" || row.Status == "archived") && len(row.Successor) == 0 {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

// listRow is the slice of `mg list --json` the two audit queries read. It is
// deliberately only the FIELDS — no tag-string parsing anywhere in this file —
// because "you can recover it from tags if you know the convention" was true the
// whole time and is precisely what did not work.
type listRow struct {
	ID                string   `json:"id"`
	Status            string   `json:"status"`
	Successor         []string `json:"successor"`
	DeclaresRemainder bool     `json:"declares_remainder"`
}

func listJSONRows(t *testing.T, bin, root string) []listRow {
	t.Helper()
	out, code := mgArchive(t, bin, root, "list", "--all", "--json")
	if code != 0 {
		t.Fatalf("mg list --all --json: exit %d\n%s", code, out)
	}

	var rows []listRow
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row listRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("mg list --json emitted an unparseable line %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// --- helpers ----------------------------------------------------------------

// showItemJSON reads one item as a map of raw fields, so a test can assert on the
// PRESENCE of a key (`jq 'keys'`, the inspection that produced the false report)
// and not only on its value.
func showItemJSON(t *testing.T, bin, root, id string) map[string]json.RawMessage {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show %s --json: exit %d\n%s", id, code, out)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("mg show %s --json emitted unparseable JSON: %v\n%s", id, err, out)
	}
	return m
}

func keysOf(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func stringsOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("field %s is not a string array: %v", raw, err)
	}
	return out
}

func boolOf(t *testing.T, raw json.RawMessage) bool {
	t.Helper()
	var out bool
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("field %s is not a bool: %v", raw, err)
	}
	return out
}

func hasString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

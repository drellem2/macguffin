package workitem

import (
	"strings"
	"testing"
)

// editAndCountEvents applies fields and returns the events emitted BY THAT
// CALL, by counting before and slicing after.
//
// mg-3122 is explicit about why the count matters: "a query that finds a stale
// event looks identical to one that finds a fresh one." An earlier attempt at
// the ticket's own investigation read a stale event and nearly recorded a
// refutation from an instrument that had not fired. Every assertion below
// therefore runs against this slice, never against a scan of the whole log.
func editAndCountEvents(t *testing.T, root, id string, fields UpdateField) []eventView {
	t.Helper()
	before := len(readEvents(t, root))
	if _, err := Update(root, id, fields); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after := readEvents(t, root)
	if len(after) < before {
		t.Fatalf("event log shrank: %d → %d", before, len(after))
	}
	fresh := make([]eventView, 0, len(after)-before)
	for _, e := range after[before:] {
		fresh = append(fresh, eventView{Type: e.Type, Extra: e.Extra})
	}
	return fresh
}

type eventView struct {
	Type  string
	Extra map[string]string
}

// onlyEdit asserts the call emitted exactly one work.edited and returns it.
func onlyEdit(t *testing.T, fresh []eventView) eventView {
	t.Helper()
	if len(fresh) != 1 {
		t.Fatalf("expected exactly 1 new event, got %d: %+v", len(fresh), fresh)
	}
	if fresh[0].Type != "work.edited" {
		t.Fatalf("new event type = %q, want work.edited", fresh[0].Type)
	}
	return fresh[0]
}

func seedMetaItem(t *testing.T, root, title string, opts ...CreateOption) *Item {
	t.Helper()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", title, nil, opts...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return item
}

// TestMetaEdit_AssigneeChangeIsLogged is mg-3122's defect B on the field that
// makes it load-bearing. `assignee` is the dispatch gate — config.IsDispatchGated
// suppresses stall-watch and dispatch for `human` and `parked` — so before this
// fix the one field deciding whether an item is ever worked on could be flipped
// by any agent with no audit record at all.
func TestMetaEdit_AssigneeChangeIsLogged(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Gate me", WithAssignee("mayor"))

	e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{Assignee: strPtr("parked")}))

	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["assignee_before"] != "mayor" {
		t.Errorf("assignee_before = %q, want mayor", e.Extra["assignee_before"])
	}
	if e.Extra["assignee_after"] != "parked" {
		t.Errorf("assignee_after = %q, want parked", e.Extra["assignee_after"])
	}
	if e.Extra["fields"] != "assignee" {
		t.Errorf("fields = %q, want assignee", e.Extra["fields"])
	}
	if e.Extra["mode"] != "metadata" {
		t.Errorf("mode = %q, want metadata (no body was touched)", e.Extra["mode"])
	}
	// The body hashes are still emitted and must be EQUAL: that is the
	// positive statement "the body was not at risk on this write", which an
	// absent field could not make.
	if e.Extra["body_hash_before"] == "" || e.Extra["body_hash_before"] != e.Extra["body_hash_after"] {
		t.Errorf("body hashes %q → %q, want a non-empty pair that is equal",
			e.Extra["body_hash_before"], e.Extra["body_hash_after"])
	}
	// And the actor is still the invoker, on the very edit that moves the
	// assignee — the two defects interacting is what made A undiagnosable.
	if e.Extra["actor"] != probeInvoker {
		t.Errorf("actor = %q, want %q", e.Extra["actor"], probeInvoker)
	}
}

// TestMetaEdit_ClearingTheAssigneeIsAChange: opening the dispatch gate is a
// transition to an EMPTY value, so a diff that treated empty as "no value
// supplied" would drop precisely the un-parking event.
func TestMetaEdit_ClearingTheAssigneeIsAChange(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Unpark me", WithAssignee("parked"))

	e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{Assignee: strPtr("")}))

	if e.Extra["assignee_before"] != "parked" {
		t.Errorf("assignee_before = %q, want parked", e.Extra["assignee_before"])
	}
	if got, ok := e.Extra["assignee_after"]; !ok || got != "" {
		t.Errorf("assignee_after = %q (present=%v), want an explicit empty value", got, ok)
	}
}

// TestMetaEdit_PriorityOnlyIsLogged is the second command mg-3122 measured
// emitting zero events.
func TestMetaEdit_PriorityOnlyIsLogged(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Prioritise me")

	e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{Priority: strPtr("high")}))

	if e.Extra["fields"] != "priority" {
		t.Errorf("fields = %q, want priority", e.Extra["fields"])
	}
	if e.Extra["priority_after"] != "high" {
		t.Errorf("priority_after = %q, want high", e.Extra["priority_after"])
	}
}

// TestMetaEdit_EveryTrackedFieldIsLogged sweeps the rest, one field per
// sub-test, each asserting the count grew by exactly one.
func TestMetaEdit_EveryTrackedFieldIsLogged(t *testing.T) {
	asInvoker(t)

	budget := 5000
	cases := []struct {
		name       string
		fields     UpdateField
		wantField  string
		wantAfter  string
		wantBefore string
	}{
		{name: "type", fields: UpdateField{Type: strPtr("bug")}, wantField: "type", wantBefore: "task", wantAfter: "bug"},
		{name: "repo", fields: UpdateField{Repo: strPtr("/tmp/elsewhere")}, wantField: "repo", wantBefore: "", wantAfter: "/tmp/elsewhere"},
		{name: "budget", fields: UpdateField{Budget: &budget}, wantField: "budget", wantBefore: "", wantAfter: "5000"},
		{name: "tags", fields: UpdateField{AddTags: []string{"urgent"}}, wantField: "tags", wantBefore: "", wantAfter: "urgent"},
		{name: "depends", fields: UpdateField{Depends: []string{"mg-zzzz"}}, wantField: "depends", wantBefore: "", wantAfter: "mg-zzzz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			item := seedMetaItem(t, root, "Sweep me")

			fresh := editAndCountEvents(t, root, item.ID, tc.fields)
			// A --depends change can additionally move the item between
			// available/ and pending/; that emits nothing of its own today, so
			// exactly one event is still the expectation.
			e := onlyEdit(t, fresh)

			if !strings.Contains(e.Extra["fields"], tc.wantField) {
				t.Errorf("fields = %q, want it to name %q", e.Extra["fields"], tc.wantField)
			}
			if got := e.Extra[tc.wantField+"_before"]; got != tc.wantBefore {
				t.Errorf("%s_before = %q, want %q", tc.wantField, got, tc.wantBefore)
			}
			if got := e.Extra[tc.wantField+"_after"]; got != tc.wantAfter {
				t.Errorf("%s_after = %q, want %q", tc.wantField, got, tc.wantAfter)
			}
			if e.Extra["actor"] != probeInvoker {
				t.Errorf("actor = %q, want %q", e.Extra["actor"], probeInvoker)
			}
		})
	}
}

// TestMetaEdit_TitleReportsBothHalves. A --title edit moves the title field AND
// rewrites the body's "# heading" line in place, so it is the one edit that is
// both a metadata change and a body change. It must report both, and must keep
// its historical body mode ("incidental") rather than being relabelled
// "metadata" — restorebody and the lost-update tests read that field.
func TestMetaEdit_TitleReportsBothHalves(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Old title")

	e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{Title: strPtr("New title")}))

	if e.Extra["mode"] != "incidental" {
		t.Errorf("mode = %q, want incidental", e.Extra["mode"])
	}
	if e.Extra["title_before"] != "Old title" || e.Extra["title_after"] != "New title" {
		t.Errorf("title %q → %q, want \"Old title\" → \"New title\"",
			e.Extra["title_before"], e.Extra["title_after"])
	}
	if e.Extra["body_hash_before"] == e.Extra["body_hash_after"] {
		t.Error("body hashes are equal, but a title edit rewrites the heading line")
	}
}

// TestMetaEdit_BodyAndMetadataInOneEdit stays a single event carrying both
// halves, rather than one event per concern.
func TestMetaEdit_BodyAndMetadataInOneEdit(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Combined")

	e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{
		Body:     strPtr("# Combined\n\nA whole new body.\n"),
		Assignee: strPtr("mayor"),
	}))

	if e.Extra["mode"] != "replace" {
		t.Errorf("mode = %q, want replace (a body flag was passed)", e.Extra["mode"])
	}
	if e.Extra["assignee_after"] != "mayor" {
		t.Errorf("assignee_after = %q, want mayor", e.Extra["assignee_after"])
	}
	if e.Extra["body_hash_before"] == e.Extra["body_hash_after"] {
		t.Error("body hashes are equal after a body replacement")
	}
}

// TestMetaEdit_NoOpEmitsNothing. The new condition is "the stored item moved",
// not "a flag was passed" — setting a field to the value it already holds
// changes nothing on disk and must not manufacture an audit line. A log that
// records non-events is a slower way to be untrustworthy.
func TestMetaEdit_NoOpEmitsNothing(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	item := seedMetaItem(t, root, "Unmoved", WithAssignee("mayor"))

	fresh := editAndCountEvents(t, root, item.ID, UpdateField{Assignee: strPtr("mayor")})
	if len(fresh) != 0 {
		t.Errorf("expected no events for a no-op edit, got %d: %+v", len(fresh), fresh)
	}
}

// TestMetaEdit_MultipleFieldsAreListedDeterministically. `fields` is a fixed
// order, not map iteration order, so two identical edits produce identical
// lines and a diff of the log is readable.
func TestMetaEdit_MultipleFieldsAreListedDeterministically(t *testing.T) {
	asInvoker(t)

	var seen string
	for i := 0; i < 5; i++ {
		root := t.TempDir()
		item := seedMetaItem(t, root, "Many fields")
		e := onlyEdit(t, editAndCountEvents(t, root, item.ID, UpdateField{
			Assignee: strPtr("mayor"),
			Priority: strPtr("high"),
			Repo:     strPtr("/tmp/r"),
		}))
		got := e.Extra["fields"]
		if want := "repo,assignee,priority"; got != want {
			t.Fatalf("fields = %q, want %q", got, want)
		}
		if i > 0 && got != seen {
			t.Fatalf("fields varied across runs: %q then %q", seen, got)
		}
		seen = got
	}
}

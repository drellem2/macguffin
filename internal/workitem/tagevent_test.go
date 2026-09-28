package workitem

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// eventsFor returns the entries of type typ whose item_id is id.
func eventsFor(t *testing.T, root, typ, id string) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, e := range readEvents(t, root) {
		if e.Type == typ && e.Extra["item_id"] == id {
			out = append(out, e.Extra)
		}
	}
	return out
}

// TestCreate_RecordsInitialTags is drellem2/macguffin#38 gap A: a tag set at
// filing had no event, so its history began at its first edit.
func TestCreate_RecordsInitialTags(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Tagged at birth", nil,
		WithTags([]string{"cutover-hold", "urgent"}), WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created := eventsFor(t, root, "work.created", item.ID)
	if len(created) != 1 {
		t.Fatalf("work.created events = %d, want 1", len(created))
	}
	if got := created[0]["tags"]; got != "cutover-hold,urgent" {
		t.Errorf("tags = %q, want cutover-hold,urgent", got)
	}
	if got := created[0]["actor"]; got != probeInvoker {
		t.Errorf("actor = %q, want %q", got, probeInvoker)
	}
}

// TestCreate_OmitsTagsWhenNone: "no field" keeps meaning "no tags", as it does
// for assignee.
func TestCreate_OmitsTagsWhenNone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Untagged", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created := eventsFor(t, root, "work.created", item.ID)
	if len(created) != 1 {
		t.Fatalf("work.created events = %d, want 1", len(created))
	}
	if _, ok := created[0]["tags"]; ok {
		t.Errorf("tags = %q on an untagged item, want the key absent", created[0]["tags"])
	}
}

// TestDoneSuccessor_EmitsTagEditsOnBothItems is gap B: `mg done --successor`
// wrote successor: on one item and predecessor: on the other with only
// work.done in the log. Each write must now be a work.edited tag change
// attributed to the invoker — the assignee is set to something else so an
// actor=assignee regression cannot pass.
func TestDoneSuccessor_EmitsTagEditsOnBothItems(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	design, err := Create(root, "mg-", "task", "Design", nil,
		WithTags([]string{"keep"}), WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create design: %v", err)
	}
	follow, err := Create(root, "mg-", "task", "Follow-up", nil, WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create follow-up: %v", err)
	}
	if _, err := Claim(root, design.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, design.ID, nil, WithDoneSuccessor(follow.ID)); err != nil {
		t.Fatalf("Done: %v", err)
	}

	cases := []struct {
		id, before, after string
	}{
		{design.ID, "keep", "keep," + SuccessorTag(follow.ID)},
		{follow.ID, "", PredecessorTag(design.ID)},
	}
	for _, c := range cases {
		edits := eventsFor(t, root, "work.edited", c.id)
		if len(edits) != 1 {
			t.Fatalf("%s: work.edited events = %d, want 1", c.id, len(edits))
		}
		e := edits[0]
		want := map[string]string{
			"actor":       probeInvoker,
			"mode":        "metadata",
			"fields":      "tags",
			"tags_before": c.before,
			"tags_after":  c.after,
		}
		for k, v := range want {
			if e[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.id, k, e[k], v)
			}
		}
		if e["body_hash_before"] == "" || e["body_hash_before"] != e["body_hash_after"] {
			t.Errorf("%s: body hashes %q/%q, want equal and non-empty", c.id, e["body_hash_before"], e["body_hash_after"])
		}
	}
}

// TestDoneSuccessor_BacklinkFailureDoesNotFailDone: the predecessor backlink is
// best-effort. When its write fails, done still succeeds, the failure is
// work.backlink_failed, and no tag change is logged for a write that did not
// land.
func TestDoneSuccessor_BacklinkFailureDoesNotFailDone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only mode this test relies on")
	}
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	design, err := Create(root, "mg-", "task", "Design", nil)
	if err != nil {
		t.Fatalf("Create design: %v", err)
	}
	follow, err := Create(root, "mg-", "task", "Follow-up", nil)
	if err != nil {
		t.Fatalf("Create follow-up: %v", err)
	}
	followPath := filepath.Join(root, "work", "available", follow.ID+".md")
	if err := os.Chmod(followPath, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(followPath, 0o644) })

	restore := backlinkNotice
	backlinkNotice = io.Discard
	t.Cleanup(func() { backlinkNotice = restore })

	if _, err := Claim(root, design.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, design.ID, nil, WithDoneSuccessor(follow.ID)); err != nil {
		t.Fatalf("Done failed on a best-effort backlink: %v", err)
	}

	if n := len(eventsFor(t, root, "work.backlink_failed", design.ID)); n != 1 {
		t.Errorf("work.backlink_failed events = %d, want 1", n)
	}
	if n := len(eventsFor(t, root, "work.edited", follow.ID)); n != 0 {
		t.Errorf("work.edited events on the unwritten backlink target = %d, want 0", n)
	}
	// Positive control: the forward half still logged, so the instrument above
	// is capable of seeing a work.edited here.
	if n := len(eventsFor(t, root, "work.edited", design.ID)); n != 1 {
		t.Errorf("work.edited events on the design = %d, want 1", n)
	}
}

// assertOneTagEdit checks that id has exactly one work.edited, and that it is a
// metadata tag change from before to after attributed to the invoker.
func assertOneTagEdit(t *testing.T, root, id, before, after string) {
	t.Helper()
	edits := eventsFor(t, root, "work.edited", id)
	if len(edits) != 1 {
		t.Fatalf("%s: work.edited events = %d, want 1", id, len(edits))
	}
	e := edits[0]
	want := map[string]string{
		"actor":       probeInvoker,
		"mode":        "metadata",
		"fields":      "tags",
		"tags_before": before,
		"tags_after":  after,
	}
	for k, v := range want {
		if e[k] != v {
			t.Errorf("%s: %s = %q, want %q", id, k, e[k], v)
		}
	}
	if e["body_hash_before"] == "" || e["body_hash_before"] != e["body_hash_after"] {
		t.Errorf("%s: body hashes %q/%q, want equal and non-empty", id, e["body_hash_before"], e["body_hash_after"])
	}
}

// TestDonePlain_ReconciledBacklinkEmitsTagEdit: plain `mg done` (no
// --successor) reconciles the reverse half of a successor: tag filed by
// another route, and that predecessor: write is a tag change like any other.
// The design's own tags are untouched, so it gets no work.edited.
func TestDonePlain_ReconciledBacklinkEmitsTagEdit(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	follow, err := Create(root, "mg-", "task", "Follow-up", nil, WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create follow-up: %v", err)
	}
	design, err := Create(root, "mg-", "task", "Design", nil,
		WithTags([]string{SuccessorTag(follow.ID)}), WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create design: %v", err)
	}
	if _, err := Claim(root, design.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, design.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	assertOneTagEdit(t, root, follow.ID, "", PredecessorTag(design.ID))
	if n := len(eventsFor(t, root, "work.edited", design.ID)); n != 0 {
		t.Errorf("work.edited events on the design = %d, want 0 (its tags did not change)", n)
	}
}

// doneTask files and completes a plain task, returning it.
func doneTask(t *testing.T, root, title string) *Item {
	t.Helper()
	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create %s: %v", title, err)
	}
	if _, err := Claim(root, item.ID, 4242); err != nil {
		t.Fatalf("Claim %s: %v", title, err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done %s: %v", title, err)
	}
	return item
}

// TestArchiveSuccessor_EmitsTagEditsOnBothItems: `mg archive --successor`
// shares linkSuccessorBothWays with done, so both of its tag writes log.
func TestArchiveSuccessor_EmitsTagEditsOnBothItems(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	design := doneTask(t, root, "Design")
	follow, err := Create(root, "mg-", "task", "Follow-up", nil)
	if err != nil {
		t.Fatalf("Create follow-up: %v", err)
	}
	if _, err := ArchiveItem(root, design.ID, ArchiveOpts{Successor: follow.ID}); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	assertOneTagEdit(t, root, design.ID, "", SuccessorTag(follow.ID))
	assertOneTagEdit(t, root, follow.ID, "", PredecessorTag(design.ID))
}

// TestArchivePlain_ReconciledBacklinkEmitsTagEdit: plain `mg archive <id>`
// closes the reverse link of a successor: tag the done run never reciprocated
// (here written straight to the done file, as a pre-reciprocity link would be),
// and that write logs as a tag change.
func TestArchivePlain_ReconciledBacklinkEmitsTagEdit(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	design := doneTask(t, root, "Design")
	follow, err := Create(root, "mg-", "task", "Follow-up", nil)
	if err != nil {
		t.Fatalf("Create follow-up: %v", err)
	}
	donePath := filepath.Join(root, "work", "done", design.ID+".md")
	onDisk, err := readFile(donePath)
	if err != nil {
		t.Fatalf("read done item: %v", err)
	}
	onDisk.Tags = append(onDisk.Tags, SuccessorTag(follow.ID))
	if err := os.WriteFile(donePath, []byte(Render(onDisk)), 0o644); err != nil {
		t.Fatalf("write successor tag: %v", err)
	}

	if _, err := ArchiveItem(root, design.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	assertOneTagEdit(t, root, follow.ID, "", PredecessorTag(design.ID))
	if n := len(eventsFor(t, root, "work.edited", design.ID)); n != 0 {
		t.Errorf("work.edited events on the design = %d, want 0 (its tags did not change)", n)
	}
}

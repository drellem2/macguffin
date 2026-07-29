package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// readEvents returns all events written to <root>/events.jsonl.
func readEvents(t *testing.T, root string) []event.Entry {
	t.Helper()
	entries, err := event.List(root, event.ListOpts{})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}
	return entries
}

// findEvent returns the first event with the given type, or fails the test.
func findEvent(t *testing.T, entries []event.Entry, eventType string) event.Entry {
	t.Helper()
	for _, e := range entries {
		if e.Type == eventType {
			return e
		}
	}
	t.Fatalf("no %s event found in %d entries", eventType, len(entries))
	return event.Entry{}
}

func TestEmitCreated(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "First bug", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := readEvents(t, root)
	if len(entries) != 1 {
		t.Fatalf("expected 1 event, got %d", len(entries))
	}
	e := entries[0]
	if e.Type != "work.created" {
		t.Errorf("type = %q, want work.created", e.Type)
	}
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["to_status"] != "available" {
		t.Errorf("to_status = %q, want available", e.Extra["to_status"])
	}
	if e.Extra["actor"] == "" {
		t.Error("actor should be set")
	}
	if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
		t.Errorf("ts %q not RFC3339: %v", e.Ts, err)
	}
}

func TestEmitCreatedWithDeps(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "bug", "Has deps", []string{"mg-zzzz"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.created")
	if e.Extra["to_status"] != "pending" {
		t.Errorf("to_status = %q, want pending (item has unmet deps)", e.Extra["to_status"])
	}
}

func TestEmitClaim(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Claim me", nil, WithAssignee("alice"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	pid := 12345
	if _, err := Claim(root, item.ID, pid); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.claim")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q", e.Extra["item_id"])
	}
	if e.Extra["from_status"] != "available" {
		t.Errorf("from_status = %q, want available", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "claimed" {
		t.Errorf("to_status = %q, want claimed", e.Extra["to_status"])
	}
	if e.Extra["pid"] != strconv.Itoa(pid) {
		t.Errorf("pid = %q, want %d", e.Extra["pid"], pid)
	}
	// NOT "alice". The item is assigned to alice; the claim was issued by
	// whoever ran this process, and that is what the field records (mg-3122).
	// The assertion this replaced pinned the defect: it demanded the assignee
	// and would have gone on passing while the log named the wrong agent for
	// every assigned item. See actor_test.go for the positive control.
	if e.Extra["actor"] == "alice" {
		t.Error("actor = \"alice\" — that is the ASSIGNEE, not the caller that claimed")
	}
	if e.Extra["actor"] != actor() {
		t.Errorf("actor = %q, want %q (the invoker)", e.Extra["actor"], actor())
	}
}

func TestEmitDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Finish me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	pid := 22222
	if _, err := Claim(root, item.ID, pid); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.done")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q", e.Extra["item_id"])
	}
	if e.Extra["from_status"] != "claimed" {
		t.Errorf("from_status = %q, want claimed", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "done" {
		t.Errorf("to_status = %q, want done", e.Extra["to_status"])
	}
	if e.Extra["pid"] != strconv.Itoa(pid) {
		t.Errorf("pid = %q, want %d (the claim-holder)", e.Extra["pid"], pid)
	}
}

func TestEmitReopen(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Reopen me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := Reopen(root, item.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.reopen")
	if e.Extra["from_status"] != "done" {
		t.Errorf("from_status = %q, want done", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "claimed" {
		t.Errorf("to_status = %q, want claimed", e.Extra["to_status"])
	}
}

func TestEmitShelveAndUnshelve(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Shelve me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.shelve")
	if e.Extra["from_status"] != "available" {
		t.Errorf("from_status = %q, want available", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "shelved" {
		t.Errorf("to_status = %q, want shelved", e.Extra["to_status"])
	}

	if _, err := Unshelve(root, item.ID); err != nil {
		t.Fatalf("Unshelve: %v", err)
	}

	u := findEvent(t, readEvents(t, root), "work.unshelve")
	if u.Extra["from_status"] != "shelved" {
		t.Errorf("from_status = %q, want shelved", u.Extra["from_status"])
	}
	if u.Extra["to_status"] != "available" {
		t.Errorf("to_status = %q, want available", u.Extra["to_status"])
	}
}

func TestEmitArchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Archive me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	// maxAge=0 archives everything in done/ regardless of age.
	if _, _, err := Archive(root, 0); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.archive")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["from_status"] != "done" {
		t.Errorf("from_status = %q, want done", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "archived" {
		t.Errorf("to_status = %q, want archived", e.Extra["to_status"])
	}
}

func TestEmitUnclaim(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Unclaim me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimPID := 88888
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, claimPID))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	if _, err := Unclaim(root, item.ID); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.unclaim")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q", e.Extra["item_id"])
	}
	if e.Extra["from_status"] != "claimed" {
		t.Errorf("from_status = %q, want claimed", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "available" {
		t.Errorf("to_status = %q, want available", e.Extra["to_status"])
	}
	if e.Extra["pid"] != strconv.Itoa(claimPID) {
		t.Errorf("pid = %q, want %d", e.Extra["pid"], claimPID)
	}
}

// TestEmitFullLifecycle mirrors the verification step from the task spec:
// claim then done then archive should produce three events in order.
func TestEmitFullLifecycle(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Full lifecycle", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, _, err := Archive(root, 0); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	entries := readEvents(t, root)
	if len(entries) != 4 {
		t.Fatalf("expected 4 events (created, claim, done, archive), got %d", len(entries))
	}
	wantTypes := []string{"work.created", "work.claim", "work.done", "work.archive"}
	for i, want := range wantTypes {
		if entries[i].Type != want {
			t.Errorf("event[%d].type = %q, want %q", i, entries[i].Type, want)
		}
	}
}

// TestEmitBestEffort verifies that emit failures don't break state transitions.
// We make events.jsonl unwritable (read-only directory) and confirm the claim
// still succeeds.
func TestEmitBestEffort(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Best effort", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Make events.jsonl a directory so writes fail. Append uses O_CREATE|O_APPEND|O_WRONLY,
	// which fails when the path exists as a directory.
	eventsPath := filepath.Join(root, "events.jsonl")
	// Remove the file the Create event wrote, then put a directory in its place.
	if err := os.Remove(eventsPath); err != nil {
		t.Fatalf("removing events file: %v", err)
	}
	if err := os.Mkdir(eventsPath, 0o755); err != nil {
		t.Fatalf("replacing events file with dir: %v", err)
	}

	// Claim should still succeed even though the event log is unwritable.
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Errorf("Claim should not fail when event log is unwritable: %v", err)
	}
}

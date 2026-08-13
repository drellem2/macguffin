package workitem

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPredecessorIDs mirrors the successor parser, including the trimming. A
// hand-written "predecessor: mg-1234" is the shape a human produces and the one
// a strict parser silently drops — and a dropped link is not a parse error here,
// it is a chain that reads as never having existed.
func TestPredecessorIDs(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{"none", []string{"urgent", "gh-issue"}, nil},
		{"one", []string{"predecessor:mg-1234"}, []string{"mg-1234"}},
		{"spaced", []string{"predecessor: mg-1234 "}, []string{"mg-1234"}},
		{"several keep order", []string{"predecessor:mg-b", "x", "predecessor:mg-a"}, []string{"mg-b", "mg-a"}},
		{"empty value is not a link", []string{"predecessor:"}, nil},
		{"successor tags are not predecessors", []string{"successor:mg-1234"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PredecessorIDs(&Item{Tags: tc.tags})
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("PredecessorIDs(%v) = %v, want %v", tc.tags, got, tc.want)
			}
		})
	}
}

// TestLinkPredecessorRefusesASelfLink: checkSuccessorTarget already refuses a
// self-successor on the write path, so this catches one that reached the store
// some other way and declines to make it reciprocal. Writing "predecessor:X" onto
// X would assert that an item preceded itself — a statement with no true reading,
// recorded as though someone had meant it.
func TestLinkPredecessorRefusesASelfLink(t *testing.T) {
	root := t.TempDir()
	if err := linkPredecessor(root, "mg-1234", "mg-1234"); err == nil {
		t.Fatal("linkPredecessor accepted a self-link")
	}
}

// TestReconcileBacklinksReportsWhatItCouldNotWrite is the control for the
// remedy's own version of the reported defect. A reverse link that fails must
// not be silent: a reader who later finds a one-directional chain has to be able
// to tell "nothing tried" from "something tried and failed", and that difference
// is the whole of mg-3386.
func TestReconcileBacklinksReportsWhatItCouldNotWrite(t *testing.T) {
	root := t.TempDir()
	mustInitStore(t, root)

	var buf bytes.Buffer
	restore := backlinkNotice
	backlinkNotice = &buf
	t.Cleanup(func() { backlinkNotice = restore })

	item := &Item{ID: "mg-aaaa", Tags: []string{"successor:mg-ffff"}}
	reconcileBacklinks(root, item) // must not panic, must not return anything to undo

	note := buf.String()
	if !strings.Contains(note, "mg-ffff") || !strings.Contains(note, "mg-aaaa") {
		t.Errorf("note = %q, want both ends of the link named", note)
	}
	if !strings.Contains(note, "reverse link could not be recorded") {
		t.Errorf("note = %q, want it to say what failed", note)
	}

	// The durable half. A note is true when printed and gone by the time anyone
	// asks, which is the thing the ticket was filed about.
	events, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatalf("no event log written: %v", err)
	}
	if !strings.Contains(string(events), "work.backlink_failed") {
		t.Errorf("events.jsonl = %q, want a work.backlink_failed entry", events)
	}
	if !strings.Contains(string(events), "mg-ffff") {
		t.Errorf("events.jsonl = %q, want the unreachable end recorded", events)
	}
}

// TestReconcileBacklinksIsSilentWhenThereIsNothingToDo: an item with no
// successor: tags is the overwhelming majority of completions. A note printed
// for them is how a warning becomes wallpaper — the reasoning lasttouch.go
// gives for suppressing its own note.
func TestReconcileBacklinksIsSilentWhenThereIsNothingToDo(t *testing.T) {
	root := t.TempDir()
	mustInitStore(t, root)

	var buf bytes.Buffer
	restore := backlinkNotice
	backlinkNotice = &buf
	t.Cleanup(func() { backlinkNotice = restore })

	reconcileBacklinks(root, &Item{ID: "mg-aaaa", Tags: []string{"urgent"}})

	if buf.Len() != 0 {
		t.Errorf("note = %q on an item with no links, want silence", buf.String())
	}
}

// mustInitStore lays down the directory layout the writers expect.
func mustInitStore(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"available", "claimed", "done", "pending"} {
		if err := os.MkdirAll(filepath.Join(root, "work", d), 0o755); err != nil {
			t.Fatalf("seeding store: %v", err)
		}
	}
}

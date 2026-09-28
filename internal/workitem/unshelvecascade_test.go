package workitem

import (
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
)

// mg unshelve restores a shelved dependent only when it was shelved WITH the
// chain being restored (mg-3066c). These pin the three cases the rule has to
// answer: a cascade-shelved dependent comes back, an independently shelved one
// stays and is reported, and one filed onto an already-shelved parent — which
// has no work.shelve of its own — comes back with the parent.

func wantStatusIs(t *testing.T, root, id, want string) {
	t.Helper()
	if st, err := Status(root, id); err != nil || st != want {
		t.Errorf("%s status = %q (err %v), want %q", id, st, err, want)
	}
}

func restoredIDs(items []*Item) []string {
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// (a) and (b): chain A <- B <- C, plus X depending on A and shelved on its own
// before A was. Unshelving A brings back A, B and C, and leaves X — named.
func TestUnshelveLeavesIndependentlyShelvedDependent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	a, _ := Create(root, "mg-", "task", "A", nil)
	b, _ := Create(root, "mg-", "task", "B", []string{a.ID})
	c, _ := Create(root, "mg-", "task", "C", []string{b.ID})
	x, _ := Create(root, "mg-", "task", "X", []string{a.ID})

	if _, err := Shelve(root, x.ID); err != nil {
		t.Fatalf("Shelve X: %v", err)
	}
	if _, err := Shelve(root, a.ID); err != nil {
		t.Fatalf("Shelve A: %v", err)
	}
	for _, id := range []string{a.ID, b.ID, c.ID, x.ID} {
		wantStatusIs(t, root, id, "shelved")
	}

	res, err := UnshelveReport(root, a.ID)
	if err != nil {
		t.Fatalf("UnshelveReport: %v", err)
	}
	got := restoredIDs(res.Restored)
	if len(got) != 3 || got[0] != a.ID {
		t.Fatalf("restored %v, want A first then B and C", got)
	}
	wantStatusIs(t, root, a.ID, "available")
	wantStatusIs(t, root, b.ID, "pending")
	wantStatusIs(t, root, c.ID, "pending")
	wantStatusIs(t, root, x.ID, "shelved")

	if len(res.Left) != 1 || res.Left[0].Item.ID != x.ID {
		t.Fatalf("left = %+v, want exactly X", res.Left)
	}
	if res.Left[0].Reason != "shelved on its own" {
		t.Errorf("reason = %q, want %q", res.Left[0].Reason, "shelved on its own")
	}

	// work.unshelve names the restore that pulled each dependent in, mirroring
	// work.shelve.
	entries, err := event.List(root, event.ListOpts{Type: "work.unshelve"})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}
	from := map[string]string{}
	for _, e := range entries {
		v, ok := e.Extra["cascaded_from"]
		if !ok {
			v = "<absent>"
		}
		from[e.Extra["item_id"]] = v
	}
	want := map[string]string{a.ID: "<absent>", b.ID: a.ID, c.ID: b.ID}
	for id, w := range want {
		if from[id] != w {
			t.Errorf("work.unshelve for %s cascaded_from = %q, want %q", id, from[id], w)
		}
	}
	if _, ok := from[x.ID]; ok {
		t.Errorf("X has a work.unshelve, but it was left shelved")
	}

	// The operator can still restore X on its own.
	if _, err := Unshelve(root, x.ID); err != nil {
		t.Fatalf("Unshelve X: %v", err)
	}
	wantStatusIs(t, root, x.ID, "pending")
}

// A dependent shelved by ANOTHER parent's cascade stays when this parent comes
// back: its hold came from the other parent, which is still shelved.
func TestUnshelveLeavesDependentShelvedWithAnotherParent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	p, _ := Create(root, "mg-", "task", "P", nil)
	q, _ := Create(root, "mg-", "task", "Q", nil)
	d, _ := Create(root, "mg-", "task", "D", []string{p.ID, q.ID})

	if _, err := Shelve(root, q.ID); err != nil { // cascades D, from Q
		t.Fatalf("Shelve Q: %v", err)
	}
	if _, err := Shelve(root, p.ID); err != nil {
		t.Fatalf("Shelve P: %v", err)
	}

	res, err := UnshelveReport(root, p.ID)
	if err != nil {
		t.Fatalf("UnshelveReport: %v", err)
	}
	wantStatusIs(t, root, d.ID, "shelved")
	if len(res.Left) != 1 || !strings.Contains(res.Left[0].Reason, q.ID) {
		t.Fatalf("left = %+v, want D naming %s", res.Left, q.ID)
	}

	if _, err := Unshelve(root, q.ID); err != nil {
		t.Fatalf("Unshelve Q: %v", err)
	}
	wantStatusIs(t, root, d.ID, "pending")
}

// A dependent the cascade reached through ANOTHER chain member comes back even
// when it is first looked at from a member it was not shelved with.
func TestUnshelveRestoresDiamondDependent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	a, _ := Create(root, "mg-", "task", "A", nil)
	b, _ := Create(root, "mg-", "task", "B", []string{a.ID})
	d, _ := Create(root, "mg-", "task", "D", []string{a.ID, b.ID})

	if _, err := Shelve(root, a.ID); err != nil {
		t.Fatalf("Shelve A: %v", err)
	}
	res, err := UnshelveReport(root, a.ID)
	if err != nil {
		t.Fatalf("UnshelveReport: %v", err)
	}
	wantStatusIs(t, root, b.ID, "pending")
	wantStatusIs(t, root, d.ID, "pending")
	if len(res.Restored) != 3 || len(res.Left) != 0 {
		t.Fatalf("restored %v, left %+v; want 3 restored, none left", restoredIDs(res.Restored), res.Left)
	}
}

// (c): an item filed onto an already-shelved parent lands shelved with no
// work.shelve of its own. It was never held — only born under the hold — so it
// comes back with the parent (README, "Depending on a shelved item").
func TestUnshelveRestoresDependentFiledUnderShelvedParent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	p, _ := Create(root, "mg-", "task", "P", nil)
	if _, err := Shelve(root, p.ID); err != nil {
		t.Fatalf("Shelve P: %v", err)
	}
	k, err := Create(root, "mg-", "task", "filed under the hold", []string{p.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantStatusIs(t, root, k.ID, "shelved")

	res, err := UnshelveReport(root, p.ID)
	if err != nil {
		t.Fatalf("UnshelveReport: %v", err)
	}
	wantStatusIs(t, root, k.ID, "pending")
	if len(res.Restored) != 2 || len(res.Left) != 0 {
		t.Fatalf("restored %v, left %+v; want P and the filed item, none left", restoredIDs(res.Restored), res.Left)
	}
}

// An item shelved on its own and then restored has a stale work.shelve; if it
// is back on the shelf with no newer work.shelve, that old record says nothing
// about its current stay and must not keep it behind.
func TestUnshelveIgnoresShelveRecordSupersededByUnshelve(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	p, _ := Create(root, "mg-", "task", "P", nil)
	k, _ := Create(root, "mg-", "task", "K", nil)
	event.Emit(root, "work.shelve", map[string]string{"item_id": k.ID, "dependents": ""})
	event.Emit(root, "work.unshelve", map[string]string{"item_id": k.ID})
	st := &unshelveRun{records: currentShelveRecords(root), chain: map[string]bool{p.ID: true}}
	if ok, why := st.shelvedWithChain(k.ID); !ok {
		t.Errorf("superseded record kept K behind: %s", why)
	}
}

// A work.shelve written before cascades were recorded (no `dependents` field)
// cannot tell a cascade from a deliberate shelve; the dependent stays, and the
// reason says the record is too old rather than claiming it was deliberate.
func TestUnshelveLeavesDependentWithPreTrackingRecord(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	k, _ := Create(root, "mg-", "task", "K", nil)
	event.Emit(root, "work.shelve", map[string]string{"item_id": k.ID, "from_status": "available"})
	st := &unshelveRun{records: currentShelveRecords(root), chain: map[string]bool{}}
	ok, why := st.shelvedWithChain(k.ID)
	if ok || !strings.Contains(why, "predates") {
		t.Errorf("got (%v, %q), want left with a predates reason", ok, why)
	}
}

// --tag reports the dependents it left, once, and never a tagged item.
func TestUnshelveByTagReportsLeftDependents(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	a, _ := Create(root, "mg-", "task", "A", nil, WithTags([]string{"grp"}))
	x, _ := Create(root, "mg-", "task", "X", []string{a.ID})
	if _, err := Shelve(root, x.ID); err != nil {
		t.Fatalf("Shelve X: %v", err)
	}
	if _, err := Shelve(root, a.ID); err != nil {
		t.Fatalf("Shelve A: %v", err)
	}
	res, skipped, err := UnshelveByTagReport(root, "grp")
	if err != nil || len(skipped) != 0 {
		t.Fatalf("UnshelveByTagReport: err %v, skipped %+v", err, skipped)
	}
	wantStatusIs(t, root, x.ID, "shelved")
	if len(res.Left) != 1 || res.Left[0].Item.ID != x.ID {
		t.Fatalf("left = %+v, want X", res.Left)
	}
}

package fsstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
	"github.com/drellem2/macguffin/store/storetest"
)

// newStore lays out an empty store the way `mg init` does.
func newStore(t *testing.T) store.Backend {
	t.Helper()
	root := t.TempDir()
	for _, st := range append(store.ActiveStatuses, store.Archived) {
		if err := os.MkdirAll(fsstore.StatusDir(root, st), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b, err := fsstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestContract(t *testing.T) {
	storetest.Run(t, newStore)
}

// The filesystem layout is the compatibility promise: a claim is
// claimed/<id>.md.<pid>, carrying the Actor's PID and nothing else.
func TestClaimLayoutIsUnchanged(t *testing.T) {
	b := newStore(t)
	root := b.(*fsstore.FS).Root
	if err := b.Create("mg-a1b2c", store.State{Status: store.Available}, []byte("---\nid: mg-a1b2c\n---\n"), store.Actor{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", "mg-a1b2c.md")); err != nil {
		t.Fatalf("created item not at available/<id>.md: %v", err)
	}
	by := store.Actor{Name: "x", Host: "h", PID: 4321, Nonce: "n"}
	if err := b.Transition("mg-a1b2c", store.State{Status: store.Available}, store.State{Status: store.Claimed}, by); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "claimed", "mg-a1b2c.md.4321")); err != nil {
		t.Fatalf("claim not at claimed/<id>.md.<pid>: %v", err)
	}
	d, err := b.Get("mg-a1b2c")
	if err != nil {
		t.Fatal(err)
	}
	if d.State.Holder != "4321" {
		t.Fatalf("Holder = %q, want the PID", d.State.Holder)
	}
}

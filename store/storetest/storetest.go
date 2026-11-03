// Package storetest is the contract every store.Backend must pass. A backend's
// own test calls Run with a factory that returns a fresh, empty, initialised
// store per call:
//
//	func TestContract(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) store.Backend { return newEmptyStore(t) })
//	}
//
// The cases are written against store's documented semantics, not against any
// one backend's layout, so the filesystem backend and a shared backend are held
// to the same promises — above all the atomic one: of two concurrent
// Transitions out of the same state, exactly one wins and the other gets
// *store.ErrLost.
package storetest

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/drellem2/macguffin/store"
)

// Factory returns a fresh, empty store ready for Create.
type Factory func(t *testing.T) store.Backend

// RaceRounds is how many times the concurrent-transition case is repeated. A
// race that is lost once in a hundred tries is still a lost race.
var RaceRounds = 50

// Racers is how many goroutines contend for one item per round.
var Racers = 8

func actor(n int) store.Actor {
	return store.Actor{Name: fmt.Sprintf("racer-%d", n), Host: "storetest", PID: 10000 + n, Nonce: fmt.Sprintf("n%d", n)}
}

var me = actor(0)

func doc(id, body string) []byte {
	return []byte("---\nid: " + id + "\ntype: task\n---\n# " + body + "\n")
}

func mustCreate(t *testing.T, b store.Backend, id string, st store.State) []byte {
	t.Helper()
	c := doc(id, "item "+id)
	if err := b.Create(id, st, c, me); err != nil {
		t.Fatalf("Create(%s, %s): %v", id, st, err)
	}
	return c
}

func mustGet(t *testing.T, b store.Backend, id string) *store.Doc {
	t.Helper()
	d, err := b.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return d
}

func wantLost(t *testing.T, err error, what string) *store.ErrLost {
	t.Helper()
	var lost *store.ErrLost
	if !errors.As(err, &lost) {
		t.Fatalf("%s: want *store.ErrLost, got %v", what, err)
	}
	return lost
}

func listIDs(t *testing.T, b store.Backend, st store.Status) map[string]store.Doc {
	t.Helper()
	docs, err := b.List(st)
	if err != nil {
		t.Fatalf("List(%s): %v", st, err)
	}
	out := map[string]store.Doc{}
	for _, d := range docs {
		if _, dup := out[d.ID]; dup {
			t.Fatalf("List(%s) returned %s twice", st, d.ID)
		}
		out[d.ID] = d
	}
	return out
}

var (
	available = store.State{Status: store.Available}
	pending   = store.State{Status: store.Pending}
	claimed   = store.State{Status: store.Claimed}
	done      = store.State{Status: store.Done}
)

// Run runs the whole contract against backends from f.
func Run(t *testing.T, f Factory) {
	t.Run("CreateGetRoundTrip", func(t *testing.T) {
		b := f(t)
		c := mustCreate(t, b, "st-a0001", available)
		d := mustGet(t, b, "st-a0001")
		if d.ID != "st-a0001" || d.State.Status != store.Available {
			t.Fatalf("Get: got id=%s state=%s", d.ID, d.State)
		}
		if !bytes.Equal(d.Content, c) {
			t.Fatalf("Get content:\n got %q\nwant %q", d.Content, c)
		}
		if d.Hash != store.HashOf(c) {
			t.Fatalf("Get hash %s, want HashOf(content) %s", d.Hash, store.HashOf(c))
		}
	})

	t.Run("GetMissingIsNotFound", func(t *testing.T) {
		b := f(t)
		if _, err := b.Get("st-nope0"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Get missing: want ErrNotFound, got %v", err)
		}
	})

	t.Run("CreateRefusesAnIDAnywhereInTheStore", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-b0001", available)
		// Same id into a DIFFERENT status: the collision scan is store-wide,
		// not per directory.
		if err := b.Create("st-b0001", pending, doc("st-b0001", "twin"), me); !errors.Is(err, store.ErrExists) {
			t.Fatalf("Create duplicate into pending: want ErrExists, got %v", err)
		}
		// And against an archived record.
		mustCreate(t, b, "st-b0002", available)
		if err := b.Transition("st-b0002", available, claimed, me); err != nil {
			t.Fatal(err)
		}
		if err := b.Transition("st-b0002", claimed, done, me); err != nil {
			t.Fatal(err)
		}
		if err := b.Archive("st-b0002", "2026-11", me); err != nil {
			t.Fatalf("Archive: %v", err)
		}
		if err := b.Create("st-b0002", available, doc("st-b0002", "reborn"), me); !errors.Is(err, store.ErrExists) {
			t.Fatalf("Create over an archived id: want ErrExists, got %v", err)
		}
		// The original must be untouched.
		if d := mustGet(t, b, "st-b0001"); !bytes.Contains(d.Content, []byte("item st-b0001")) {
			t.Fatalf("a refused Create changed the existing item: %q", d.Content)
		}
	})

	t.Run("ListByStatus", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-c0001", available)
		mustCreate(t, b, "st-c0002", available)
		mustCreate(t, b, "st-c0003", pending)
		if err := b.Transition("st-c0002", available, claimed, me); err != nil {
			t.Fatal(err)
		}
		av := listIDs(t, b, store.Available)
		if _, ok := av["st-c0001"]; !ok || len(av) != 1 {
			t.Fatalf("List(available) = %v, want just st-c0001", keys(av))
		}
		if cl := listIDs(t, b, store.Claimed); len(cl) != 1 || cl["st-c0002"].State.Status != store.Claimed {
			t.Fatalf("List(claimed) = %v, want just st-c0002", keys(cl))
		}
		if pe := listIDs(t, b, store.Pending); len(pe) != 1 {
			t.Fatalf("List(pending) = %v, want just st-c0003", keys(pe))
		}
	})

	t.Run("TransitionMovesAndRecordsHolder", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-d0001", available)
		if err := b.Transition("st-d0001", available, claimed, actor(3)); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		d := mustGet(t, b, "st-d0001")
		if d.State.Status != store.Claimed || d.State.Holder == "" {
			t.Fatalf("after claim, state = %q; want claimed with a holder", d.State)
		}
		// Expecting the actual holder succeeds; expecting another refuses.
		other := store.State{Status: store.Claimed, Holder: d.State.Holder + "x"}
		lost := wantLost(t, b.Transition("st-d0001", other, available, me), "Transition with the wrong holder")
		if lost.Current == nil || lost.Current.Holder != d.State.Holder {
			t.Fatalf("ErrLost.Current = %v, want it to name holder %q", lost.Current, d.State.Holder)
		}
		if err := b.Transition("st-d0001", d.State, done, me); err != nil {
			t.Fatalf("Transition by the actual holder: %v", err)
		}
	})

	t.Run("TransitionFromTheWrongStateIsLost", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-e0001", pending)
		before := mustGet(t, b, "st-e0001")
		lost := wantLost(t, b.Transition("st-e0001", available, claimed, me), "claim of a pending item")
		if lost.Current == nil || lost.Current.Status != store.Pending {
			t.Fatalf("ErrLost.Current = %v, want pending", lost.Current)
		}
		after := mustGet(t, b, "st-e0001")
		if after.State != before.State || after.Hash != before.Hash {
			t.Fatalf("a refused Transition changed the item: %v -> %v", before.State, after.State)
		}
	})

	t.Run("TransitionOfAMissingItemIsLostWithNoCurrent", func(t *testing.T) {
		b := f(t)
		lost := wantLost(t, b.Transition("st-f0000", available, claimed, me), "claim of a missing item")
		if lost.Current != nil {
			t.Fatalf("ErrLost.Current = %v, want nil for a missing item", *lost.Current)
		}
	})

	t.Run("ConcurrentTransitionsExactlyOneWins", func(t *testing.T) {
		b := f(t)
		for round := 0; round < RaceRounds; round++ {
			id := fmt.Sprintf("st-r%04d", round)
			mustCreate(t, b, id, available)
			errs := make([]error, Racers)
			var start, wg sync.WaitGroup
			start.Add(1)
			for i := 0; i < Racers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					start.Wait()
					errs[i] = b.Transition(id, available, claimed, actor(i+1))
				}(i)
			}
			start.Done()
			wg.Wait()

			winners := 0
			for i, err := range errs {
				if err == nil {
					winners++
					continue
				}
				var lost *store.ErrLost
				if !errors.As(err, &lost) {
					t.Fatalf("round %d racer %d: loser got %v, want *store.ErrLost", round, i, err)
				}
			}
			if winners != 1 {
				t.Fatalf("round %d: %d of %d concurrent claims succeeded; exactly one must", round, winners, Racers)
			}
			d := mustGet(t, b, id) // also proves the item is in exactly one place
			if d.State.Status != store.Claimed {
				t.Fatalf("round %d: after the race the item is %s", round, d.State)
			}
		}
	})

	t.Run("EditIfUnchanged", func(t *testing.T) {
		b := f(t)
		c := mustCreate(t, b, "st-g0001", available)
		v2 := doc("st-g0001", "second version")
		if err := b.Edit("st-g0001", store.HashOf(c), v2, me); err != nil {
			t.Fatalf("Edit with the current hash: %v", err)
		}
		v3 := doc("st-g0001", "third version")
		err := b.Edit("st-g0001", store.HashOf(c), v3, me) // stale hash
		var changed *store.ErrChanged
		if !errors.As(err, &changed) {
			t.Fatalf("Edit with a stale hash: want *store.ErrChanged, got %v", err)
		}
		if d := mustGet(t, b, "st-g0001"); !bytes.Equal(d.Content, v2) {
			t.Fatalf("a refused Edit wrote: %q", d.Content)
		}
		if err := b.Edit("st-g0001", "", v3, me); err != nil {
			t.Fatalf("unguarded Edit: %v", err)
		}
		if d := mustGet(t, b, "st-g0001"); !bytes.Equal(d.Content, v3) {
			t.Fatalf("unguarded Edit did not land: %q", d.Content)
		}
	})

	// Edit can never change state: only Transition does. A backend that keeps
	// state inside the document must preserve it whatever the new content
	// says, including content that spells out a different state.
	t.Run("EditNeverChangesState", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-h0001", available)
		if err := b.Transition("st-h0001", available, claimed, actor(5)); err != nil {
			t.Fatal(err)
		}
		before := mustGet(t, b, "st-h0001")
		forged := []byte("---\nid: st-h0001\nstate: done\nstatus: done\ntype: task\n---\n# forged\n")
		if err := b.Edit("st-h0001", before.Hash, forged, me); err != nil {
			t.Fatalf("Edit: %v", err)
		}
		after := mustGet(t, b, "st-h0001")
		if after.State != before.State {
			t.Fatalf("Edit changed state %s -> %s; only Transition may", before.State, after.State)
		}
		if len(listIDs(t, b, store.Done)) != 0 {
			t.Fatalf("Edit content naming a state put the item in done")
		}
	})

	// The mg-698a8 regression at the interface level: a Transition that lands
	// between an editor's read and its write must not be undone or duplicated
	// by the write.
	t.Run("EditAfterTransitionNeverRecreatesTheOldState", func(t *testing.T) {
		b := f(t)
		c := mustCreate(t, b, "st-i0001", available)
		read := store.HashOf(c) // the editor's read
		if err := b.Transition("st-i0001", available, claimed, actor(7)); err != nil {
			t.Fatal(err)
		}
		_ = b.Edit("st-i0001", read, doc("st-i0001", "edited"), me) // may follow or refuse
		if av := listIDs(t, b, store.Available); len(av) != 0 {
			t.Fatalf("the Edit re-created the item in available: %v", keys(av))
		}
		if d := mustGet(t, b, "st-i0001"); d.State.Status != store.Claimed {
			t.Fatalf("after the Edit the item is %s, want claimed", d.State)
		}
	})

	t.Run("ArchiveOnlyFromDone", func(t *testing.T) {
		b := f(t)
		mustCreate(t, b, "st-j0001", available)
		wantLost(t, b.Archive("st-j0001", "2026-11", me), "Archive of an available item")
		if err := b.Transition("st-j0001", available, claimed, me); err != nil {
			t.Fatal(err)
		}
		if err := b.Transition("st-j0001", claimed, done, me); err != nil {
			t.Fatal(err)
		}
		if err := b.Archive("st-j0001", "2026-11", me); err != nil {
			t.Fatalf("Archive: %v", err)
		}
		d := mustGet(t, b, "st-j0001")
		if d.State.Status != store.Archived || d.Partition != "2026-11" {
			t.Fatalf("after Archive: state=%s partition=%q", d.State, d.Partition)
		}
		if ar := listIDs(t, b, store.Archived); ar["st-j0001"].Partition != "2026-11" {
			t.Fatalf("List(archived) = %v", keys(ar))
		}
		if len(listIDs(t, b, store.Done)) != 0 {
			t.Fatalf("archived item still listed as done")
		}
	})

	t.Run("EventsAppendAndRead", func(t *testing.T) {
		b := f(t)
		log := b.Events()
		info, err := log.StatEvents()
		if err != nil {
			t.Fatalf("StatEvents on an empty store: %v", err)
		}
		if info.Exists && info.Size != 0 {
			t.Fatalf("fresh store already has %d bytes of events", info.Size)
		}
		lines := []string{`{"type":"a"}` + "\n", `{"type":"b"}` + "\n"}
		for _, l := range lines {
			if err := log.AppendEvent([]byte(l)); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}
		}
		got, err := log.ReadEvents()
		if err != nil {
			t.Fatalf("ReadEvents: %v", err)
		}
		if string(got) != lines[0]+lines[1] {
			t.Fatalf("ReadEvents = %q", got)
		}
		if info, err := log.StatEvents(); err != nil || !info.Exists || info.Size == 0 {
			t.Fatalf("StatEvents after appends = %+v, %v", info, err)
		}
	})
}

func keys(m map[string]store.Doc) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

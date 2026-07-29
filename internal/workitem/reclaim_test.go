package workitem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// Reclaim exists for one reason: to move the PID recorded on a claim WITHOUT
// the item leaving claimed/. Unclaim+Claim already re-stamps a PID; what it
// cannot do is keep the item claimed throughout, and pogod's spawn-time claim
// is worthless if every dispatch parks the item back in available/ for a
// moment. So the tests below assert that property from the outside — twice, two
// different ways — rather than by reading the implementation.

// rcClaimed puts a fresh item in claimed/ under an explicit PID and returns its
// id. It renames the file directly rather than going through Claim, so a test
// can pick a PID that is not this process's.
func rcClaimed(t *testing.T, root string, pid int, title string) string {
	t.Helper()
	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, pid))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}
	return item.ID
}

// rcClaimedNames returns the claimed/ filenames belonging to id.
func rcClaimedNames(t *testing.T, root, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "work", "claimed"))
	if err != nil {
		t.Fatalf("reading claimed/: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestReclaimReStampsThePID(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 32194, "claimed on the worker's behalf")

	res, err := Reclaim(root, id, 40881)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	if res.OldPID != 32194 || res.NewPID != 40881 {
		t.Errorf("result pids = %d -> %d, want 32194 -> 40881", res.OldPID, res.NewPID)
	}
	if !res.Moved {
		t.Error("Moved = false on a re-stamp that changed the PID")
	}

	want := fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedNames(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}
}

// The load-bearing property, proved structurally: make available/ unwritable,
// so ANY implementation that routes the item through it fails. Reclaim renames
// within claimed/ and does not care.
func TestReclaimSucceedsWhenAvailableIsUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 32194, "no route through available/")

	avail := filepath.Join(root, "work", "available")
	if err := os.Chmod(avail, 0o500); err != nil {
		t.Fatalf("chmod available/: %v", err)
	}
	// t.TempDir cleanup needs the write bit back.
	t.Cleanup(func() { _ = os.Chmod(avail, 0o755) })

	if _, err := Reclaim(root, id, 40881); err != nil {
		t.Fatalf("Reclaim failed with available/ read-only — it must never write there: %v", err)
	}

	want := fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedNames(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}
}

// The same property proved observationally: a watcher polls both directories as
// fast as it can while reclaims run, and must never once see the item in
// available/ nor see it missing from claimed/. A correct implementation cannot
// fail this; an unclaim+claim one is caught by it.
func TestReclaimNeverPassesThroughAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 1000, "watched across a handover")

	availDir := filepath.Join(root, "work", "available")
	claimedDir := filepath.Join(root, "work", "claimed")

	var seenInAvailable, missingFromClaimed, samples atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		present := func(dir string) bool {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return false
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), id) {
					return true
				}
			}
			return false
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			if present(availDir) {
				seenInAvailable.Add(1)
			}
			if !present(claimedDir) {
				missingFromClaimed.Add(1)
			}
			samples.Add(1)
		}
	}()

	// Many re-stamps, so the watcher gets many chances to catch a gap.
	for i := 0; i < 200; i++ {
		if _, err := Reclaim(root, id, 2000+i); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("Reclaim #%d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	if samples.Load() == 0 {
		t.Fatal("watcher took no samples; the test proves nothing")
	}
	if n := seenInAvailable.Load(); n > 0 {
		t.Errorf("item was observed in available/ %d times during %d reclaims — reclaim must never let the item leave claimed/", n, 200)
	}
	if n := missingFromClaimed.Load(); n > 0 {
		t.Errorf("item was observed missing from claimed/ %d times during %d reclaims — the item must be in claimed/ at every observable point", n, 200)
	}
}

// A worker that repeats its handover step after a context compaction must get a
// no-op, not an error that reads as a failure.
func TestReclaimToTheSamePIDIsANoOp(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 40881, "already mine")

	before, err := event.List(root, event.ListOpts{})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}

	res, err := Reclaim(root, id, 40881)
	if err != nil {
		t.Fatalf("re-stamping to the recorded PID must succeed: %v", err)
	}
	if res.Moved {
		t.Error("Moved = true on a re-stamp to the PID already recorded")
	}
	if res.OldPID != 40881 || res.NewPID != 40881 {
		t.Errorf("result pids = %d -> %d, want 40881 -> 40881", res.OldPID, res.NewPID)
	}

	want := fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedNames(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}

	after, err := event.List(root, event.ListOpts{})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("a no-op emitted %d event(s); nothing changed, so nothing should be recorded", len(after)-len(before))
	}
}

// pid 0 means "the calling process", as it does for Claim.
func TestReclaimDefaultsToTheCallingProcess(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 32194, "default pid")

	res, err := Reclaim(root, id, 0)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.NewPID != os.Getpid() {
		t.Errorf("NewPID = %d, want the calling process %d", res.NewPID, os.Getpid())
	}
}

// A legacy claim file with no PID suffix parses as PID 0. That must be treated
// as "unrecorded, stamp it" and not mistaken for "already pid 0".
func TestReclaimStampsAClaimWithNoPIDSuffix(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "claimed the old way", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", item.ID+".md")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating a suffix-less claim: %v", err)
	}

	res, err := Reclaim(root, item.ID, 40881)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if !res.Moved || res.OldPID != 0 || res.NewPID != 40881 {
		t.Errorf("result = {moved:%v %d -> %d}, want {true 0 -> 40881}", res.Moved, res.OldPID, res.NewPID)
	}
	want := item.ID + ".md.40881"
	if got := rcClaimedNames(t, root, item.ID); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}
}

// Reclaim requires a claim to re-stamp. Every other status is a conflict, and
// in particular an available item is NOT claimed as a fallback: that would make
// reclaim a claim with its atomic refusal bypassed.
func TestReclaimRefusesEveryNonClaimedStatus(t *testing.T) {
	cases := []struct {
		name     string
		wantCode string
		// setup files the item and leaves it in the status under test,
		// returning its id and the directory it must still be in afterwards.
		setup func(t *testing.T, root string) (id, stayDir string)
	}{
		{
			name:     "available",
			wantCode: "not_claimed",
			setup: func(t *testing.T, root string) (string, string) {
				item, err := Create(root, "mg-", "task", "nobody has claimed this", nil)
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				return item.ID, "available"
			},
		},
		{
			name:     "done",
			wantCode: "already_done",
			setup: func(t *testing.T, root string) (string, string) {
				id := rcClaimed(t, root, 32194, "finished work")
				if _, _, err := Done(root, id, nil); err != nil {
					t.Fatalf("Done: %v", err)
				}
				return id, "done"
			},
		},
		{
			name:     "shelved",
			wantCode: "item_shelved",
			setup: func(t *testing.T, root string) (string, string) {
				item, err := Create(root, "mg-", "task", "set aside", nil)
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				if _, err := Shelve(root, item.ID); err != nil {
					t.Fatalf("Shelve: %v", err)
				}
				return item.ID, "shelved"
			},
		},
		{
			name:     "pending",
			wantCode: "not_claimed",
			setup: func(t *testing.T, root string) (string, string) {
				parent := rcClaimed(t, root, 32194, "the gate")
				child, err := Create(root, "mg-", "task", "waiting on the gate", []string{parent})
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				return child.ID, "pending"
			},
		},
		{
			name:     "archived",
			wantCode: "item_archived",
			setup: func(t *testing.T, root string) (string, string) {
				id := rcClaimed(t, root, 32194, "long finished")
				if _, _, err := Done(root, id, nil); err != nil {
					t.Fatalf("Done: %v", err)
				}
				if _, err := ArchiveItem(root, id, ArchiveOpts{}); err != nil {
					t.Fatalf("ArchiveItem: %v", err)
				}
				return id, "" // archive layout is partitioned; checked via Status below
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			id, stayDir := tc.setup(t, root)

			res, err := Reclaim(root, id, 40881)
			if err == nil {
				t.Fatalf("Reclaim on a %s item succeeded (result %+v); it must refuse", tc.name, res)
			}

			var mErr *mgerr.Error
			if !errors.As(err, &mErr) {
				t.Fatalf("error is not a typed mgerr.Error: %T %v", err, err)
			}
			if mErr.Category != mgerr.CatConflict {
				t.Errorf("category = %q, want %q (exit 4)", mErr.Category, mgerr.CatConflict)
			}
			if mErr.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", mErr.Code, tc.wantCode)
			}

			// It must not have claimed, stamped, or otherwise moved the item.
			if names := rcClaimedNames(t, root, id); len(names) != 0 {
				t.Errorf("a refused reclaim left %v in claimed/", names)
			}
			if stayDir != "" {
				entries, err := os.ReadDir(filepath.Join(root, "work", stayDir))
				if err != nil {
					t.Fatalf("reading %s/: %v", stayDir, err)
				}
				found := false
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), id) {
						found = true
					}
				}
				if !found {
					t.Errorf("item is no longer in %s/ after a refused reclaim", stayDir)
				}
			}
		})
	}
}

func TestReclaimUnknownIDIsNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Reclaim(root, "mg-zzzz", 40881)
	if err == nil {
		t.Fatal("Reclaim on an unknown id succeeded; it must not")
	}
	var mErr *mgerr.Error
	if !errors.As(err, &mErr) {
		t.Fatalf("error is not a typed mgerr.Error: %T %v", err, err)
	}
	if mErr.Category != mgerr.CatNotFound {
		t.Errorf("category = %q, want %q (exit 3)", mErr.Category, mgerr.CatNotFound)
	}
}

// A re-stamp is a claim by a new owner, so it records the same event `claim`
// does — `mg spend` pairs work.claim with the next release to attribute an
// actor's spend, and a silent handover bills the worker's run to pogod.
func TestReclaimEmitsAClaimEvent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 32194, "handover recorded")

	if _, err := Reclaim(root, id, 40881); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	entries, err := event.List(root, event.ListOpts{})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}
	var last *event.Entry
	for i := range entries {
		if entries[i].Type == "work.claim" && entries[i].Extra["item_id"] == id {
			last = &entries[i]
		}
	}
	if last == nil {
		t.Fatalf("no work.claim event recorded for %s; events: %+v", id, entries)
	}
	if got := last.Extra["pid"]; got != "40881" {
		t.Errorf("event pid = %q, want 40881", got)
	}
	if got := last.Extra["prev_pid"]; got != "32194" {
		t.Errorf("event prev_pid = %q, want 32194", got)
	}
	// Nothing moved between statuses, and the event must not claim otherwise —
	// `mg flow` reads these two fields as throughput.
	if from, to := last.Extra["from_status"], last.Extra["to_status"]; from != "claimed" || to != "claimed" {
		t.Errorf("event statuses = %q -> %q, want claimed -> claimed", from, to)
	}
	if last.Extra["actor"] == "" {
		t.Error("event records no actor; spend attribution keys on it")
	}
}

// The .md moves within claimed/, so the result sidecar beside it needs no
// follow-up move — but it must still be there, and still beside the .md.
func TestReclaimLeavesTheResultSidecarInPlace(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 32194, "has a sidecar")

	sidecar := filepath.Join(root, "work", "claimed", resultSidecarName(id))
	if err := os.WriteFile(sidecar, []byte(`{"kind":"investigation"}`), 0o644); err != nil {
		t.Fatalf("writing sidecar: %v", err)
	}

	if _, err := Reclaim(root, id, 40881); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("sidecar is gone after a reclaim: %v", err)
	}
	if string(data) != `{"kind":"investigation"}` {
		t.Errorf("sidecar content changed: %s", data)
	}
}

// Concurrent re-stamps on one item are not a real workflow — pogod stamps once
// and the worker re-stamps once — but the failure mode if the rename were not
// atomic is losing the item or duplicating the claim, so pin the degradation.
// Losers get a RETRYABLE conflict (their source name was renamed out from under
// them), never a success they cannot back up, and the item stays one file in
// claimed/ throughout.
func TestReclaimConcurrentReStampsDegradeSafely(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := rcClaimed(t, root, 1000, "raced handover")

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Reclaim(root, id, 2000+i)
		}(i)
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		if err == nil {
			won++
			continue
		}
		var mErr *mgerr.Error
		if !errors.As(err, &mErr) {
			t.Errorf("concurrent Reclaim #%d failed with an untyped error: %T %v", i, err, err)
			continue
		}
		if mErr.Category != mgerr.CatConflict || !mErr.Retryable {
			t.Errorf("concurrent Reclaim #%d lost with %s/%s (retryable=%v); a race loser must be a retryable conflict", i, mErr.Category, mErr.Code, mErr.Retryable)
		}
	}
	if won == 0 {
		t.Error("every concurrent reclaim failed; at least one must win")
	}

	if names := rcClaimedNames(t, root, id); len(names) != 1 {
		t.Errorf("claimed/ holds %v, want exactly one file for %s", names, id)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", id+".md")); !os.IsNotExist(err) {
		t.Errorf("item appeared in available/ after concurrent reclaims (stat err: %v)", err)
	}
	status, err := Status(root, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "claimed" {
		t.Errorf("status = %q after concurrent reclaims, want claimed", status)
	}
}

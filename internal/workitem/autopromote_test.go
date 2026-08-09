package workitem

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// snoozeInPast files an item, snoozes it, and drives the clock past the wake
// time — the state the store is in when a gate has opened and nothing has run.
func snoozeInPast(t *testing.T, root, title string, deps []string) string {
	t.Helper()
	item, err := Create(root, "mg-", "task", title, deps)
	if err != nil {
		t.Fatalf("Create %q: %v", title, err)
	}
	if _, _, err := SnoozeItem(root, item.ID, time.Now().UTC().Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem %s: %v", item.ID, err)
	}
	return item.ID
}

// TestAutoPromote_OpensAnElapsedGate is the unit-level acceptance case: nothing
// calls Schedule, and the item still comes out.
func TestAutoPromote_OpensAnElapsedGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := snoozeInPast(t, root, "Come back to this", nil)

	// The negative control first: before the wake time, nothing moves.
	snoozeAt(t, time.Now().UTC())
	promoted, err := AutoPromote(root)
	if err != nil {
		t.Fatalf("AutoPromote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %d items before the wake time, want 0", len(promoted))
	}
	if got := mustStatus(t, root, id); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}

	// Then the case itself.
	snoozeAt(t, time.Now().UTC().Add(48*time.Hour))
	promoted, err = AutoPromote(root)
	if err != nil {
		t.Fatalf("AutoPromote: %v", err)
	}
	if len(promoted) != 1 || promoted[0].ID != id {
		t.Fatalf("promoted = %v, want exactly [%s]", promoted, id)
	}
	if got := mustStatus(t, root, id); got != "available" {
		t.Fatalf("status = %q, want available", got)
	}
	item, err := readFile(filepath.Join(root, "work", "available", id+".md"))
	if err != nil {
		t.Fatalf("reading available/%s.md: %v", id, err)
	}
	if item.SnoozeRaw != "" {
		t.Errorf("the spent gate survived promotion: snooze = %q", item.SnoozeRaw)
	}
}

// The promoter opens the CLOCK gate only, and only when the dependency gate is
// also open. It shares gateOpen with the sweep rather than holding a second
// opinion about readiness.
func TestAutoPromote_LeavesTheDependencyGateClosed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child := snoozeInPast(t, root, "Child", []string{parent.ID})

	snoozeAt(t, time.Now().UTC().Add(48*time.Hour))
	promoted, err := AutoPromote(root)
	if err != nil {
		t.Fatalf("AutoPromote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %v with the parent still open, want none", promoted)
	}
	if got := mustStatus(t, root, child); got != "pending" {
		t.Fatalf("child status = %q, want pending", got)
	}
}

// A pending item with no clock gate is not this promoter's business: the
// dependency gate opens on `mg done`, which sweeps at that instant, and it has
// never been the gate that went stale.
func TestAutoPromote_IgnoresPendingItemsWithNoSnooze(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	promoted, err := AutoPromote(root)
	if err != nil {
		t.Fatalf("AutoPromote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %v, want none", promoted)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("child status = %q, want pending", got)
	}
}

// A `snooze:` value that is not a parseable timestamp HOLDS the item — the same
// rule the sweep follows. Ignoring it and promoting would turn a hand-edit typo
// into a silent early wake with nothing recording that a gate was discarded.
func TestAutoPromote_HoldsAMalformedGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := snoozeInPast(t, root, "Hand-edited", nil)

	path := filepath.Join(root, "work", "pending", id+".md")
	item, err := readFile(path)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	item.SnoozeRaw = "next tuesday"
	item.Snooze = time.Time{}
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	snoozeAt(t, time.Now().UTC().Add(48*time.Hour))
	promoted, err := AutoPromote(root)
	if err != nil {
		t.Fatalf("AutoPromote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %v on an unparseable gate, want none", promoted)
	}
	if got := mustStatus(t, root, id); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}
}

// TestPromote_ConcurrentPromotersDoNotDuplicate is the in-process form of the
// race that made rename-first necessary.
//
// The old ordering wrote the cleared file into pending/ and THEN renamed. Under
// concurrency that write is O_CREATE|O_TRUNC, so a promoter that lost the
// rename re-created the item in pending/ — the same item in two directories at
// once. rename-first makes the rename the only decision point.
func TestPromote_ConcurrentPromotersDoNotDuplicate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := snoozeInPast(t, root, "Contended", nil)
	snoozeAt(t, time.Now().UTC().Add(48*time.Hour))

	const racers = 16
	results := make([][]*Item, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = AutoPromote(root)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range results {
		if errs[i] != nil {
			t.Errorf("racer %d: %v — losing a promotion race must not be an error", i, errs[i])
		}
		winners += len(results[i])
	}
	if winners != 1 {
		t.Errorf("%d racers reported promoting the item, want exactly 1", winners)
	}

	pending, _ := filepath.Glob(filepath.Join(root, "work", "pending", "*.md"))
	if len(pending) != 0 {
		t.Errorf("pending/ is not empty — a loser re-created the file it lost: %v", pending)
	}
	available, _ := filepath.Glob(filepath.Join(root, "work", "available", "*.md"))
	if len(available) != 1 {
		t.Errorf("available/ holds %d files, want 1: %v", len(available), available)
	}
	if got := mustStatus(t, root, id); got != "available" {
		t.Errorf("status = %q, want available", got)
	}
}

// The sweep and the per-invocation promoter share promote(), so they may run
// against the same store at the same time without either losing an item or
// announcing one twice.
func TestPromote_ScheduleAndAutoPromoteRaceCleanly(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	ids := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		ids = append(ids, snoozeInPast(t, root, "Racer", nil))
	}
	snoozeAt(t, time.Now().UTC().Add(48*time.Hour))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		promoted []string
	)
	record := func(items []*Item, err error, who string) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			t.Errorf("%s: %v", who, err)
		}
		for _, it := range items {
			promoted = append(promoted, it.ID)
		}
	}
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); items, err := Schedule(root); record(items, err, "Schedule") }()
		go func() { defer wg.Done(); items, err := AutoPromote(root); record(items, err, "AutoPromote") }()
	}
	wg.Wait()

	if len(promoted) != len(ids) {
		t.Errorf("%d promotions reported for %d items — a promotion announced twice is a bug",
			len(promoted), len(ids))
	}
	seen := map[string]bool{}
	for _, id := range promoted {
		if seen[id] {
			t.Errorf("%s was promoted twice", id)
		}
		seen[id] = true
	}
	for _, id := range ids {
		if got := mustStatus(t, root, id); got != "available" {
			t.Errorf("%s status = %q, want available", id, got)
		}
	}
}

// ClearSpentGates is the self-heal for promote()'s one residue: an elapsed wake
// time left on an item that is no longer pending, because the process died —
// or a claim landed — between the rename and the clear.
func TestClearSpentGates_WipesTheResidueAndNothingElse(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Construct the residue directly. The crash window is microseconds wide.
	spent, err := Create(root, "mg-", "task", "Spent", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	live, err := Create(root, "mg-", "task", "Live", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stampGate(t, root, "available", spent.ID, time.Now().UTC().Add(-48*time.Hour))
	stampGate(t, root, "available", live.ID, time.Now().UTC().Add(48*time.Hour))

	cleared, err := ClearSpentGates(root)
	if err != nil {
		t.Fatalf("ClearSpentGates: %v", err)
	}
	if len(cleared) != 1 || cleared[0] != spent.ID {
		t.Fatalf("cleared = %v, want exactly [%s]", cleared, spent.ID)
	}

	after, err := readFile(filepath.Join(root, "work", "available", spent.ID+".md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if after.SnoozeRaw != "" {
		t.Errorf("the spent gate survived: %q", after.SnoozeRaw)
	}
	// A future gate on a non-pending item is inert too, but somebody typed it.
	untouched, err := readFile(filepath.Join(root, "work", "available", live.ID+".md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if untouched.SnoozeRaw == "" {
		t.Errorf("a future gate was deleted; only SPENT gates are litter")
	}
	// Neither item moved. Clearing a gate is not licence to re-file anything.
	for _, id := range []string{spent.ID, live.ID} {
		if got := mustStatus(t, root, id); got != "available" {
			t.Errorf("%s status = %q, want available", id, got)
		}
	}
}

// Unpromoted is the detector that replaces the staleness warning: pending items
// with every gate open, which after a sweep can only mean promotion is failing.
func TestUnpromoted_NamesOnlyItemsNothingIsHolding(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	held := snoozeInPast(t, root, "Still gated", nil)
	open := snoozeInPast(t, root, "Gate is open", nil)

	// Move `open`'s gate into the past on disk, leaving `held`'s in the future.
	stampGate(t, root, "pending", open, time.Now().UTC().Add(-48*time.Hour))

	stuck, err := Unpromoted(root)
	if err != nil {
		t.Fatalf("Unpromoted: %v", err)
	}
	if len(stuck) != 1 || stuck[0].ID != open {
		ids := make([]string, len(stuck))
		for i, s := range stuck {
			ids[i] = s.ID
		}
		t.Fatalf("Unpromoted = %v, want exactly [%s] (held item %s is not stuck, it is waiting)",
			ids, open, held)
	}
}

// stampGate writes a `snooze:` value onto an item in the named status
// directory, which is how the residue and the stuck-item states are built.
func stampGate(t *testing.T, root, status, id string, at time.Time) {
	t.Helper()
	path := filepath.Join(root, "work", status, id+".md")
	item, err := readFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	item.SetSnooze(at)
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

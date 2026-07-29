package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// snoozeAt drives the whole package's snooze clock to a fixed instant for the
// duration of a test. Every gate is evaluated against snoozeNow, so this is how
// "the sweep ran three days after the wake time" is expressed without sleeping.
func snoozeAt(t *testing.T, at time.Time) {
	t.Helper()
	prev := snoozeNow
	snoozeNow = func() time.Time { return at }
	t.Cleanup(func() { snoozeNow = prev })
}

// mustReadPending reads an item straight out of pending/, failing if it is not
// there. Tests assert on the DIRECTORY because the directory is the status.
func mustReadPending(t *testing.T, root, id string) *Item {
	t.Helper()
	item, err := readFile(filepath.Join(root, "work", "pending", id+".md"))
	if err != nil {
		t.Fatalf("reading pending/%s.md: %v", id, err)
	}
	return item
}

// --- the regression bar ---------------------------------------------------

// A pending item with no `snooze:` attribute must behave EXACTLY as it did
// before this feature existed. Every depends chain in the store rides on this.
func TestSnooze_UnsnoozedPendingItemIsUnchanged(t *testing.T) {
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
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("child status = %q, want pending", got)
	}

	// Sweep with the parent unfinished: nothing moves, exactly as before.
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %d items with the parent unfinished, want 0", len(promoted))
	}

	// Finish the parent: the child releases, exactly as before.
	if _, err := Claim(root, parent.ID, 0); err != nil {
		t.Fatalf("Claim parent: %v", err)
	}
	if _, _, err := Done(root, parent.ID, nil); err != nil {
		t.Fatalf("Done parent: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("child status = %q, want available", got)
	}

	// And it carries no snooze line: the attribute is absent, not empty.
	data, err := os.ReadFile(filepath.Join(root, "work", "available", child.ID+".md"))
	if err != nil {
		t.Fatalf("reading child: %v", err)
	}
	if strings.Contains(string(data), SnoozeKey+":") {
		t.Errorf("an unsnoozed item must carry no %s: line; got:\n%s", SnoozeKey, data)
	}
}

// --- the case the driver exists for ---------------------------------------

// THE ACCEPTANCE CASE: a snoozed item returns to available/ at its time even
// though nothing swept during the interval that contained that time.
//
// The gate is level-triggered, so "the driver was down through the wake
// instant" is indistinguishable from "the driver ran late". Both are expressed
// here as: no sweep between the snooze and a sweep three days after the wake.
func TestSnooze_ReturnsToAvailableAfterDriverDowntime(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	item, err := Create(root, "mg-", "task", "Revisit the pricing page", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wake := base.Add(24 * time.Hour)
	if _, _, err := SnoozeItem(root, item.ID, wake); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}
	if got := mustStatus(t, root, item.ID); got != "pending" {
		t.Fatalf("snoozed item status = %q, want pending", got)
	}

	// A sweep BEFORE the wake time leaves it alone.
	snoozeAt(t, base.Add(12*time.Hour))
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule (before wake): %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %d items before the wake time, want 0", len(promoted))
	}

	// Now the driver is DOWN across the wake instant: no sweep at all runs at
	// or near `wake`. The next sweep happens three days late.
	snoozeAt(t, wake.Add(72*time.Hour))
	promoted, err = Schedule(root)
	if err != nil {
		t.Fatalf("Schedule (after downtime): %v", err)
	}
	if len(promoted) != 1 || promoted[0].ID != item.ID {
		t.Fatalf("promoted = %v, want exactly %s", promoted, item.ID)
	}
	if got := mustStatus(t, root, item.ID); got != "available" {
		t.Errorf("status after the late sweep = %q, want available", got)
	}

	// The attribute is gone from the promoted item — a wake time that already
	// fired must not sit on an available item pretending to be a live gate.
	woken, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if woken.SnoozeRaw != "" {
		t.Errorf("promoted item still carries %s: %q", SnoozeKey, woken.SnoozeRaw)
	}
}

// A snoozed item must not be released by its dependencies completing. Both
// gates are ANDed; either one closed holds the item.
func TestSnooze_HoldsEvenWhenDependenciesComplete(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if _, _, err := SnoozeItem(root, child.ID, base.Add(48*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	if _, err := Claim(root, parent.ID, 0); err != nil {
		t.Fatalf("Claim parent: %v", err)
	}
	if _, promoted, err := Done(root, parent.ID, nil); err != nil {
		t.Fatalf("Done parent: %v", err)
	} else if len(promoted) != 0 {
		t.Fatalf("Done promoted %d items past a closed snooze gate, want 0", len(promoted))
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Errorf("child status = %q, want pending (snooze still holds)", got)
	}

	// Past the wake time, the same sweep releases it.
	snoozeAt(t, base.Add(49*time.Hour))
	if _, err := Schedule(root); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("child status = %q, want available", got)
	}
}

// A snooze whose wake time has passed must NOT be released while a dependency
// is still outstanding — the AND runs in both directions.
func TestSnooze_ElapsedSnoozeStillWaitsOnDependencies(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if _, _, err := SnoozeItem(root, child.ID, base.Add(time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	snoozeAt(t, base.Add(10*time.Hour)) // wake time long past, parent unfinished
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %d items with an unmet dependency, want 0", len(promoted))
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Errorf("child status = %q, want pending", got)
	}
}

// --- the swallower case, constructed ---------------------------------------

// A `snooze:` value mg cannot parse must HOLD the item and be reported. The
// alternative failure modes are both silent: discarding it promotes the item
// early with no record, and ignoring it quietly parks the item forever.
func TestSnooze_MalformedValueHoldsAndIsReported(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	item, err := Create(root, "mg-", "task", "Hand-edited gate", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Only a hand-edit can produce this — `mg snooze` refuses it — so it is
	// constructed on disk exactly the way a person would create it.
	writeMalformedSnooze(t, root, item.ID, "next tuesday")

	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promoted %d items past an unparseable gate, want 0", len(promoted))
	}
	if got := mustStatus(t, root, item.ID); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}

	// Held is only half of it. It must also be NAMED, or it is exactly the
	// silent swallow this feature exists to prevent.
	stranded, err := Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 1 {
		t.Fatalf("Stranded reported %d items, want 1", len(stranded))
	}
	if stranded[0].Item.ID != item.ID {
		t.Fatalf("Stranded named %s, want %s", stranded[0].Item.ID, item.ID)
	}
	if got := stranded[0].Reason(); !strings.Contains(got, "next tuesday") || !strings.Contains(got, "RFC3339") {
		t.Errorf("Reason() = %q; it must quote the bad value and say what was expected", got)
	}

	// And the bad value survives a read/write round trip rather than being
	// silently erased into an un-snoozed item.
	if _, err := Update(root, item.ID, UpdateField{Priority: strPtr("high")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after := mustReadPending(t, root, item.ID)
	if after.SnoozeRaw != "next tuesday" {
		t.Errorf("after an unrelated edit, %s = %q, want %q", SnoozeKey, after.SnoozeRaw, "next tuesday")
	}
}

// writeMalformedSnooze rewrites an item's frontmatter with a `snooze:` value
// that is not a timestamp, reproducing a hand-edit.
func writeMalformedSnooze(t *testing.T, root, id, raw string) {
	t.Helper()
	path := filepath.Join(root, "work", "available", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	content := strings.Replace(string(data), "\ndepends: ", "\n"+SnoozeKey+": "+raw+"\ndepends: ", 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", id, err)
	}
	moveForTest(t, root, "available", "pending", id)
}

func strPtr(s string) *string { return &s }

// --- placement rule, all four doors ---------------------------------------

// Clearing the last dependency off a snoozed item must not promote it: one
// gate opening is not every gate opening. This is edit.go's door.
func TestSnooze_EditRemovingDepsDoesNotPromoteSnoozedItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if _, _, err := SnoozeItem(root, child.ID, base.Add(48*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	if _, err := Update(root, child.ID, UpdateField{RmDepends: []string{parent.ID}}); err != nil {
		t.Fatalf("Update --rm-depends: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Errorf("status after removing the last dep = %q, want pending (snooze still holds)", got)
	}
}

// An item shelved while snoozed must come back still snoozed. Unshelving lifts
// the shelf, not the clock. This is shelve.go's door.
func TestSnooze_UnshelveKeepsAClosedSnoozeGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	item, err := Create(root, "mg-", "task", "Parked and snoozed", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := SnoozeItem(root, item.ID, base.Add(48*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	if _, err := Unshelve(root, item.ID); err != nil {
		t.Fatalf("Unshelve: %v", err)
	}
	if got := mustStatus(t, root, item.ID); got != "pending" {
		t.Errorf("status after unshelve = %q, want pending (snooze still holds)", got)
	}

	// Past the wake time the same unshelve releases it, so the gate is being
	// consulted rather than the item being parked unconditionally.
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("re-Shelve: %v", err)
	}
	snoozeAt(t, base.Add(49*time.Hour))
	if _, err := Unshelve(root, item.ID); err != nil {
		t.Fatalf("late Unshelve: %v", err)
	}
	if got := mustStatus(t, root, item.ID); got != "available" {
		t.Errorf("status after the late unshelve = %q, want available", got)
	}
}

// --- lifting the gate -----------------------------------------------------

func TestSnooze_UnsnoozeReleasesWhenNoOtherGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	item, err := Create(root, "mg-", "task", "Changed my mind", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := SnoozeItem(root, item.ID, base.Add(72*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	updated, dest, err := UnsnoozeItem(root, item.ID)
	if err != nil {
		t.Fatalf("UnsnoozeItem: %v", err)
	}
	if dest != "available" {
		t.Errorf("destination = %q, want available", dest)
	}
	if updated.SnoozeRaw != "" {
		t.Errorf("%s survived the unsnooze: %q", SnoozeKey, updated.SnoozeRaw)
	}
	if got := mustStatus(t, root, item.ID); got != "available" {
		t.Errorf("status = %q, want available", got)
	}
}

func TestSnooze_UnsnoozeKeepsItPendingOnAnUnmetDependency(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if _, _, err := SnoozeItem(root, child.ID, base.Add(72*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	_, dest, err := UnsnoozeItem(root, child.ID)
	if err != nil {
		t.Fatalf("UnsnoozeItem: %v", err)
	}
	if dest != "pending" {
		t.Errorf("destination = %q, want pending — the dependency gate is still closed", dest)
	}
}

func TestSnooze_UnsnoozeRefusesAnItemThatIsNotSnoozed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Never snoozed", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := UnsnoozeItem(root, item.ID); err == nil {
		t.Fatal("UnsnoozeItem on an unsnoozed item returned nil; it must refuse")
	}
}

// --- status validation ----------------------------------------------------

func TestSnooze_RefusesTerminalAndShelvedStatuses(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)
	until := base.Add(24 * time.Hour)

	done, err := Create(root, "mg-", "task", "Finished", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, done.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, done.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, _, err := SnoozeItem(root, done.ID, until); err == nil {
		t.Error("snoozing a done item must be refused")
	}

	shelved, err := Create(root, "mg-", "task", "Parked", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Shelve(root, shelved.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	if _, _, err := SnoozeItem(root, shelved.ID, until); err == nil {
		t.Error("snoozing a shelved item must be refused")
	}
}

// Snoozing a claimed item releases the claim, the same way shelving does — and
// the file must land as <id>.md, not keep its PID suffix.
func TestSnooze_ClaimedItemLandsInPendingWithoutThePIDSuffix(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	item, err := Create(root, "mg-", "task", "Claimed then set aside", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 4321); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, from, err := SnoozeItem(root, item.ID, base.Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	} else if from != "claimed" {
		t.Errorf("from-status = %q, want claimed", from)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "pending", item.ID+".md")); err != nil {
		t.Errorf("expected pending/%s.md: %v", item.ID, err)
	}
}

// --- round trip -----------------------------------------------------------

func TestSnooze_RoundTripsThroughParseAndRender(t *testing.T) {
	at := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC)
	item := &Item{ID: "mg-abcd", Type: "task", Created: at, Creator: "tester", Title: "T"}
	item.SetSnooze(at.Add(24 * time.Hour))

	parsed, err := Parse(Render(item))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !parsed.Snooze.Equal(item.Snooze) {
		t.Errorf("Snooze = %v, want %v", parsed.Snooze, item.Snooze)
	}
	if parsed.SnoozeRaw != item.SnoozeRaw {
		t.Errorf("SnoozeRaw = %q, want %q", parsed.SnoozeRaw, item.SnoozeRaw)
	}
	if parsed.SnoozeMalformed() {
		t.Error("a well-formed snooze must not read as malformed")
	}
}

// A local input is stored as UTC. The record must mean the same instant on
// another machine and on the other side of a DST boundary.
func TestSnooze_StoresUTCRegardlessOfInputZone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("Europe/London unavailable: %v", err)
	}
	got, err := ParseSnoozeUntil("2026-08-03 14:30", loc)
	if err != nil {
		t.Fatalf("ParseSnoozeUntil: %v", err)
	}
	want := time.Date(2026, 8, 3, 13, 30, 0, 0, time.UTC) // BST is UTC+1
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
}

func TestSnooze_DateOnlyMeansNineLocal(t *testing.T) {
	loc := time.FixedZone("TEST", 2*3600)
	got, err := ParseSnoozeUntil("2026-08-03", loc)
	if err != nil {
		t.Fatalf("ParseSnoozeUntil: %v", err)
	}
	want := time.Date(2026, 8, 3, dateOnlyHour, 0, 0, 0, loc).UTC()
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestSnooze_ParseUntilRejectsGarbage(t *testing.T) {
	for _, in := range []string{"next tuesday", "", "2026-13-45", "soon"} {
		if _, err := ParseSnoozeUntil(in, time.UTC); err == nil {
			t.Errorf("ParseSnoozeUntil(%q) accepted an unparseable time", in)
		}
	}
}

func TestSnooze_ParseForAcceptsDaysAndWeeks(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"90m", now.Add(90 * time.Minute)},
		{"6h", now.Add(6 * time.Hour)},
		{"3d", now.AddDate(0, 0, 3)},
		{"2w", now.AddDate(0, 0, 14)},
	}
	for _, c := range cases {
		got, err := ParseSnoozeFor(c.in, now)
		if err != nil {
			t.Fatalf("ParseSnoozeFor(%q): %v", c.in, err)
		}
		if !got.Equal(c.want.UTC()) {
			t.Errorf("ParseSnoozeFor(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want.UTC().Format(time.RFC3339))
		}
	}
	for _, bad := range []string{"", "tomorrow", "-3h", "0h", "3x"} {
		if _, err := ParseSnoozeFor(bad, now); err == nil {
			t.Errorf("ParseSnoozeFor(%q) accepted an invalid duration", bad)
		}
	}
}

// --- the driver's pulse ---------------------------------------------------

func TestSnooze_DriverIsStaleUntilTheSweepRuns(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, now)

	// Never swept: the strongest form of "nothing is driving this".
	st := CheckDriver(root, now)
	if st.Ever {
		t.Error("Ever = true on a store that has never been swept")
	}
	if !st.Stale {
		t.Error("Stale = false on a store that has never been swept")
	}

	if err := RecordSweep(root); err != nil {
		t.Fatalf("RecordSweep: %v", err)
	}
	if st := CheckDriver(root, now); st.Stale || !st.Ever {
		t.Errorf("after a sweep: Stale=%v Ever=%v, want false/true", st.Stale, st.Ever)
	}

	// Just inside the window is fresh; just outside is stale.
	if st := CheckDriver(root, now.Add(DriverStaleAfter-time.Minute)); st.Stale {
		t.Error("a sweep inside DriverStaleAfter must not read as stale")
	}
	if st := CheckDriver(root, now.Add(DriverStaleAfter+time.Minute)); !st.Stale {
		t.Error("a sweep older than DriverStaleAfter must read as stale")
	}
}

// SnoozedPending is the only view of the waiting population. If it cannot list
// them, nobody can audit them.
func TestSnooze_SnoozedPendingListsSoonestFirst(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, base)

	later, err := Create(root, "mg-", "task", "Later", nil)
	if err != nil {
		t.Fatalf("Create later: %v", err)
	}
	sooner, err := Create(root, "mg-", "task", "Sooner", nil)
	if err != nil {
		t.Fatalf("Create sooner: %v", err)
	}
	plain, err := Create(root, "mg-", "task", "Plain pending", nil)
	if err != nil {
		t.Fatalf("Create plain: %v", err)
	}
	moveForTest(t, root, "available", "pending", plain.ID)

	if _, _, err := SnoozeItem(root, later.ID, base.Add(72*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem later: %v", err)
	}
	if _, _, err := SnoozeItem(root, sooner.ID, base.Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem sooner: %v", err)
	}

	got, err := SnoozedPending(root)
	if err != nil {
		t.Fatalf("SnoozedPending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("SnoozedPending returned %d items, want 2 (the un-snoozed pending item must not appear)", len(got))
	}
	if got[0].ID != sooner.ID || got[1].ID != later.ID {
		t.Errorf("order = [%s %s], want soonest first [%s %s]", got[0].ID, got[1].ID, sooner.ID, later.ID)
	}
}

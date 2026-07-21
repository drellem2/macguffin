package workitem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeSidecarFile drops a <id>.result.json sidecar directly into work/<dir>/,
// simulating a completion result that already sits beside the item's .md.
func writeSidecarFile(t *testing.T, root, dir, id, content string) string {
	t.Helper()
	path := filepath.Join(root, "work", dir, id+".result.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing sidecar %s: %v", path, err)
	}
	return path
}

func assertSidecarAt(t *testing.T, root, dir, id, wantContent string) {
	t.Helper()
	path := filepath.Join(root, "work", dir, id+".result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected sidecar at %s: %v", path, err)
	}
	if wantContent != "" && string(data) != wantContent {
		t.Errorf("sidecar at %s = %q, want %q", path, string(data), wantContent)
	}
}

func assertNoSidecarAt(t *testing.T, root, dir, id string) {
	t.Helper()
	path := filepath.Join(root, "work", dir, id+".result.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("sidecar unexpectedly present at %s (err=%v)", path, err)
	}
}

// --- moveResultSidecar unit tests ---

func TestMoveResultSidecar_MovesWhenPresent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	srcDir := filepath.Join(root, "work", "done")
	dstDir := filepath.Join(root, "work", "claimed")
	writeSidecarFile(t, root, "done", "mg-abcd", `{"branch":"x"}`)

	if err := moveResultSidecar(srcDir, dstDir, "mg-abcd"); err != nil {
		t.Fatalf("moveResultSidecar: %v", err)
	}
	assertSidecarAt(t, root, "claimed", "mg-abcd", `{"branch":"x"}`)
	assertNoSidecarAt(t, root, "done", "mg-abcd")
}

func TestMoveResultSidecar_NoopWhenAbsent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	srcDir := filepath.Join(root, "work", "done")
	dstDir := filepath.Join(root, "work", "claimed")

	if err := moveResultSidecar(srcDir, dstDir, "mg-none"); err != nil {
		t.Fatalf("moveResultSidecar (absent) should be a no-op, got: %v", err)
	}
	assertNoSidecarAt(t, root, "claimed", "mg-none")
}

func TestMoveResultSidecar_CreatesDstDir(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	srcDir := filepath.Join(root, "work", "done")
	// A partition-style dst dir that does not exist yet.
	dstDir := filepath.Join(root, "work", "archive", "2026-07")
	writeSidecarFile(t, root, "done", "mg-part", `{"branch":"y"}`)

	if err := moveResultSidecar(srcDir, dstDir, "mg-part"); err != nil {
		t.Fatalf("moveResultSidecar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "mg-part.result.json")); err != nil {
		t.Errorf("sidecar not moved into freshly created dst dir: %v", err)
	}
}

// --- transition positive controls: the sidecar must follow the .md ---

func TestReopenMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Reopen carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Done with a real result writes the sidecar into done/.
	if _, _, err := Done(root, item.ID, json.RawMessage(`{"branch":"polecat-x"}`)); err != nil {
		t.Fatalf("Done: %v", err)
	}
	assertSidecarAt(t, root, "done", item.ID, `{"branch":"polecat-x"}`)

	if _, err := Reopen(root, item.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	// The sidecar must follow the item into claimed/ — not be orphaned in done/.
	assertSidecarAt(t, root, "claimed", item.ID, `{"branch":"polecat-x"}`)
	assertNoSidecarAt(t, root, "done", item.ID)
}

func TestUnclaimMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Unclaim carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// A sidecar sitting in claimed/ (e.g. left by a reopen) must follow unclaim.
	writeSidecarFile(t, root, "claimed", item.ID, `{"branch":"polecat-x"}`)

	if _, err := Unclaim(root, item.ID); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	assertSidecarAt(t, root, "available", item.ID, `{"branch":"polecat-x"}`)
	assertNoSidecarAt(t, root, "claimed", item.ID)
}

func TestClaimMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Claim carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A sidecar in available/ (e.g. after an unarchive) must follow the claim.
	writeSidecarFile(t, root, "available", item.ID, `{"branch":"polecat-x"}`)

	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	assertSidecarAt(t, root, "claimed", item.ID, `{"branch":"polecat-x"}`)
	assertNoSidecarAt(t, root, "available", item.ID)
}

func TestShelveMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Shelve carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeSidecarFile(t, root, "available", item.ID, `{"branch":"polecat-x"}`)

	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	assertSidecarAt(t, root, "shelved", item.ID, `{"branch":"polecat-x"}`)
	assertNoSidecarAt(t, root, "available", item.ID)
}

func TestUnshelveMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Unshelve carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeSidecarFile(t, root, "available", item.ID, `{"branch":"polecat-x"}`)

	// Shelve moves both .md and sidecar into shelved/.
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	assertSidecarAt(t, root, "shelved", item.ID, `{"branch":"polecat-x"}`)

	// Unshelve (no deps) moves them back to available/.
	if _, err := Unshelve(root, item.ID); err != nil {
		t.Fatalf("Unshelve: %v", err)
	}
	assertSidecarAt(t, root, "available", item.ID, `{"branch":"polecat-x"}`)
	assertNoSidecarAt(t, root, "shelved", item.ID)
}

// TestDoneMovesSidecar_ReopenDoneRoundTrip is the positive control for mg-9795.
//
// Done is the only transition that may *write* a sidecar as well as carry one,
// and for a long time it only wrote: it built its destination path directly and
// never touched a sidecar already sitting in the origin directory. A
// done -> reopen -> done round trip therefore left REV1 stranded in claimed/
// (put there correctly by Reopen) while REV2 landed in done/, producing a
// .result.json with no .md beside it — an orphan invisible to `mg show`.
//
// The assertion that matters is that the ORIGIN directory is empty of the
// sidecar, not merely that the destination has one. The weaker
// destination-only check passes against the broken code, which is precisely why
// this survived mg-ab67's cleanup.
func TestDoneMovesSidecar_ReopenDoneRoundTrip(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Done carries sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, json.RawMessage(`{"branch":"rev1"}`)); err != nil {
		t.Fatalf("Done (rev1): %v", err)
	}

	// Reopen carries REV1 back into claimed/ — this part already worked.
	if _, err := Reopen(root, item.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	assertSidecarAt(t, root, "claimed", item.ID, `{"branch":"rev1"}`)

	// The second Done must carry REV1 out of claimed/ before writing REV2.
	if _, _, err := Done(root, item.ID, json.RawMessage(`{"branch":"rev2"}`)); err != nil {
		t.Fatalf("Done (rev2): %v", err)
	}

	// Destination holds the fresh result: REV2 supersedes the carried REV1.
	assertSidecarAt(t, root, "done", item.ID, `{"branch":"rev2"}`)
	// The whole ticket: nothing left behind in the origin directory.
	assertNoSidecarAt(t, root, "claimed", item.ID)
}

// TestDoneMovesSidecarWithoutNewResult covers the other half of the gap: a Done
// with no result JSON at all. The old code wrote nothing and moved nothing, so
// a pre-existing sidecar in claimed/ was orphaned *and* the completed item in
// done/ lost its result entirely.
func TestDoneMovesSidecarWithoutNewResult(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Done with no new result", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	writeSidecarFile(t, root, "claimed", item.ID, `{"branch":"carried"}`)

	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	// With no fresh result to supersede it, the carried sidecar is the result.
	assertSidecarAt(t, root, "done", item.ID, `{"branch":"carried"}`)
	assertNoSidecarAt(t, root, "claimed", item.ID)
}

// TestTransitionsWithoutSidecarStayClean guards the no-op path: transitioning an
// item that never had a result must not error nor conjure a stray sidecar.
func TestTransitionsWithoutSidecarStayClean(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "No sidecar", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := Unclaim(root, item.ID); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	assertNoSidecarAt(t, root, "available", item.ID)
	assertNoSidecarAt(t, root, "claimed", item.ID)
}

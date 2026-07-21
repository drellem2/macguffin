package workitem

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content at root/work/<rel>, creating parents.
func writeStoreFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, "work", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func strayByID(strays []StraySidecar, id string) (StraySidecar, bool) {
	for _, s := range strays {
		if s.ID == id {
			return s, true
		}
	}
	return StraySidecar{}, false
}

// TestFindStraySidecars_BesideItemIsNotStray pins the base case: a sidecar
// sharing a directory with its .md is not reported, in every lifecycle dir,
// including the claimed/ PID-suffix form.
func TestFindStraySidecars_BesideItemIsNotStray(t *testing.T) {
	root := t.TempDir()
	writeStoreFile(t, root, "done/mg-aaaa.md", "# done item")
	writeStoreFile(t, root, "done/mg-aaaa.result.json", `{"ok":true}`)
	writeStoreFile(t, root, "claimed/mg-bbbb.md.4242", "# claimed item")
	writeStoreFile(t, root, "claimed/mg-bbbb.result.json", `{"ok":true}`)
	writeStoreFile(t, root, "archive/2026-07/mg-cccc.md", "# archived item")
	writeStoreFile(t, root, "archive/2026-07/mg-cccc.result.json", `{"ok":true}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 0 {
		t.Fatalf("expected no strays, got %d: %+v", len(strays), strays)
	}
}

// TestFindStraySidecars_PositiveControl is the test this whole feature exists
// for: on a dirty store the scan must FIND the orphans. A checker that reports
// "nothing to do" against known-dirty input is the failure mode here, and it
// looks exactly like success.
func TestFindStraySidecars_PositiveControl(t *testing.T) {
	root := t.TempDir()
	// Item is done; a stale sidecar was left behind in claimed/. `claimed`
	// sorts before `done`, which is what makes a glob return the wrong one.
	writeStoreFile(t, root, "done/mg-1111.md", "# item")
	writeStoreFile(t, root, "done/mg-1111.result.json", `{"verdict":"current"}`)
	writeStoreFile(t, root, "claimed/mg-1111.result.json", `{"verdict":"superseded"}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 {
		t.Fatalf("expected exactly 1 stray, got %d: %+v", len(strays), strays)
	}
	s := strays[0]
	if s.ID != "mg-1111" {
		t.Errorf("ID = %q, want mg-1111", s.ID)
	}
	if s.Location != "claimed" {
		t.Errorf("Location = %q, want claimed", s.Location)
	}
	if s.ItemStatus != "done" {
		t.Errorf("ItemStatus = %q, want done", s.ItemStatus)
	}
	if !s.AuthoritativeExists {
		t.Error("AuthoritativeExists = false, want true")
	}
	if !s.Differs {
		t.Error("Differs = false, but the two files have different bytes")
	}
	if s.Redundant() {
		t.Error("Redundant() = true for a differing stray; that would invite a destructive delete")
	}
}

// TestFindStraySidecars_IdenticalIsRedundant separates the safe case from the
// unsafe one. Only a byte-identical stray may be reported as safe to delete.
func TestFindStraySidecars_IdenticalIsRedundant(t *testing.T) {
	root := t.TempDir()
	writeStoreFile(t, root, "done/mg-2222.md", "# item")
	writeStoreFile(t, root, "done/mg-2222.result.json", `{"same":true}`)
	writeStoreFile(t, root, "available/mg-2222.result.json", `{"same":true}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 {
		t.Fatalf("expected 1 stray, got %d", len(strays))
	}
	if strays[0].Differs {
		t.Error("Differs = true for byte-identical copies")
	}
	if !strays[0].Redundant() {
		t.Error("Redundant() = false for an identical stray with the authoritative copy present")
	}
}

// TestFindStraySidecars_MissingAuthoritativeIsLoadBearing pins the finding that
// motivated reporting rather than auto-deleting: when the copy beside the item
// is absent, the stray is the ONLY surviving record. Deleting it destroys data.
func TestFindStraySidecars_MissingAuthoritativeIsLoadBearing(t *testing.T) {
	root := t.TempDir()
	writeStoreFile(t, root, "done/mg-3333.md", "# item, no sidecar beside it")
	writeStoreFile(t, root, "claimed/mg-3333.result.json", `{"payload":"the only copy"}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := strayByID(strays, "mg-3333")
	if !ok {
		t.Fatal("stray not found")
	}
	if s.AuthoritativeExists {
		t.Error("AuthoritativeExists = true, but no sidecar sits beside the item")
	}
	if s.Redundant() {
		t.Error("Redundant() = true with no authoritative copy — this would delete the only record")
	}
}

// TestFindStraySidecars_ArchivedTwinsKeepOwnSidecars guards the false positive
// that a naive implementation hits: two archived twins of one ID in different
// partitions make the ID ambiguous to ResolveUnique, but each twin's own
// sidecar is beside its own .md and must not be reported. Before co-location
// was checked first, this misreported every such sidecar as an orphan.
func TestFindStraySidecars_ArchivedTwinsKeepOwnSidecars(t *testing.T) {
	root := t.TempDir()
	writeStoreFile(t, root, "archive/2026-05/mg-4444.md", "# twin one")
	writeStoreFile(t, root, "archive/2026-05/mg-4444.result.json", `{"partition":"2026-05"}`)
	writeStoreFile(t, root, "archive/2026-07/mg-4444.md", "# twin two")
	writeStoreFile(t, root, "archive/2026-07/mg-4444.result.json", `{"partition":"2026-07"}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 0 {
		t.Fatalf("archived twins' own sidecars reported as strays: %+v", strays)
	}
}

// TestFindStraySidecars_UnresolvableItemIsReported covers a sidecar whose item
// is gone entirely — nothing is authoritative, so it must never be called
// redundant.
func TestFindStraySidecars_UnresolvableItemIsReported(t *testing.T) {
	root := t.TempDir()
	writeStoreFile(t, root, "claimed/mg-5555.result.json", `{"orphan":true}`)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := strayByID(strays, "mg-5555")
	if !ok {
		t.Fatal("stray for an item with no .md was not reported")
	}
	if s.ItemStatus != "" {
		t.Errorf("ItemStatus = %q, want empty for an unresolvable item", s.ItemStatus)
	}
	if s.Redundant() {
		t.Error("Redundant() = true for a sidecar whose item does not exist")
	}
}

// TestFindStraySidecars_EmptyStore confirms a clean store reports nothing and
// does not error on missing lifecycle directories.
func TestFindStraySidecars_EmptyStore(t *testing.T) {
	root := t.TempDir()
	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatalf("scan of an empty store errored: %v", err)
	}
	if len(strays) != 0 {
		t.Fatalf("expected no strays, got %+v", strays)
	}
}

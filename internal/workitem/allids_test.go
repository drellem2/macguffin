package workitem

import (
	"os"
	"path/filepath"
	"testing"
)

// AllIDs answers, in one walk, the question Resolve answers per name. The two
// views must agree about the same store: a bulk answer that is more permissive
// than the single-name one hands a caller standing a name does not have, and
// one that is stricter hides a work item that is really there. These tests
// check the agreement, not just the contents.

// seedItemFile plants a work-item file at work/<dir>/<name>.
func seedItemFile(t *testing.T, root, dir, name string) {
	t.Helper()
	full := filepath.Join(root, "work", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(filepath.Join(full, name), []byte("---\nid: x\n---\n\n# x\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestAllIDsCoversEveryStateIncludingTerminalOnes(t *testing.T) {
	root := t.TempDir()
	seedItemFile(t, root, "available", "aaaa.md")
	seedItemFile(t, root, "claimed", "bbbb.md.4321") // claimed items carry a pid suffix
	seedItemFile(t, root, "pending", "cccc.md")
	seedItemFile(t, root, "shelved", "dddd.md")
	seedItemFile(t, root, "done", "eeee.md")
	seedItemFile(t, root, filepath.Join("archive", "2026-07"), "ffff.md")
	seedItemFile(t, root, "archive", "gggg.md") // loose, no partition

	ids := AllIDs(root)
	for _, want := range []string{"aaaa", "bbbb", "cccc", "dddd", "eeee", "ffff", "gggg"} {
		if !ids[want] {
			t.Errorf("AllIDs is missing %q: %v", want, ids)
		}
	}

	// Terminal ids are the difference from LiveIDs, and the difference is the
	// point: a finished item still explains a mailbox named after it, even
	// though it is a wrong guess for "who did the sender mean".
	live := map[string]bool{}
	for _, id := range LiveIDs(root) {
		live[id] = true
	}
	if live["eeee"] || live["ffff"] {
		t.Errorf("LiveIDs must exclude terminal records, got %v", live)
	}
}

// TestAllIDsIgnoresWhatResolveIgnores: sidecars and editor leavings are not
// work items. Counting one would vouch for a mailbox name that Resolve calls
// unknown, so a send would be refused while the listing called the box
// accounted-for — the two views disagreeing about one store.
func TestAllIDsIgnoresWhatResolveIgnores(t *testing.T) {
	root := t.TempDir()
	seedItemFile(t, root, "available", "aaaa.result.json") // sidecar
	seedItemFile(t, root, "archive", "bbbb.md.bak")        // loose editor backup
	seedItemFile(t, root, "available", "cccc.md")          // the only real item

	ids := AllIDs(root)
	if len(ids) != 1 || !ids["cccc"] {
		t.Errorf("AllIDs = %v, want exactly {cccc}", ids)
	}

	// And the agreement is checked directly rather than assumed.
	for _, name := range []string{"aaaa", "bbbb", "cccc"} {
		matches, err := Resolve(root, name)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		if got := len(matches) > 0; got != ids[name] {
			t.Errorf("%s: Resolve sees it = %v, AllIDs sees it = %v — the two views disagree", name, got, ids[name])
		}
	}
}

// TestAllIDsToleratesAMissingStore: a store with no work/ at all yields an
// empty set, not a panic. `mg mail list` calls this on every enumeration, and a
// half-built store must not turn every mailbox into an unvouched one by way of
// a crash.
func TestAllIDsToleratesAMissingStore(t *testing.T) {
	ids := AllIDs(t.TempDir())
	if len(ids) != 0 {
		t.Errorf("AllIDs on an empty store = %v, want empty", ids)
	}
}

package mgcli

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/memstore"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
)

// runMG runs one mg invocation in-process through Run — Main's body, minus
// os.Exit — and returns its exit code and stdout.
func runMG(t *testing.T, open store.Opener, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	code := Run(open, Build{}, args)
	w.Close()
	os.Stdout = old
	return code, string(<-done)
}

// The proof that a second backend is actually reached: mg's own CLI lifecycle,
// new -> claim -> done -> archive, run end to end against an in-memory store
// through mgcli.Run, with a workspace root that is an EMPTY directory.
//
// If any of those commands reached storage around store.Backend — an
// os.ReadDir of work/available, an os.WriteFile of an item path — it would
// find nothing (the item is in a Go map) or write a file (the root must stay
// empty), and this test fails either way. The fake itself passes storetest
// (internal/memstore).
func TestCLILifecycleReachesANonFilesystemBackend(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MG_ROOT", "")
	t.Setenv("MG_ACTOR", "memstore-test")
	t.Setenv(envNoAutoPromote, "")

	mem := memstore.New()
	open := func(r string) (store.Backend, error) {
		if r != root {
			t.Errorf("backend opened for root %q, want %q", r, root)
		}
		return mem, nil
	}
	t.Cleanup(func() {
		useBackend(fsstore.Open)
		event.SetLogOpener(nil)
		workitem.SetOpener(nil)
		workitem.SetActor(nil)
		rootFlag = ""
		outputJSON = false
	})

	code, out := runMG(t, open, "--root", root, "new", "--title=memstore lifecycle", "--type=task")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := strings.TrimPrefix(strings.SplitN(strings.TrimSpace(out), ":", 2)[0], "Created ")
	if id == "" || strings.Contains(id, " ") {
		t.Fatalf("could not read the new id from %q", out)
	}
	if d, err := mem.Get(id); err != nil || d.State.Status != store.Available {
		t.Fatalf("after mg new the backend holds %v, %v; want %s available", d, err, id)
	}

	if code, out := runMG(t, open, "--root", root, "claim", id); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	d, err := mem.Get(id)
	if err != nil || d.State.Status != store.Claimed || !strings.HasPrefix(d.State.Holder, "memstore-test@") {
		t.Fatalf("after mg claim the backend holds %+v, %v; want claimed with the CLI's Actor as holder", d, err)
	}

	// A second claim must be refused by the backend's Transition.
	if code, _ := runMG(t, open, "--root", root, "claim", id); code != 4 {
		t.Fatalf("second mg claim: exit %d, want 4 (conflict)", code)
	}

	if code, out := runMG(t, open, "--root", root, "done", id); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	if d, _ := mem.Get(id); d == nil || d.State.Status != store.Done {
		t.Fatalf("after mg done the backend holds %+v; want done", d)
	}

	if code, out := runMG(t, open, "--root", root, "archive", id); code != 0 {
		t.Fatalf("mg archive: exit %d\n%s", code, out)
	}
	d, err = mem.Get(id)
	if err != nil || d.State.Status != store.Archived || d.Partition == "" {
		t.Fatalf("after mg archive the backend holds %+v, %v; want archived in a partition", d, err)
	}

	// mg show reads it back through the backend.
	code, out = runMG(t, open, "--root", root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show: exit %d\n%s", code, out)
	}
	var shown struct {
		ID, Status string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &shown); err != nil || shown.ID != id || shown.Status != "archived" {
		t.Fatalf("mg show --json = %q (%v); want %s archived", out, err, id)
	}
	outputJSON = false

	calls := mem.Calls()
	for _, op := range []string{"Create", "Transition", "Archive", "AppendEvent"} {
		if calls[op] == 0 {
			t.Errorf("the backend's %s was never called (calls: %v)", op, calls)
		}
	}

	// The audit trail went to the backend's log, in lifecycle order.
	log, _ := mem.Events().ReadEvents()
	var types []string
	for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
		var e struct{ Type string }
		_ = json.Unmarshal(line, &e)
		types = append(types, e.Type)
	}
	want := []string{"work.created", "work.claim", "work.done", "work.archive"}
	got := strings.Join(types, ",")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("backend event log %q is missing %s", got, w)
		}
	}

	// Nothing was written to the workspace root: not an item, not an event,
	// not a directory. Positive control first — the walk does see a file.
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, filepath.Dir(probe)); n != 1 {
		t.Fatalf("control: countFiles saw %d files in a directory holding one", n)
	}
	if n := countFiles(t, root); n != 0 {
		var names []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if p != root {
				names = append(names, p)
			}
			return nil
		})
		t.Fatalf("mg wrote %d entries under the workspace root, bypassing the backend:\n%s", n, strings.Join(names, "\n"))
	}
}

// countFiles counts every entry under dir, directories included.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

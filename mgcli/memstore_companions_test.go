package mgcli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/memstore"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
)

// mg-7153e: the companions — result sidecar, saved bodies, sweep stamp — are
// reached through store.Backend.Companions, so the lifecycle pogo actually
// drives (`mg done --result` on every completion) works on a backend that is
// not the filesystem. Same harness as TestCLILifecycleReachesANonFilesystemBackend:
// an in-memory store and a workspace root that must stay EMPTY, so a companion
// written as a file anywhere under it fails the test.
//
// Before mg-7153e, Done refused a --result on any backend that was not
// store.Local (results_unsupported). Mutation control, run by hand: with
// memstore's WriteResult made a no-op, this test fails at `mg sidecar --json`
// (exit 3, no_sidecar) — it reads the result back from the backend, it does
// not take the exit code of `mg done` on trust.
func TestCLICompanionsReachANonFilesystemBackend(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MG_ROOT", "")
	t.Setenv("MG_ACTOR", "memstore-test")
	t.Setenv(envNoAutoPromote, "")

	mem := memstore.New()
	open := func(string) (store.Backend, error) { return mem, nil }
	reset := func() {
		outputJSON, sidecarJSON, sidecarPathOnly, showJSON = false, false, false, false
		editBody, restoreBodyList = "", false
	}
	t.Cleanup(func() {
		reset()
		useBackend(fsstore.Open)
		event.SetLogOpener(nil)
		workitem.SetOpener(nil)
		workitem.SetActor(nil)
		rootFlag = ""
	})
	mg := func(args ...string) (int, string) {
		t.Helper()
		code, out := runMG(t, open, append([]string{"--root", root}, args...)...)
		reset()
		return code, out
	}

	code, out := mg("new", "--title=companions", "--type=task", "--body=the original body")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := strings.TrimPrefix(strings.SplitN(strings.TrimSpace(out), ":", 2)[0], "Created ")
	if code, out := mg("claim", id); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	// A replace-mode edit saves the prior body, through the backend.
	if code, out := mg("edit", id, "--body=a clobbered body"); code != 0 {
		t.Fatalf("mg edit --body: exit %d\n%s", code, out)
	}
	if names, _ := mem.Companions().Bodies(store.Ref{ID: id}); len(names) != 1 {
		t.Fatalf("after a replace edit the backend holds %d saved bodies, want 1", len(names))
	}
	if code, out := mg("restore-body", id); code != 0 {
		t.Fatalf("mg restore-body: exit %d\n%s", code, out)
	}
	if d, _ := mem.Get(id); d == nil || !strings.Contains(string(d.Content), "the original body") {
		t.Fatalf("restore-body did not bring the saved body back: %s", d.Content)
	}

	// The completion pogo runs on every merge.
	if code, out := mg("done", id, `--result={"branch":"polecat-x","verdict":"pass"}`); code != 0 {
		t.Fatalf("mg done --result: exit %d\n%s", code, out)
	}
	if d, _ := mem.Get(id); d == nil || d.State.Status != store.Done {
		t.Fatalf("after mg done the backend holds %+v; want done", d)
	}

	type sidecarOut struct {
		ID, Status string
		Result     struct{ Branch, Verdict string }
	}
	readSidecar := func(wantStatus string) {
		t.Helper()
		code, out := mg("sidecar", id, "--json")
		if code != 0 {
			t.Fatalf("mg sidecar --json: exit %d\n%s", code, out)
		}
		var sc sidecarOut
		if err := json.Unmarshal([]byte(out), &sc); err != nil {
			t.Fatalf("mg sidecar --json = %q: %v", out, err)
		}
		if sc.ID != id || sc.Status != wantStatus || sc.Result.Branch != "polecat-x" || sc.Result.Verdict != "pass" {
			t.Fatalf("mg sidecar --json = %+v; want %s %s with the --result payload", sc, id, wantStatus)
		}
	}
	readSidecar("done")

	code, out = mg("show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, out)
	}
	var shown struct {
		Status string
		Result struct{ Branch string }
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &shown); err != nil || shown.Status != "done" || shown.Result.Branch != "polecat-x" {
		t.Fatalf("mg show --json = %q (%v); want done with result.branch", out, err)
	}

	// A second completion merges into the stored result instead of
	// replacing it — the rule Done applies on every backend.
	if code, out := mg("reopen", id); code != 0 {
		t.Fatalf("mg reopen: exit %d\n%s", code, out)
	}
	if code, out := mg("done", id, `--result={"mr":"mr-1"}`); code != 0 {
		t.Fatalf("second mg done --result: exit %d\n%s", code, out)
	}
	got, err := mem.Companions().ReadResult(store.Ref{ID: id})
	if err != nil {
		t.Fatalf("result after second done: %v", err)
	}
	var merged map[string]string
	if err := json.Unmarshal(got, &merged); err != nil || merged["branch"] != "polecat-x" || merged["mr"] != "mr-1" {
		t.Fatalf("second --result replaced the first instead of merging: %s", got)
	}

	// The driver's pulse is a companion too.
	if code, out := mg("schedule"); code != 0 {
		t.Fatalf("mg schedule: exit %d\n%s", code, out)
	}
	if _, err := mem.Companions().ReadSweepStamp(); err != nil {
		t.Fatalf("mg schedule recorded no sweep stamp in the backend: %v", err)
	}

	if code, out := mg("archive", id); code != 0 {
		t.Fatalf("mg archive: exit %d\n%s", code, out)
	}
	readSidecar("archived")

	calls := mem.Calls()
	for _, op := range []string{"WriteResult", "ReadResult", "SaveBody", "ReadBody", "WriteSweepStamp", "MoveBodies"} {
		if calls[op] == 0 {
			t.Errorf("the backend's companion %s was never called (calls: %v)", op, calls)
		}
	}

	// Nothing under the workspace root. (countFiles' positive control runs in
	// TestCLILifecycleReachesANonFilesystemBackend.)
	if n := countFiles(t, root); n != 0 {
		t.Fatalf("mg wrote %d entries under the workspace root, bypassing the backend", n)
	}
}

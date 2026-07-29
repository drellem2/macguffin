package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `mg reclaim` is the macguffin half of pogo's worker started-signal (mg-7d6d):
// pogod claims a work item at spawn with its own PID, and the worker re-stamps
// the claim to its own PID as its first protocol act. The PID changing is the
// signal. pogo's capability probe is literally `mg reclaim --help`, so the help
// path is as load-bearing as the command, and pogo's duplicate-dispatch guard
// rests on `mg claim` refusing a non-available item, so `reclaim` must never be
// a route around that refusal. Both are pinned below.

// rcEnv builds a workspace and returns the binary, a clean env (POGO_PID
// stripped so PID resolution is deterministic) and the workspace root.
func rcEnv(t *testing.T) (bin string, env []string, root string) {
	t.Helper()
	home := t.TempDir()
	bin = buildBinary(t)
	env = emEnv(home)
	emInit(t, bin, env)
	return bin, env, filepath.Join(home, ".macguffin")
}

// rcClaimedFiles lists the names in work/claimed/ belonging to id.
func rcClaimedFiles(t *testing.T, root, id string) []string {
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

// The acceptance case: a claimed item, re-stamped to an explicit PID, exits 0
// and the file in claimed/ carries that PID.
func TestCLI_ReclaimReStampsThePIDOnAClaimedItem(t *testing.T) {
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "dispatched work")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "32194"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	out, code := snzRun(t, bin, env, "reclaim", id, "--pid", "40881")
	if code != 0 {
		t.Fatalf("mg reclaim: exit %d\n%s", code, out)
	}

	want := fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedFiles(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}

	// The transition is printed, so an operator reading a transcript can tell
	// which side of the handover they are looking at.
	for _, want := range []string{id, "32194", "40881"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not name %q; the transition must be printed:\n%s", want, out)
		}
	}
}

// Bare `mg reclaim <id>` is the invocation the worker prompt carries, so the
// default PID must be the calling process's — not an error, and not a no-op.
func TestCLI_ReclaimDefaultsThePID(t *testing.T) {
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "default pid handover")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "32194"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	out, code := snzRun(t, bin, env, "reclaim", id)
	if code != 0 {
		t.Fatalf("bare `mg reclaim %s`: exit %d\n%s", id, code, out)
	}

	names := rcClaimedFiles(t, root, id)
	if len(names) != 1 {
		t.Fatalf("claimed/ holds %v, want exactly one file", names)
	}
	if names[0] == fmt.Sprintf("%s.md.%d", id, 32194) {
		t.Errorf("the PID was not re-stamped: claimed/ still holds %s", names[0])
	}
	if !strings.HasPrefix(names[0], id+".md.") {
		t.Errorf("claimed file %s carries no PID suffix", names[0])
	}
}

// $POGO_PID is how pogo automation names the owning agent rather than the
// short-lived mg subprocess. reclaim reads it exactly as claim does — a reclaim
// that resolved the PID differently would re-stamp the wrong owner.
func TestCLI_ReclaimHonoursPOGOPID(t *testing.T) {
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "pogo-owned handover")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "32194"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	pogoEnv := append(env, "POGO_PID=55501")
	if out, code := snzRun(t, bin, pogoEnv, "reclaim", id); code != 0 {
		t.Fatalf("mg reclaim: exit %d\n%s", code, out)
	}
	want := fmt.Sprintf("%s.md.%d", id, 55501)
	if got := rcClaimedFiles(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s] from $POGO_PID", got, want)
	}

	// An explicit --pid still wins over the environment.
	if out, code := snzRun(t, bin, pogoEnv, "reclaim", id, "--pid", "40881"); code != 0 {
		t.Fatalf("mg reclaim --pid: exit %d\n%s", code, out)
	}
	want = fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedFiles(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s] — explicit --pid must beat $POGO_PID", got, want)
	}
}

// A worker that repeats its handover step after a context compaction must not
// get an error that reads as a failure.
func TestCLI_ReclaimToTheSamePIDIsANoOp(t *testing.T) {
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "already mine")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "40881"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	want := fmt.Sprintf("%s.md.%d", id, 40881)
	out, code := snzRun(t, bin, env, "reclaim", id, "--pid", "40881")
	if code != 0 {
		t.Fatalf("re-stamping the PID already recorded must exit 0, got %d\n%s", code, out)
	}
	if got := rcClaimedFiles(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s] — a no-op must change nothing", got, want)
	}
	if !strings.Contains(out, "40881") {
		t.Errorf("output does not name the PID on the claim:\n%s", out)
	}
}

// The item is in claimed/ at every observable point — asserted directly here by
// making available/ unwritable, which no unclaim+claim implementation survives.
// (internal/workitem also watches the directories across 200 re-stamps.)
func TestCLI_ReclaimNeverRoutesThroughAvailable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "must not leave claimed/")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "32194"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	avail := filepath.Join(root, "work", "available")
	if err := os.Chmod(avail, 0o500); err != nil {
		t.Fatalf("chmod available/: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(avail, 0o755) })

	out, code := snzRun(t, bin, env, "reclaim", id, "--pid", "40881")
	if code != 0 {
		t.Fatalf("mg reclaim failed with available/ read-only (exit %d) — it must never write there\n%s", code, out)
	}
	want := fmt.Sprintf("%s.md.%d", id, 40881)
	if got := rcClaimedFiles(t, root, id); len(got) != 1 || got[0] != want {
		t.Errorf("claimed/ holds %v, want exactly [%s]", got, want)
	}
}

// reclaim requires a claim. In particular an available item is REFUSED and left
// alone: a reclaim that fell back to claiming would be a `claim` with its
// atomic refusal — the fleet's duplicate-dispatch guard — bypassed.
func TestCLI_ReclaimRefusesAnAvailableItem(t *testing.T) {
	bin, env, root := rcEnv(t)
	id := emNew(t, bin, env, "nobody has claimed this")

	out, code := snzRun(t, bin, env, "reclaim", id, "--pid", "40881")
	if code != 4 {
		t.Fatalf("exit code = %d, want 4 (conflict)\n%s", code, out)
	}
	if names := rcClaimedFiles(t, root, id); len(names) != 0 {
		t.Errorf("a refused reclaim claimed the item anyway: claimed/ holds %v", names)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", id+".md")); err != nil {
		t.Errorf("item is no longer in available/ after a refused reclaim: %v", err)
	}
	if !strings.Contains(out, "mg claim") {
		t.Errorf("the refusal must name `mg claim` as the remedy:\n%s", out)
	}
}

func TestCLI_ReclaimRefusesADoneItem(t *testing.T) {
	bin, env, _ := rcEnv(t)
	id := emNew(t, bin, env, "finished work")

	if out, code := snzRun(t, bin, env, "claim", id, "--pid", "32194"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	if out, code := snzRun(t, bin, env, "done", id); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}

	out, code := snzRun(t, bin, env, "reclaim", id, "--pid", "40881")
	if code != 4 {
		t.Errorf("exit code = %d, want 4 (conflict) on a done item\n%s", code, out)
	}
}

func TestCLI_ReclaimUnknownIDIsNotFound(t *testing.T) {
	bin, env, _ := rcEnv(t)

	out, code := snzRun(t, bin, env, "reclaim", "mg-zzzz", "--pid", "40881")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (not_found) on an unknown id\n%s", code, out)
	}
}

// pogo's capability probe is `mg reclaim --help`. A command that exists but
// whose help errors leaves the pogo half switched off, so this is not a
// formality.
func TestCLI_ReclaimHelpExitsZero(t *testing.T) {
	bin, env, _ := rcEnv(t)

	out, code := snzRun(t, bin, env, "reclaim", "--help")
	if code != 0 {
		t.Fatalf("`mg reclaim --help` exited %d; pogo's capability probe is exactly this command\n%s", code, out)
	}
	for _, want := range []string{"reclaim", "--pid"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output does not mention %q:\n%s", want, out)
		}
	}
}

// `mg schema` is the agent-facing discovery surface; a command missing from it
// is a command consumers cannot find.
func TestCLI_ReclaimAppearsInSchema(t *testing.T) {
	bin, env, _ := rcEnv(t)

	out := emOK(t, bin, env, "schema")
	var doc schemaDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("mg schema is not valid JSON: %v", err)
	}

	var found *schemaCommand
	for i := range doc.Command.Commands {
		if doc.Command.Commands[i].Path == "mg reclaim" {
			found = &doc.Command.Commands[i]
		}
	}
	if found == nil {
		t.Fatal("`mg reclaim` does not appear in `mg schema`")
	}
	if !found.Mutates {
		t.Error("mg reclaim is classified as non-mutating; it renames a claim")
	}
	hasPID := false
	for _, f := range found.Flags {
		if f.Name == "pid" {
			hasPID = true
		}
	}
	if !hasPID {
		t.Errorf("mg reclaim's schema entry does not list --pid: %+v", found.Flags)
	}
}

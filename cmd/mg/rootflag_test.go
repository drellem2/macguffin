package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- workspace isolation (mg-4fa7) ------------------------------------------
//
// mg resolves its store as: --root flag > $MG_ROOT > $HOME/.macguffin. It never
// walks up from the working directory, so `cd` isolates nothing. Before this
// existed, a smoke script that ran `mg claim`/`mg done` from a scratch dir hit
// the REAL store and marked a live work item done.
//
// These tests observe END STATE — they checksum the "real" store's bytes before
// and after, rather than asserting that some code path was taken. A store the
// command must not touch is seeded with real content (an item and an event log)
// so the digest has something to lose.

// storeDigest hashes every file under dir: relative path, size, and contents.
// Empty dir trees hash to a constant, so a digest match on an EMPTY tree proves
// nothing — seed the store first.
func storeDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var files int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(h, "%s\x00", rel)
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		files++
		return nil
	})
	if err != nil {
		t.Fatalf("digesting %s: %v", dir, err)
	}
	if files == 0 {
		t.Fatalf("store %s has no files; digest would be vacuous", dir)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// mgRun runs the mg binary with an explicit workspace environment.
//   - home is exported as $HOME (so $HOME/.macguffin is the fallback store)
//   - env is exported as $MG_ROOT; "" exports it empty (which must fall through)
//   - cwd is the working directory, to prove it has no bearing on the store
//
// It returns stdout only, so a test can assert the stdout contract stays clean.
func mgRun(t *testing.T, bin, home, env, cwd string, args ...string) string {
	t.Helper()
	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "MG_ROOT="+env)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg %s failed: %v\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// seedStore initializes a workspace and puts one item in it, so the tree has
// bytes worth protecting.
func seedStore(t *testing.T, bin, root string) {
	t.Helper()
	// cwd is an unrelated existing dir: root itself may not exist yet, and the
	// working directory has no bearing on where the store lands anyway.
	cwd := t.TempDir()
	mgRun(t, bin, cwd, "", cwd, "--root="+root, "init")
	mgRun(t, bin, cwd, "", cwd, "--root="+root, "new", "seeded item")
}

// availableIDs lists the item IDs sitting in work/available under root.
func availableIDs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "work", "available"))
	if err != nil {
		t.Fatalf("reading available items in %s: %v", root, err)
	}
	var ids []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return ids
}

// TestCLI_CdDoesNotTouchRealStore is the mg-ba58 incident, frozen as a test.
// Running mg's mutating verbs from a scratch working directory, with $MG_ROOT
// pointed at a scratch store, must leave $HOME/.macguffin byte-identical.
func TestCLI_CdDoesNotTouchRealStore(t *testing.T) {
	bin := buildBinary(t)
	realHome := t.TempDir()
	realStore := filepath.Join(realHome, ".macguffin")
	scratch := filepath.Join(t.TempDir(), "scratch-store")
	elsewhere := t.TempDir() // an unrelated cwd, as a smoke script would use

	// The "real" store: initialized, with a live item in it.
	mgRun(t, bin, realHome, "", realHome, "init")
	mgRun(t, bin, realHome, "", realHome, "new", "a live ticket nobody may close")
	before := storeDigest(t, realStore)

	// Everything below runs from `elsewhere`, against the scratch store.
	mgRun(t, bin, realHome, scratch, elsewhere, "init")
	mgRun(t, bin, realHome, scratch, elsewhere, "new", "scratch item")

	ids := availableIDs(t, scratch)
	if len(ids) != 1 {
		t.Fatalf("scratch store has %d available items, want 1: %v", len(ids), ids)
	}
	mgRun(t, bin, realHome, scratch, elsewhere, "claim", ids[0])
	mgRun(t, bin, realHome, scratch, elsewhere, "done", ids[0], `--result={"ok":true}`)

	if after := storeDigest(t, realStore); after != before {
		t.Errorf("$HOME/.macguffin changed while MG_ROOT pointed elsewhere\nbefore: %s\nafter:  %s", before, after)
	}
	// And the work really did happen — in the scratch store.
	if _, err := os.Stat(filepath.Join(scratch, "work", "done", ids[0]+".md")); err != nil {
		t.Errorf("claim/done did not land in the scratch store: %v", err)
	}
	if got := len(availableIDs(t, realStore)); got != 1 {
		t.Errorf("real store has %d available items, want its 1 untouched item", got)
	}
}

// TestCLI_RootFlagBeatsEnv: --root wins over $MG_ROOT, and $MG_ROOT wins over
// $HOME. Asserted by which store the new item actually lands in.
func TestCLI_RootFlagBeatsEnv(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	homeStore := filepath.Join(home, ".macguffin")
	envRoot := filepath.Join(t.TempDir(), "env-store")
	flagRoot := filepath.Join(t.TempDir(), "flag-store")

	seedStore(t, bin, homeStore)
	seedStore(t, bin, envRoot)
	seedStore(t, bin, flagRoot)
	homeBefore, envBefore := storeDigest(t, homeStore), storeDigest(t, envRoot)

	// --root beats MG_ROOT beats HOME.
	mgRun(t, bin, home, envRoot, t.TempDir(), "--root="+flagRoot, "new", "flag item")
	if got := len(availableIDs(t, flagRoot)); got != 2 {
		t.Errorf("--root store has %d items, want 2 (seed + flag item)", got)
	}
	if storeDigest(t, envRoot) != envBefore {
		t.Error("--root did not beat $MG_ROOT: the env store changed")
	}
	if storeDigest(t, homeStore) != homeBefore {
		t.Error("--root did not beat $HOME: the home store changed")
	}

	// MG_ROOT beats HOME when no flag is given.
	mgRun(t, bin, home, envRoot, t.TempDir(), "new", "env item")
	if got := len(availableIDs(t, envRoot)); got != 2 {
		t.Errorf("$MG_ROOT store has %d items, want 2 (seed + env item)", got)
	}
	if storeDigest(t, homeStore) != homeBefore {
		t.Error("$MG_ROOT did not beat $HOME: the home store changed")
	}

	// With neither, HOME is the store. (MG_ROOT="" must not mean "cwd".)
	mgRun(t, bin, home, "", t.TempDir(), "new", "home item")
	if got := len(availableIDs(t, homeStore)); got != 2 {
		t.Errorf("$HOME store has %d items, want 2 (seed + home item)", got)
	}
}

// TestCLI_EventAppendRejectsRootFlag: `mg event append` forwards its args
// verbatim, so cobra never binds --root there. Passing it must be a usage error,
// not a junk "root" field appended to the store the caller meant to avoid.
// $MG_ROOT is the supported lever, and it must still work.
func TestCLI_EventAppendRejectsRootFlag(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	homeStore := filepath.Join(home, ".macguffin")
	other := filepath.Join(t.TempDir(), "other")
	seedStore(t, bin, homeStore)
	seedStore(t, bin, other)
	homeBefore := storeDigest(t, homeStore)

	cmd := exec.Command(bin, "event", "append", "work.claim", "--root="+other)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "MG_ROOT=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("mg event append --root=... succeeded; want a usage error\noutput: %s", out)
	}
	if !strings.Contains(string(out), "MG_ROOT") {
		t.Errorf("error should point at $MG_ROOT, got: %s", out)
	}
	if after := storeDigest(t, homeStore); after != homeBefore {
		t.Error("rejected `event append --root` still wrote to $HOME/.macguffin")
	}

	// The supported lever still works, and lands the event in the right store.
	mgRun(t, bin, home, other, t.TempDir(), "event", "append", "work.claim")
	if _, err := os.Stat(filepath.Join(other, "events.jsonl")); err != nil {
		t.Errorf("$MG_ROOT event append did not land in %s: %v", other, err)
	}
	if after := storeDigest(t, homeStore); after != homeBefore {
		t.Error("$MG_ROOT event append wrote to $HOME/.macguffin")
	}
}

// TestCLI_RootFlagKeepsJSONStdoutClean guards the mg-fb07 contract: an isolated
// root must not leak advisory chatter into the --json stdout stream.
func TestCLI_RootFlagKeepsJSONStdoutClean(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "store")
	seedStore(t, bin, root)

	out := mgRun(t, bin, home, "", t.TempDir(), "--root="+root, "list", "--json")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "{") {
			t.Errorf("non-JSON line on stdout of `mg --root=... list --json`: %q", line)
		}
	}
}

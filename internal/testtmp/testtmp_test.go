package testtmp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The load-bearing case in this file is TestReapKeepsALiveOwnersEntry.
//
// Every other assertion here is about reclaiming disk, and the worst outcome of
// getting one wrong is that the leak this package exists to stop carries on —
// visible, measurable, and no worse than before it was written. Getting the LIVE
// direction wrong is a different kind of failure: this box runs several polecats
// and a refinery gate at once, so a sweep that deletes a running suite's
// fixtures surfaces as a branch defect against code that is fine, which is the
// exact shape of the failure this package was written for. A sweep observed only
// deleting things has not been observed working.

// TestMain is the package's own adoption of Run — the same call every other test
// package in this repo makes, and the reason this suite's t.TempDir() calls do
// not land in $TMPDIR either.
func TestMain(m *testing.M) { os.Exit(Run("testtmp", m.Run)) }

// TestReapKeepsALiveOwnersEntry — the direction that must never regress.
func TestReapKeepsALiveOwnersEntry(t *testing.T) {
	root := t.TempDir()
	// This process is the one pid the test can be certain is alive.
	live := filepath.Join(root, entryName("cwd", os.Getpid(), 1))
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	// Old enough that any age-based rule would delete it. Ownership must win.
	old := time.Now().Add(-100 * StaleAfter)
	if err := os.Chtimes(live, old, old); err != nil {
		t.Fatal(err)
	}

	Reap(root)

	if _, err := os.Stat(live); err != nil {
		t.Fatalf("Reap deleted a LIVE process's directory (%s): %v\n"+
			"This is the failure the package exists to prevent arriving by a new route: "+
			"a running suite loses its fixtures and reports a branch defect.", live, err)
	}
}

// TestReapRemovesADeadOwnersEntry is the reclaiming direction. Without it the
// package is a rename of the problem.
func TestReapRemovesADeadOwnersEntry(t *testing.T) {
	root := t.TempDir()
	dead := filepath.Join(root, entryName("workitem", deadPID(t), 1))
	if err := os.MkdirAll(filepath.Join(dead, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Brand new, so nothing here is reclaimed by age. Ownership is the reason.
	Reap(root)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("Reap kept a dead process's directory (%s): stat err = %v", dead, err)
	}
}

// TestReapAgesOutOnlyEntriesThatCarryNoPID pins the fallback branch in both
// directions. An entry this package did not name is reclaimed by age and by
// nothing else — reaping it eagerly would let the sweep delete whatever else a
// future caller decides to keep in Root().
func TestReapAgesOutOnlyEntriesThatCarryNoPID(t *testing.T) {
	root := t.TempDir()
	recent := filepath.Join(root, "not-one-of-ours")
	stale := filepath.Join(root, "also-not-ours")
	for _, d := range []string{recent, stale} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-StaleAfter - time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	Reap(root)

	if _, err := os.Stat(recent); err != nil {
		t.Errorf("Reap deleted an unowned entry that is younger than StaleAfter (%s): %v", recent, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("Reap kept an unowned entry older than StaleAfter (%s): stat err = %v", stale, err)
	}
}

// TestReapTouchesNothingOutsideTheRootItWasGiven. The sweep runs on a box whose
// $TMPDIR holds tens of thousands of other people's directories; "it only ever
// looks inside Root()" is the property that makes it safe to run unattended.
func TestReapTouchesNothingOutsideTheRootItWasGiven(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	// A sibling of root, named exactly like a reapable entry, owned by a dead
	// pid, and old. Everything about it says "delete me" except its location.
	sibling := filepath.Join(parent, entryName("workitem", deadPID(t), 7))
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-100 * StaleAfter)
	if err := os.Chtimes(sibling, old, old); err != nil {
		t.Fatal(err)
	}

	Reap(root)

	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("Reap left its root and deleted a sibling (%s): %v", sibling, err)
	}
}

// TestDirNestsEverythingUnderOneRoot is the acceptance criterion in miniature:
// $TMPDIR's entry count must stop growing with the number of directories a test
// binary needs.
func TestDirNestsEverythingUnderOneRoot(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	for _, purpose := range []string{"cwd", "workitem", "cwd"} {
		dir, err := Dir(purpose)
		if err != nil {
			t.Fatalf("Dir(%q): %v", purpose, err)
		}
		t.Cleanup(func() { _ = Remove(dir) })
		if filepath.Dir(dir) != root {
			t.Errorf("Dir(%q) = %s, which is not directly inside Root() = %s", purpose, dir, root)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("Dir(%q) returned a path that does not exist: %v", purpose, err)
		}
		if pid, ok := ownerPID(filepath.Base(dir)); !ok || pid != os.Getpid() {
			t.Errorf("Dir(%q) = %s: name does not encode this process's pid (%d), so Reap could "+
				"only ever age it out", purpose, dir, os.Getpid())
		}
	}
}

// TestDirRefusesAPurposeReapCouldNotParse. A dot in the label silently converts
// an owned entry into one the sweep can only age out — the failure is not the
// bad name, it is that the name still works.
func TestDirRefusesAPurposeReapCouldNotParse(t *testing.T) {
	for _, purpose := range []string{"", "a.b", "a/b", "store.mg"} {
		if _, err := Dir(purpose); err == nil {
			t.Errorf("Dir(%q) was accepted; a name Reap cannot parse must be refused at the call", purpose)
		}
	}
}

// TestOwnerPIDReadsBackWhatEntryNameWrote — the two halves of the naming are the
// only contract between a process that creates a directory and a process that
// decides whether to delete it. The shell half (scripts/lib/testtmp.sh) is a
// third party to the same contract, which is why the shape is pinned here rather
// than left implicit in fmt.Sprintf.
func TestOwnerPIDReadsBackWhatEntryNameWrote(t *testing.T) {
	name := entryName("test-shadow", 4242, 9)
	if name != "test-shadow.4242.9" {
		t.Errorf("entryName = %q, want \"test-shadow.4242.9\"", name)
	}
	// The tail field is not parsed by either half, and that is deliberate: this
	// package writes a counter there and scripts/lib/testtmp.sh writes mktemp's
	// suffix. What both halves must agree on is three dot-separated fields with
	// a numeric pid in the middle.
	if pid, ok := ownerPID("test-shadow.4242.ZWSdk9"); !ok || pid != 4242 {
		t.Errorf("ownerPID could not read the shell half's name shape; its directories "+
			"would fall to the age rule and be kept for %v", StaleAfter)
	}
	pid, ok := ownerPID(name)
	if !ok || pid != 4242 {
		t.Errorf("ownerPID(%q) = %d, %v; want 4242, true", name, pid, ok)
	}
	for _, bad := range []string{"cwd", "cwd.notanumber.1", "cwd.0.1", "cwd.-3.1", "a.1.2.3"} {
		if _, ok := ownerPID(bad); ok {
			t.Errorf("ownerPID(%q) claimed an owner; an unparseable name must fall to the age rule", bad)
		}
	}
}

// TestDirClearsADirectoryLeftByARecycledPID.
//
// The remedy is an artifact of the same kind as the defect, so it is subject to
// it: the sweep keeps every entry whose pid is alive, and once this process has
// been handed a recycled pid, a DEAD namesake's directory reads as live and is
// kept forever. Reusing it would hand this binary a scratch tree from a run that
// ended days ago, and the phantom files would be read as a defect in whichever
// test found them.
func TestDirClearsADirectoryLeftByARecycledPID(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	// Stage exactly what a namesake leaves behind: the name Dir is about to
	// mint, already present, with state inside it.
	next := entryName("recycled", os.Getpid(), seq.Load()+1)
	stale := filepath.Join(root, next)
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	ghost := filepath.Join(stale, "items.jsonl")
	if err := os.WriteFile(ghost, []byte("a dead run's records\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, err := Dir("recycled")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	t.Cleanup(func() { _ = Remove(dir) })
	if dir != stale {
		t.Skipf("another Dir call raced this one; got %s, wanted the staged %s", dir, stale)
	}
	if _, err := os.Stat(ghost); !os.IsNotExist(err) {
		t.Fatalf("Dir handed back a recycled pid's directory with the dead run's state still in it "+
			"(%s): stat err = %v", ghost, err)
	}
}

// TestShellHalfAgreesOnTheRoot. scripts/lib/testtmp.sh sweeps and nests into the
// same directory as this package, and it names that directory with a literal
// because it runs from several working directories. Two literals that must match
// are a rename waiting to become two roots — one of them unswept — so the pairing
// is asserted rather than left to a comment.
func TestShellHalfAgreesOnTheRoot(t *testing.T) {
	const rel = "../../scripts/lib/testtmp.sh"
	src, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("the shell half is missing (%s): %v\n"+
			"If it moved, this test moves with it — the point is that neither half can be "+
			"renamed alone.", rel, err)
	}
	want := "TESTTMP_ROOT_NAME=" + RootName + "\n"
	if !strings.Contains(string(src), want) {
		t.Errorf("%s does not set %s\n"+
			"The two halves would sweep different roots, and whichever one no live process "+
			"writes to would go unreclaimed.", rel, strings.TrimSuffix(want, "\n"))
	}
}

// TestRemoveDefeatsAReadOnlyModuleCache is failure mode 3, staged directly.
//
// Go writes its module cache read-only — 0444 files inside 0555 directories —
// and a scratch root standing in for $HOME collects one the moment a test shells
// out to `go build`. os.RemoveAll stops at the first EACCES, so the largest
// thing in the nest is what survives.
func TestRemoveDefeatsAReadOnlyModuleCache(t *testing.T) {
	nest := filepath.Join(t.TempDir(), "home")
	cache := filepath.Join(nest, "go", "pkg", "mod", "example.com", "m@v1.0.0")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "go.mod"), []byte("module m\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	// The directory permissions are the part that matters: a 0555 directory
	// cannot have its children unlinked, whatever their own modes are.
	if err := os.Chmod(cache, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })

	// The control: plain os.RemoveAll must FAIL here, or this test is asserting
	// nothing about Remove that os.RemoveAll would not already give.
	if err := os.RemoveAll(nest); err == nil {
		t.Skip("os.RemoveAll removed a 0555 directory's contents on this platform; " +
			"nothing here distinguishes Remove from it")
	}

	if err := Remove(nest); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(nest); !os.IsNotExist(err) {
		t.Fatalf("Remove left the read-only nest behind (%s): stat err = %v", nest, err)
	}
}

// TestRemoveReportsWhatItCouldNotDelete. The lesson of failure mode 3 is not
// only that the removal stopped — it is that the teardown ignored the error, so
// the biggest thing in $TMPDIR was unreclaimed and nothing said so.
func TestRemoveReportsWhatItCouldNotDelete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can unlink through any permission; there is no undeletable tree to stage")
	}
	// The un-chmod-able case: the PARENT is not ours to write, so neither the
	// first pass nor the chmod retry can unlink the child.
	base := t.TempDir()
	parent := filepath.Join(base, "sealed")
	if err := os.MkdirAll(filepath.Join(parent, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	// Remove is handed the CHILD, so the chmod pass can reach nothing that would
	// help: the permission it needs is on a directory outside the tree.
	err := Remove(filepath.Join(parent, "child"))
	if err == nil {
		t.Skip("this filesystem allowed the unlink; nothing undeletable could be staged")
	}
	if !strings.Contains(err.Error(), "child") {
		t.Errorf("Remove error does not name the path it failed on: %v", err)
	}
}

// TestRunNestsTheTestBinarysOwnTMPDIR is the whole scheme, end to end, measured
// the way the ticket asks for: count $TMPDIR before, run a binary that makes
// temp directories, count after.
//
// It runs a CHILD copy of this test binary because Run is a TestMain-level call
// — the only honest way to observe it is from outside the process it wraps.
func TestRunNestsTheTestBinarysOwnTMPDIR(t *testing.T) {
	base := t.TempDir()

	before, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("the measured $TMPDIR was not empty to begin with: %v", before)
	}

	out := runChildProbe(t, base)

	// 1. The child's own $TMPDIR was inside our root, so t.TempDir() nested too.
	root := filepath.Join(base, RootName)
	probe := probeField(t, out, "tmpdir")
	if filepath.Dir(probe) != root {
		t.Errorf("the child's $TMPDIR was %s, which is not directly inside %s", probe, root)
	}
	if pid, ok := ownerPID(filepath.Base(probe)); !ok || pid <= 0 {
		t.Errorf("the child's $TMPDIR %s does not carry a pid the sweep can read, so a crashed "+
			"run's tree could only ever be aged out", probe)
	}
	nested := probeField(t, out, "ttmp")
	if !strings.HasPrefix(nested, probe+string(filepath.Separator)) {
		t.Errorf("t.TempDir() returned %s, which is not inside the child's pinned $TMPDIR %s\n"+
			"Without that nesting this package bounds only the directories asked for by name, "+
			"and the testing package's own stay one $TMPDIR entry per test.", nested, probe)
	}

	// 2. The acceptance criterion: $TMPDIR gained exactly one entry, the root.
	after, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Name() != RootName {
		names := make([]string, 0, len(after))
		for _, e := range after {
			names = append(names, e.Name())
		}
		t.Fatalf("a run left %d entries in $TMPDIR (%v), want exactly 1 (%s)", len(after), names, RootName)
	}

	// 3. And the root itself is empty again, because Run removed what it made.
	// Nesting alone would only rename the problem one level down.
	left, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("the child's directory survived its own exit: %v", names)
	}
}

// TestRootRefusesASymlink. os.TempDir falls back to a world-writable /tmp when
// TMPDIR is unset, which is the case in CI; MkdirAll follows a link there and
// reports success, so the sweep would go on to delete a tree of somebody else's
// choosing. The refusal has to be explicit, and the only way to observe it is
// against a root this test supplies.
func TestRootRefusesASymlink(t *testing.T) {
	// Root memoises per process, so the check is exercised through a private
	// TMPDIR in a child `go test` rather than by re-entering Root here.
	base := t.TempDir()
	elsewhere := filepath.Join(base, "somebody-elses-tree")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, RootName)); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	// Any test name will do: the refusal is raised the first time anything in
	// the child asks for a directory, which is its TestMain — earlier than any
	// assertion could run. That is the intended shape. A run that cannot get a
	// private root must make no claim about the tree.
	cmd := exec.Command(os.Args[0], "-test.run=TestOwnerPIDReadsBackWhatEntryNameWrote", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "TMPDIR="+base)
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("the child accepted a symlinked root; the sweep would delete through it:\n%s", out)
	}
	if !strings.Contains(string(out), "is a symlink") {
		t.Errorf("the child failed, but not with the symlink refusal — so this case is not "+
			"observing what it claims to:\n%s", out)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Errorf("the symlink target was disturbed: %v", err)
	}
}

// probeEnv opts a child process into TestTMPDIRProbe. Without it the probe is a
// no-op, so a normal `go test ./...` neither runs nor reports it.
const probeEnv = "MG_TESTTMP_PROBE"

// TestTMPDIRProbe reports where the child process's temp directories actually
// landed. It is the observed half of TestRunNestsTheTestBinarysOwnTMPDIR and is
// skipped in every other run.
func TestTMPDIRProbe(t *testing.T) {
	if os.Getenv(probeEnv) == "" {
		t.Skip("child-process probe; driven by TestRunNestsTheTestBinarysOwnTMPDIR")
	}
	fmt.Printf("PROBE tmpdir=%s\n", os.TempDir())
	fmt.Printf("PROBE ttmp=%s\n", t.TempDir())
}

// runChildProbe runs this test binary's probe with $TMPDIR pinned to base, and
// returns its output.
func runChildProbe(t *testing.T, base string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestTMPDIRProbe", "-test.v", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "TMPDIR="+base, probeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child probe failed (%v):\n%s", err, out)
	}
	return string(out)
}

// probeField pulls one "PROBE <key>=<value>" line out of the child's output.
func probeField(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "PROBE "+key+"="); ok {
			return v
		}
	}
	t.Fatalf("the child printed no %q line; it did not report what this test measures:\n%s", key, out)
	return ""
}

// deadPID returns the pid of a process that has certainly exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a throwaway process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for a throwaway process: %v", err)
	}
	if pidAlive(pid) {
		t.Skipf("pid %d was reused before the assertion could run", pid)
	}
	return pid
}

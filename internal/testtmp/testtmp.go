// Package testtmp owns the temp directories a macguffin TEST BINARY creates —
// the ones nothing else will ever delete.
//
// # Why this package exists
//
// On 2026-08-13 the host disk reached 100% capacity with 204Mi free, and every
// merge gate on the box began failing with Errno 28. That presents as a random
// branch defect: the gate that dies is whichever one happens to run when the
// disk crosses, so the branch that gets rejected has nothing to do with the
// cause. pogo fixed its own side first (mg-de3c, mg-60eb) and measured that the
// remaining producers were not pogo's — this repo's harness prefixes were among
// the named ones (mg-cc3f).
//
// Two mechanisms leaked, and neither is a missing `defer`:
//
//   - cmd/mg's TestMain created a neutral working directory with os.MkdirTemp
//     and removed it after m.Run(). That cleanup runs on the SUCCESS path only:
//     a panic, a `-timeout` expiry (Go implements that by panicking) or a kill
//     skips it, and a test binary's generated main() ends in os.Exit, which runs
//     no deferred function anywhere. So it leaked exactly when tests fail, which
//     is when they are run most.
//   - t.TempDir() is cleaned up by the testing package on the same success path,
//     and it creates its directory directly in $TMPDIR — one entry per test, not
//     per run.
//
// # What it does instead
//
// One directory in $TMPDIR — Root() — with every test-mode directory nested
// inside it, named for the process that owns it. That alone fixes the SHAPE of
// the problem: $TMPDIR's entry count stops growing with the number of test runs
// and starts being 1. Run() then points the test binary's own $TMPDIR at its
// per-process entry, so t.TempDir() and every subprocess land inside it too and
// are reclaimed as one unit.
//
// Nesting alone would not bound the DISK, so Root() is also swept. Reap runs
// once per process, on the first Dir call, and the rule it applies is
// OWNERSHIP:
//
//   - the name encodes a pid and that process is alive — keep, at any age;
//   - the name encodes a pid and that process is gone — remove;
//   - the name encodes no pid — remove once it is older than StaleAfter.
//
// Ownership rather than age, because age is the reading that gets this wrong in
// the expensive direction. This box runs several polecats and a refinery gate
// concurrently, so at any instant a sibling entry is very likely another agent's
// LIVE test binary; a sweep that deleted a running suite's fixtures would
// surface as a branch defect, which is exactly the failure this package exists
// to stop, arriving by a new route. A pid answers "is anyone still using this"
// directly, and signal 0 answers it without a race worth naming: a process that
// dies between the readdir and the remove needed the directory right up until it
// didn't.
//
// The cost of that rule is that a crashed run's fixtures are gone by the next
// `go test` rather than sitting in $TMPDIR for a while. That is the intended
// trade: the instrument for a failed test is its output, and the alternative —
// keeping unowned state around on the chance someone looks — is the habit that
// filled the disk.
//
// # What it deliberately does not do
//
// It does not touch anything outside Root(). In particular it has no opinion
// about $TMPDIR at large: reclaiming what has already leaked is a separate,
// careful operation from stopping the leak, and this package is the second one.
//
// The shell half of the same scheme — the same root, the same names, the same
// ownership rule — is scripts/lib/testtmp.sh, and scripts/test-tmpdir-leak.sh
// measures both.
package testtmp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// RootName is the single directory this package owns inside $TMPDIR.
//
// Short and unmistakably ours: a human staring at an `ls $TMPDIR` that has gone
// wrong should be able to tell in one line whether macguffin is the cause. The
// shell half reads this same name out of this file — see scripts/lib/testtmp.sh
// — so renaming it here cannot leave the two halves sweeping different roots.
const RootName = "macguffin-test-tmp"

// StaleAfter is how long an entry whose name encodes NO pid must have been idle
// before Reap will remove it.
//
// It is the fallback rule, not the main one — every name this package writes
// carries a pid, so an entry reaching this branch was put here by something
// else, or by a version of this package that predates the naming. Two hours,
// against a full `./test.sh` that measures well under ten minutes; the margin is
// caution about the mtime reading, which does not advance for writes NESTED
// inside a directory, only for entries created or removed directly in it.
var StaleAfter = 2 * time.Hour

var (
	seq       atomic.Int64
	reapOnce  sync.Once
	rootOnce  sync.Once
	rootErr   error
	rootCache string
)

// Root returns the directory this package owns inside $TMPDIR, creating it on
// first use.
//
// It is resolved once per process. $TMPDIR is read at that moment, so a caller
// that pins TMPDIR must do so before the first Dir call — which is exactly what
// Run does, and in the opposite order to the one that would break this: it
// resolves the root from the ambient $TMPDIR and only then repoints $TMPDIR
// inside it.
func Root() (string, error) {
	rootOnce.Do(func() {
		rootCache = filepath.Join(os.TempDir(), RootName)
		// Lstat, not Stat, and it runs before MkdirAll rather than instead of
		// it. $TMPDIR is per-user and 0700 on darwin, but os.TempDir falls back
		// to a world-writable /tmp when TMPDIR is unset — which is the case in
		// CI — and there a pre-planted symlink at this name would have the sweep
		// deleting a directory tree of somebody else's choosing. MkdirAll
		// follows the link and reports success, so the refusal has to be
		// explicit.
		if fi, err := os.Lstat(rootCache); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			rootErr = fmt.Errorf("testtmp: %s is a symlink; refusing to create or sweep through it", rootCache)
			return
		}
		// 0o700: these hold test fixtures that stand in for a user's own
		// ~/.macguffin store.
		rootErr = os.MkdirAll(rootCache, 0o700)
	})
	return rootCache, rootErr
}

// Dir creates and returns a directory private to this process, named for
// purpose.
//
// purpose is a short label naming what the directory holds ("cwd", "workitem"),
// not a path: it appears verbatim in the directory name, so it must contain no
// dot and no separator. Dir rejects such a label rather than producing a name
// Reap cannot parse — an unparseable name is one Reap can only age out, which
// silently converts a pid-owned entry into a two-hour one.
//
// The first call in a process also runs Reap. That is the only sweep trigger:
// the sweep costs one readdir of a directory holding, at steady state, the live
// test binaries on this box, and paying it once per process keeps it off every
// subsequent lookup.
func Dir(purpose string) (string, error) {
	if purpose == "" {
		return "", fmt.Errorf("testtmp: empty purpose")
	}
	if strings.ContainsAny(purpose, "./"+string(filepath.Separator)) {
		return "", fmt.Errorf("testtmp: purpose %q must not contain a dot or a separator", purpose)
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	reapOnce.Do(func() { Reap(root) })

	dir := filepath.Join(root, entryName(purpose, os.Getpid(), seq.Add(1)))
	// os.Mkdir rather than MkdirAll, because "it already exists" has to be an
	// answer here rather than a success.
	//
	// pids are reused. The sweep keeps any entry whose pid is alive, and once
	// this process has been given a recycled pid, the DEAD namesake's directory
	// reads as live and is kept — so MkdirAll would hand this binary a scratch
	// tree belonging to a run that ended days ago, and the resulting phantom
	// files would look like a defect in whatever test read them. It cannot be a
	// directory this process made: seq is monotonic and this value has not been
	// issued before. So it is the namesake's, its owner is provably gone, and
	// clearing it is the one correct move.
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("testtmp: create %s: %w", dir, err)
		}
		if err := Remove(dir); err != nil {
			return "", fmt.Errorf("testtmp: clear a recycled pid's stale %s: %w", dir, err)
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", fmt.Errorf("testtmp: create %s: %w", dir, err)
		}
	}
	return dir, nil
}

// Run is what a TestMain calls. It gives the test binary a private, swept scratch
// directory, points $TMPDIR at it for the duration of the run, and returns the
// exit code the caller must pass to os.Exit:
//
//	func TestMain(m *testing.M) { os.Exit(testtmp.Run("workitem", m.Run)) }
//
// The signature is the structural half of the fix and is not a style choice.
// The directory's owner is this function, and it returns an exit CODE rather
// than calling os.Exit itself, so there is no arm on which a caller can leave
// past the cleanup — which is precisely what the old cmd/mg TestMain did on
// every one of its error paths.
//
// Redirecting $TMPDIR is what pulls t.TempDir() and every subprocess in with it.
// Without that, this package would bound only the directories it is asked for by
// name, and the testing package's own would go on being one $TMPDIR entry per
// test.
func Run(purpose string, run func() int) int {
	dir, err := Dir(purpose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testtmp: %v\n", err)
		return 1
	}
	// Restoring the old value matters even though the process is about to exit:
	// it keeps Run composable with a TestMain that does its own setup around it.
	old, had := os.LookupEnv("TMPDIR")
	if err := os.Setenv("TMPDIR", dir); err != nil {
		fmt.Fprintf(os.Stderr, "testtmp: pin TMPDIR to %s: %v\n", dir, err)
		// This arm is the defect this package exists to fix, in the fix itself:
		// an error path that returns past the cleanup for a directory it has
		// already created. The sweep would reclaim it on the next run, which is
		// exactly the excuse that produced the original leak.
		if err := Remove(dir); err != nil {
			fmt.Fprintf(os.Stderr, "testtmp: %v\n", err)
		}
		return 1
	}

	code := run()

	if had {
		os.Setenv("TMPDIR", old)
	} else {
		os.Unsetenv("TMPDIR")
	}
	// REPORTED, not swallowed, and not fatal. A teardown that ignored EACCES is
	// how the largest thing in a leaked nest went unreclaimed with nothing
	// saying so, so the error is printed where a human running the suite will
	// see it. It does not change the exit code: the sweep reclaims this entry on
	// the next run regardless, and a passing suite must not go red over temp
	// bookkeeping.
	if err := Remove(dir); err != nil {
		fmt.Fprintf(os.Stderr, "testtmp: %v\n", err)
	}
	return code
}

// Remove deletes a tree that os.RemoveAll alone may not be able to.
//
// A scratch root that stands in for $HOME collects $HOME/go/pkg/mod the moment
// anything under it shells out to `go build`, and Go writes its module cache
// READ-ONLY: 0444 files inside 0555 directories. os.RemoveAll cannot unlink a
// child of a directory it may not write, so it returns EACCES at the first one
// and stops — leaving the largest thing in the nest behind. macguffin's tests do
// build the mg binary, so this is not a hypothetical import from pogo.
//
// The retry chmods every directory back to 0700 (we own them; nothing else can
// have put them here) and removes again.
func Remove(path string) error {
	first := os.RemoveAll(path)
	if first == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Keep walking: an unreadable subtree is exactly the case this pass
			// exists for, and stopping here would leave its siblings unfixed.
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("testtmp: remove %s: %w (a chmod pass did not help; the first error was %v)", path, err, first)
	}
	return nil
}

// entryName builds the on-disk name Reap parses back.
//
// purpose first so `ls` sorts by what a directory IS, which is what a human
// reading a swollen root wants grouped; pid second so ownership is one field
// away, not a search. scripts/lib/testtmp.sh writes the identical shape.
func entryName(purpose string, pid int, n int64) string {
	return fmt.Sprintf("%s.%d.%d", purpose, pid, n)
}

// ownerPID recovers the pid encoded in an entry name, or ok=false when the name
// is not one of ours.
func ownerPID(name string) (int, bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 3 {
		return 0, false
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// pidAlive reports whether pid names a live process.
//
// Signal 0 is the portable existence probe: it delivers nothing and reports
// whether it COULD have. EPERM is a live process owned by another user, and is
// therefore an ALIVE answer, not an error — reading it as "gone" is how a sweep
// deletes a directory belonging to something it merely cannot signal.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// Reap removes entries in root that no live process owns — see the package doc
// for the rule and why it is ownership rather than age. Errors are swallowed: a
// sweep that cannot delete something has lost nothing a caller can act on, and
// it must never be the reason a test fails.
//
// Exported so its behaviour can be tested directly against a fixture root,
// which is the only way to observe the direction that matters — that a LIVE
// owner's entry survives.
func Reap(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-StaleAfter)
	for _, e := range entries {
		if pid, ok := ownerPID(e.Name()); ok {
			if !pidAlive(pid) {
				_ = Remove(filepath.Join(root, e.Name()))
			}
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = Remove(filepath.Join(root, e.Name()))
	}
}

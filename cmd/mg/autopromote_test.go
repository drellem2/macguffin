package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests never call `mg schedule` unless they are testing `mg schedule`.
// That is the acceptance condition of mg-ad63 stated as a constraint on the
// test file: readiness must not depend on the sweep, so a test that proves it
// must not run the sweep.

// snzSnooze snoozes an item and fails the test if it does not take.
func snzSnooze(t *testing.T, bin string, env []string, id, dur string) {
	t.Helper()
	if out, code := snzRun(t, bin, env, "snooze", id, "--for", dur); code != 0 {
		t.Fatalf("mg snooze %s: exit %d\n%s", id, code, out)
	}
}

// elapse rewrites an item's gate to a time well in the past, which puts the
// store in exactly the state it would be in if the wake time had arrived while
// nothing was running.
func elapse(t *testing.T, root, id string) {
	t.Helper()
	rewriteSnooze(t, filepath.Join(root, "work", "pending", id+".md"),
		time.Now().UTC().Add(-72*time.Hour).Format(time.RFC3339))
}

// dirOf reads the item's status off the FILESYSTEM rather than by running
// `mg show`. That is deliberate: every mg invocation now promotes, so asking mg
// where an item is would be a promotion, and the negative control would pass
// for the wrong reason.
func dirOf(t *testing.T, root, id string) string {
	t.Helper()
	for _, s := range []string{"available", "pending", "claimed", "done", "shelved"} {
		matches, _ := filepath.Glob(filepath.Join(root, "work", s, id+".md*"))
		if len(matches) > 0 {
			return s
		}
	}
	return ""
}

// TestCLI_ElapsedSnoozeOpensWithoutTheSweep is the acceptance case for mg-ad63,
// with its negative control.
//
// The sweep on 2026-08-04 reported "the previous sweep ran 4d 9h ago" — four
// days in which every gate that opened stayed shut, because the one cron that
// promoted them was gone. This asserts that a plain `mg list` promotes an
// elapsed snooze, and that it does NOT promote one that has not elapsed.
//
// `mg schedule` is never run. There is no work/.last-sweep in this store at
// all, which is the strongest available form of "mayor's sweep schedule removed
// entirely".
func TestCLI_ElapsedSnoozeOpensWithoutTheSweep(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Come back to this")
	snzSnooze(t, bin, env, id, "3d")

	// NEGATIVE CONTROL. The gate has not elapsed. Several read-only commands
	// run against the store; none of them may promote it. A promoter that
	// cannot tell the difference is worse than no promoter, because it opens
	// gates people set on purpose.
	for _, args := range [][]string{{"list"}, {"show", id}, {"list", "--json"}} {
		out, code := snzRun(t, bin, env, args...)
		if code != 0 {
			t.Fatalf("mg %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		if strings.Contains(out, "Snooze elapsed") {
			t.Errorf("mg %s promoted an item whose gate is still closed:\n%s", strings.Join(args, " "), out)
		}
		if got := dirOf(t, root, id); got != "pending" {
			t.Fatalf("after mg %s the item is %q, want pending — the gate has not elapsed",
				strings.Join(args, " "), got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "work", ".last-sweep")); !os.IsNotExist(err) {
		t.Fatalf("no command in this test may stamp the sweep; Stat says: %v", err)
	}

	// THE ACCEPTANCE CASE. The wake time passes with nothing running, and the
	// very next mg command — a read, not a sweep — opens the gate.
	elapse(t, root, id)

	out, code := snzRun(t, bin, env, "list")
	if code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, id); got != "available" {
		t.Fatalf("`mg list` did not promote an elapsed snooze — item is %q, want available\n%s", got, out)
	}
	if !strings.Contains(out, "Snooze elapsed") || !strings.Contains(out, id) {
		t.Errorf("a promotion is a state change and must be announced, got:\n%s", out)
	}

	// The spent gate is cleared, exactly as the sweep would have cleared it.
	showOut, _ := snzRun(t, bin, env, "show", id)
	if strings.Contains(showOut, "Snooze:") {
		t.Errorf("a spent gate must be cleared on promotion:\n%s", showOut)
	}

	// And still nothing ever ran the sweep.
	if _, err := os.Stat(filepath.Join(root, "work", ".last-sweep")); !os.IsNotExist(err) {
		t.Errorf("the acceptance case must hold with no sweep having run at all; Stat says: %v", err)
	}
}

// The clock is one gate of two. An elapsed wake time on an item whose parent is
// still open must not release it — the promoter ANDs both gates through
// gateOpen, the same predicate the sweep uses, rather than growing a second
// opinion about readiness.
func TestCLI_AutoPromoteStillHonoursTheDependencyGate(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	parent := snzNewItem(t, bin, env, "Parent")
	child := snzNewItem(t, bin, env, "Child")

	if out, code := snzRun(t, bin, env, "edit", child, "--add-depends", parent); code != 0 {
		t.Fatalf("mg edit --add-depends: exit %d\n%s", code, out)
	}
	snzSnooze(t, bin, env, child, "3d")
	elapse(t, root, child)

	if out, code := snzRun(t, bin, env, "list"); code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, child); got != "pending" {
		t.Fatalf("the clock opened but the parent is still open; item is %q, want pending", got)
	}

	// Complete the parent, and the child comes out on the next plain command.
	if out, code := snzRun(t, bin, env, "claim", parent); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	if out, code := snzRun(t, bin, env, "done", parent); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, child); got != "available" {
		t.Fatalf("both gates are open; item is %q, want available", got)
	}
}

// A promoter that opens gates cannot be allowed to fail the command it rode in
// on. `mg list` against a store it cannot write must still print its list and
// still exit 0 — with the failure said out loud on stderr, because a silent
// swallow is the defect this whole ticket is about.
func TestCLI_AutoPromoteFailureDoesNotFailTheCommand(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod cannot make a directory unwritable")
	}
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Wedged")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	pendingDir := filepath.Join(root, "work", "pending")
	if err := os.Chmod(pendingDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(pendingDir, 0o755) })

	out, code := snzRun(t, bin, env, "list")
	if code != 0 {
		t.Fatalf("a failed promotion must not fail `mg list`: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Wedged") {
		t.Errorf("`mg list` must still produce its own output, got:\n%s", out)
	}
	if !strings.Contains(out, "could not promote elapsed snoozes") {
		t.Errorf("a failed promotion must say so rather than swallow it, got:\n%s", out)
	}
	if !strings.Contains(out, "does not affect the command above") {
		t.Errorf("the warning must say it did not affect the command, got:\n%s", out)
	}
}

// --json consumers parse stdout. Promotion notices and warnings are state
// mg volunteered, not the data that was asked for, so they go to stderr and
// stdout stays a single parseable document.
func TestCLI_AutoPromoteKeepsJSONStdoutClean(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Machine readable")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	cmd := exec.Command(bin, "list", "--json")
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("mg list --json: %v\nstderr: %s", err, stderr.String())
	}
	// `mg list --json` is NDJSON: one object per line. Parsing the whole stream
	// as a single document only ever worked by accident, on the one-item store
	// this test builds, and it turned BOTH interesting failures into the same
	// opaque syntax error — a promoter notice leaking onto stdout, and the item
	// going missing from stdout entirely, are opposite defects and must not
	// report identically. Parse per line, and say which one happened.
	var items []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("a stdout line is not parseable JSON — the promoter leaked into it: %v\n%q", err, line)
		}
		items = append(items, obj)
	}

	// The item must be PRESENT. This is the assertion the test was missing: the
	// promoter renames pending/ -> available/ while the listing walks those same
	// directories, and a walk that reads the destination before the source sees
	// the item in neither. That emits zero lines, which is zero bytes, which a
	// `jq` consumer reads as "no results" rather than as an error.
	if len(items) != 1 {
		t.Fatalf("stdout has %d items, want exactly 1 — a promotion mid-listing must not make the item vanish:\n%q",
			len(items), stdout)
	}
	if got := items[0]["id"]; got != id {
		t.Errorf("stdout lists %v, want %s", got, id)
	}
	if got := items[0]["status"]; got != "available" {
		t.Errorf("stdout reports status %v, want available", got)
	}
	if !strings.Contains(stderr.String(), "Snooze elapsed") {
		t.Errorf("the promotion must still be announced, on stderr:\n%s", stderr.String())
	}
	if got := dirOf(t, root, id); got != "available" {
		t.Errorf("item is %q, want available", got)
	}
}

// TestCLI_AutoPromoteFinishesBeforeTheCommandReads is the test above with the
// interleaving FORCED, rather than left to whether CI happened to be loaded.
//
// The one above is a race by construction: the promoter renames pending/x.md to
// available/x.md while `mg list` walks those same directories, and an mg listing
// takes an item's status from the directory it was found in. It passed on every
// developer machine and failed on CI, twice, on two different commits — because
// the losing interleaving needs the reader to get through both ReadDirs before
// the rename lands, and that is a coin CI flips differently.
//
// MG_AUTO_PROMOTE_DELAY holds the promoter back long enough that the reader
// WILL win that race if it is allowed to run concurrently at all. So:
//
//   - without the PersistentPreRun barrier, stdout says pending, every time.
//   - with it, the command does not start until the promoter is done, so stdout
//     says available, every time.
//
// The delay is well inside autoPromoteBudget, so nothing here is testing the
// timeout; that is the next test.
func TestCLI_AutoPromoteFinishesBeforeTheCommandReads(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Slow store")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	slow := append(append([]string{}, env...), "MG_AUTO_PROMOTE_DELAY=250ms")
	cmd := exec.Command(bin, "list", "--json")
	cmd.Env = slow
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("mg list --json: %v\nstderr: %s", err, stderr.String())
	}

	// IN-BAND CONTROL, and it is the whole reason this test is worth having.
	// A cleanliness or freshness assertion on a run where the promote never
	// fired passes vacuously — it proves only that nothing happened. Assert the
	// promotion occurred in the SAME run being judged, before judging it.
	if !strings.Contains(stderr.String(), "Snooze elapsed") {
		t.Fatalf("the promoter did not run, so this test proves nothing:\n%s", stderr.String())
	}
	if got := dirOf(t, root, id); got != "available" {
		t.Fatalf("the promoter did not finish; item is %q, want available", got)
	}

	var items []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("a stdout line is not parseable JSON: %v\n%q", err, line)
		}
		items = append(items, obj)
	}
	if len(items) != 1 {
		t.Fatalf("stdout has %d items, want exactly 1:\n%q", len(items), stdout)
	}
	if got := items[0]["status"]; got != "available" {
		t.Errorf("stdout reports status %v, want available — the command read the store "+
			"while its own promoter was still rewriting it, and stderr above says so", got)
	}
}

// The barrier waits, but not forever. A store that has stopped responding must
// not hold the user's command hostage past autoPromoteBudget: mg gives up, says
// it gave up, runs the command anyway, and exits 0.
//
// The delay here is far longer than the budget, so the promoter cannot possibly
// land — which makes "the item is still pending" a real in-band control that
// abandonment happened, rather than an assertion about timing luck.
func TestCLI_AutoPromoteBarrierAbandonsAWedgedStore(t *testing.T) {
	if testing.Short() {
		t.Skip("spends autoPromoteBudget waiting on purpose")
	}
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Wedged store")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	wedged := append(append([]string{}, env...), "MG_AUTO_PROMOTE_DELAY=10s")
	start := time.Now()
	out, code := snzRun(t, bin, wedged, "list")
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("an unresponsive promoter must not fail `mg list`: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Wedged store") {
		t.Errorf("`mg list` must still produce its own output, got:\n%s", out)
	}
	if !strings.Contains(out, "gave up promoting elapsed snoozes") {
		t.Errorf("abandonment must be said out loud, got:\n%s", out)
	}
	if !strings.Contains(out, "does not affect the command above") {
		t.Errorf("the warning must say it did not affect the command, got:\n%s", out)
	}
	if got := dirOf(t, root, id); got != "pending" {
		t.Errorf("item is %q, want pending — the promoter was supposed to be abandoned, "+
			"so this run did not exercise the timeout at all", got)
	}
	// The budget is a ceiling on the wait, and the point of having one is that it
	// is not the 10s the promoter would have taken.
	if elapsed > autoPromoteBudget+5*time.Second {
		t.Errorf("waited %s for a promoter it was supposed to abandon after %s", elapsed, autoPromoteBudget)
	}
}

// The opt-out. `mg list` mutating the store is a real behaviour change, and a
// reader who needs a provably read-only mg gets one.
func TestCLI_AutoPromoteCanBeDisabled(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Opt out")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	off := append(append([]string{}, env...), "MG_NO_AUTO_PROMOTE=1")
	if out, code := snzRun(t, bin, off, "list"); code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, id); got != "pending" {
		t.Fatalf("MG_NO_AUTO_PROMOTE did not disable the promoter; item is %q, want pending", got)
	}

	// Same store, same command, without the variable.
	if out, code := snzRun(t, bin, env, "list"); code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, id); got != "available" {
		t.Fatalf("item is %q, want available once the opt-out is gone", got)
	}
}

// TestCLI_ConcurrentPromotersPromoteExactlyOnce is the concurrency case this
// fleet actually runs: twelve polecats plus crew, every one of them promoting.
//
// rename(2) is mg's only concurrency primitive and promote() leans on it
// entirely — the rename is the first thing it does, so exactly one process can
// win and the losers see ENOENT and say nothing. The assertion is threefold:
// the item lands in available/ once, pending/ is empty, and exactly ONE of the
// racing processes claims the promotion in its output.
func TestCLI_ConcurrentPromotersPromoteExactlyOnce(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Contended")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	const racers = 12
	outs := make([]string, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(bin, "list")
			cmd.Env = env
			<-start
			out, _ := cmd.CombinedOutput()
			outs[i] = string(out)
		}(i)
	}
	close(start)
	wg.Wait()

	announced := 0
	for i, out := range outs {
		if strings.Contains(out, "Snooze elapsed") {
			announced++
		}
		if strings.Contains(out, "warning:") {
			t.Errorf("racer %d reported a failure; losing a promotion race is not an error:\n%s", i, out)
		}
	}
	if announced != 1 {
		t.Errorf("%d of %d racers announced the promotion, want exactly 1 — a promotion announced twice is a promotion that happened twice", announced, racers)
	}

	if got := dirOf(t, root, id); got != "available" {
		t.Fatalf("item is %q, want available", got)
	}
	// The duplicate-item failure this ordering exists to prevent: the same item
	// present in pending/ and available/ at once, because a losing promoter
	// re-created the file it had already lost.
	leftovers, _ := filepath.Glob(filepath.Join(root, "work", "pending", "*.md"))
	if len(leftovers) != 0 {
		t.Errorf("pending/ is not empty after the race — a loser re-created the file it lost: %v", leftovers)
	}
	available, _ := filepath.Glob(filepath.Join(root, "work", "available", id+".md*"))
	if len(available) != 1 {
		t.Errorf("available/ holds %d copies of %s, want 1: %v", len(available), id, available)
	}
}

// promote()'s one residue, and its self-heal. A process killed between the
// rename into available/ and the write that clears the spent gate leaves an
// inert elapsed wake time on an item that is no longer pending. It gates
// nothing, but it reads as though the item were still scheduled, so
// `mg schedule` wipes it and says which items it wiped.
func TestCLI_ScheduleClearsSpentGateResidue(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Residue")

	// Construct the residue directly: the crash window is microseconds wide and
	// cannot be hit reliably, so the state it produces is written by hand.
	path := filepath.Join(root, "work", "available", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	past := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
	withGate := strings.Replace(string(data), "---\n", "---\nsnooze: "+past+"\n", 1)
	if err := os.WriteFile(path, []byte(withGate), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if showOut, _ := snzRun(t, bin, env, "show", id); !strings.Contains(showOut, "Snooze:") {
		t.Fatalf("the residue was not constructed; mg does not see a snooze:\n%s", showOut)
	}

	out := driveTheSweep(t, bin, env)
	if !strings.Contains(out, "Cleared 1 spent snooze") || !strings.Contains(out, id) {
		t.Errorf("`mg schedule` must clear and name the spent gate, got:\n%s", out)
	}
	if showOut, _ := snzRun(t, bin, env, "show", id); strings.Contains(showOut, "Snooze:") {
		t.Errorf("the spent gate is still there:\n%s", showOut)
	}
	if got := dirOf(t, root, id); got != "available" {
		t.Errorf("clearing an inert gate must not move the item; it is %q", got)
	}
}

// A FUTURE wake time on a non-pending item is inert too, but somebody typed it.
// The tidy-up clears spent gates, not gates it disagrees with.
func TestCLI_ScheduleLeavesAnUnelapsedInertGateAlone(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Hand-edited future gate")

	path := filepath.Join(root, "work", "available", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	future := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	withGate := strings.Replace(string(data), "---\n", "---\nsnooze: "+future+"\n", 1)
	if err := os.WriteFile(path, []byte(withGate), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	out := driveTheSweep(t, bin, env)
	if strings.Contains(out, "Cleared") {
		t.Errorf("a future gate is not spent and must be left alone, got:\n%s", out)
	}
	if showOut, _ := snzRun(t, bin, env, "show", id); !strings.Contains(showOut, "Snooze:") {
		t.Errorf("the sweep deleted a gate somebody set on purpose:\n%s", showOut)
	}
}

// TestCLI_ScheduleReportsPromotionFailing exercises the detector that replaces
// the staleness warning as the snooze safety net.
//
// The old warning measured the sweep's ABSENCE, which stops meaning anything
// once promotion no longer needs the sweep. This measures the outcome: an item
// in pending/ with every gate open, immediately after a sweep whose job was to
// move it, can only mean promotion is failing — and it fires for causes the old
// warning could never have seen, of which an unwritable store is one.
func TestCLI_ScheduleReportsPromotionFailing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod cannot make a directory unwritable")
	}
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Cannot move")
	snzSnooze(t, bin, env, id, "3d")
	elapse(t, root, id)

	pendingDir := filepath.Join(root, "work", "pending")
	if err := os.Chmod(pendingDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(pendingDir, 0o755) })

	out, code := snzRun(t, bin, env, "schedule")
	if code == 0 {
		t.Errorf("a sweep that could not promote must exit non-zero\n%s", out)
	}
	if !strings.Contains(out, "have every gate open and were NOT promoted") {
		t.Fatalf("the detector did not fire; it is the replacement safety net:\n%s", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("the detector must name the item that did not move:\n%s", out)
	}
	if !strings.Contains(out, "promotion itself is failing") {
		t.Errorf("the detector must say what the state means:\n%s", out)
	}
}

// The sweep still warns when nothing is driving it, but about what it now
// actually detects: the held and stranded reports going unread. It must NOT
// still claim that snoozes only open when it runs, because that is no longer
// true and a false safety warning is worse than none.
func TestCLI_ScheduleStalenessWarningNamesTheReports(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	// A dependent on a parent that does not exist is stranded: no completion
	// can ever release it, which is exactly the population automatic promotion
	// can never help and only this report can see.
	child := snzNewItem(t, bin, env, "Waits on a ghost")
	if out, code := snzRun(t, bin, env, "edit", child, "--add-depends", "mg-ffff"); code != 0 {
		t.Fatalf("mg edit --add-depends: exit %d\n%s", code, out)
	}
	if got := dirOf(t, root, child); got != "pending" {
		t.Fatalf("child is %q, want pending", got)
	}

	out := driveTheSweep(t, bin, env)
	if !strings.Contains(out, "no previous sweep was ever recorded") {
		t.Fatalf("expected the staleness warning on a store with no prior sweep:\n%s", out)
	}
	if strings.Contains(out, "Snoozes open only when this sweep runs") {
		t.Errorf("the warning still makes the claim this feature falsified:\n%s", out)
	}
	if !strings.Contains(out, "open by themselves on any `mg` command") {
		t.Errorf("the warning must say promotion no longer needs the sweep:\n%s", out)
	}
	if !strings.Contains(out, "stranded item waits forever") {
		t.Errorf("the warning must name what the sweep is still load-bearing for:\n%s", out)
	}
}

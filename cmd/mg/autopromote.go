package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

// Opportunistic promotion: any mg action opens elapsed snoozes.
//
// Before this, a snoozed item returned to available/ only when `mg schedule`
// ran, and `mg schedule` ran only because mayor held a cron for it. When that
// cron was lost the gates stayed shut and nothing said so — the sweep of
// 2026-08-04 reported "the previous sweep ran 4d 9h ago", four days of open
// gates that nobody could see. An item's readiness should not depend on one
// particular agent being alive.
//
// # On every action, not on a ticker
//
// The obvious reading of "another go routine" is a ticker: start a goroutine,
// promote every N seconds. That shape does not fit this program. mg is a CLI,
// not a daemon; the median process lives a few milliseconds and exits, so a
// ticker with any sane N fires zero times in almost every process that starts
// one. So it runs once per invocation.
//
// # The goroutine is for ABANDONMENT, not for overlap
//
// This code originally ran the promotion BESIDE the user's command — started in
// PersistentPreRun, joined at the exit seam — on the reasoning that the useful
// half of "another goroutine" is concurrency. That reasoning was wrong for any
// command that READS the store, which is most of them.
//
// The promoter's whole job is renaming pending/x.md to available/x.md. An mg
// listing derives an item's status from the directory it was found in — there
// is no status field in the frontmatter, only a location. So a reader running
// concurrently with this process's own promoter reports whichever side of the
// rename it happened to observe, and one `mg list --json` could print
//
//	stdout: {"id":"mg-xxxx","status":"pending"}
//	stderr: Snooze elapsed: promoted mg-xxxx to available
//
// in the same breath, with the item sitting in available/ by the time the
// process exited. That is what kept main red through 13221e09 and 4d18247a:
// mg-9e3d fixed the walk ORDER so a mid-walk rename could no longer make the
// item vanish, which was a real and worse bug, but no walk order can make a
// concurrent reader see a rename that has not happened yet. Ordering bought
// "never absent"; it cannot buy "never stale".
//
// So the promoter is now joined BEFORE the command runs, in the same
// PersistentPreRun that starts it. What that costs is what AutoPromote costs in
// the common case — one ReadDir of pending/, the small directory, and zero
// writes — paid serially instead of in parallel, in a process whose median life
// is a few milliseconds. What it buys is that no mg command can contradict its
// own promoter.
//
// The goroutine stays, because overlap was never the only thing it bought: a
// blocking ReadDir on a wedged store has no timeout, and select-on-a-channel is
// how you get one. Abandonment is the property worth keeping (see
// autoPromoteBudget), and it survives serialisation intact.
//
// # What this does NOT close
//
// Two windows are left open on purpose, and neither is the one above.
//
//   - ANOTHER process. An agent's `mg claim` can still rename an item out from
//     under this one's listing. Nothing in a single process can close that; see
//     listAllOrder in internal/workitem for what read ordering does and does not
//     cover.
//   - THIS process, after abandonment. If the barrier gives up, the goroutine is
//     still alive and may land its rename while the command reads. What that can
//     no longer produce is the self-contradiction — the seam reports "gave up",
//     never "promoted mg-xxxx" beside a listing that says pending — but the
//     listing itself can still be a moment stale. That costs a wedged store, it
//     is bounded by autoPromoteBudget, and the alternative is waiting on a store
//     that is not answering.
//
// # It cannot fail the user's command
//
// A locked store, a full disk, a permissions change: none of it may turn
// `mg list` into a non-zero exit. Failures are collected and printed as a
// warning on STDERR after the command has produced its output — stderr so that
// `--json` consumers keep a clean stdout, and after so that the aside comes
// below the data the caller actually asked for.
//
// Abandonment is safe for the same reason a missed sweep is safe: the gate is
// level-triggered. Anything this run does not finish, the next invocation
// promotes from the same on-disk state.

// autoPromoteBudget is how long the exit seam will wait for the promoter after
// the user's command has finished. It is generous — the work is one ReadDir in
// the common case — because the only thing that consumes it is a store that has
// stopped responding, and in that case an mg that hangs briefly and then says
// so is better than one that abandons the store silently.
const autoPromoteBudget = 3 * time.Second

// envNoAutoPromote disables the promoter entirely. It exists for the reader who
// wants `mg list` to be provably read-only, and for tests that need to observe
// an un-promoted store. `mg schedule` still promotes when it is set: that
// command is an explicit instruction to sweep, not an incidental action.
const envNoAutoPromote = "MG_NO_AUTO_PROMOTE"

// envAutoPromoteDelay holds the promoter back by a duration before it does any
// work. It is a TEST SEAM and nothing else sets it.
//
// It exists because the defect this file's barrier fixes is an interleaving,
// and an interleaving that only reproduces under load is one that gets
// "verified" by a green local run and shipped red. mg-9e3d proved its fix with
// a temporary instrumented build, which is a measurement that ran once and then
// deleted itself; the next regression was found by CI on the merge commit. A
// delay the test can dial makes the losing interleaving DETERMINISTIC, so the
// property is checked on every run instead of on the day someone remembers to
// instrument it.
//
// The delay is deliberately applied inside the goroutine rather than in the
// barrier: it must simulate a SLOW STORE, which is a promoter that finishes
// late, not a caller that waits longer. That is the same shape as the real
// thing, so autoPromoteBudget still governs it.
const envAutoPromoteDelay = "MG_AUTO_PROMOTE_DELAY"

// autoPromoteSkip lists the commands that must not trigger a promotion.
//
//   - init: the store is being created; there is nothing to sweep and touching
//     the path first would be the one way to get it wrong.
//   - version, completion, help, schema: they answer questions about the
//     binary, not about a store, and must work with no store at all.
//   - schedule: it runs the full sweep itself, synchronously, and reports what
//     it did. A second concurrent promoter could only steal promotions out of
//     its report.
var autoPromoteSkip = map[string]bool{
	"init":       true,
	"version":    true,
	"completion": true,
	"help":       true,
	"schema":     true,
	"schedule":   true,
	"mg":         true, // the bare root command: no store operation is happening
}

// promoteResult carries what the goroutine found back to the exit seam.
type promoteResult struct {
	promoted []*workitem.Item
	err      error
}

// promoteCh is non-nil exactly when a promoter is running and has not yet been
// joined by awaitAutoPromote.
var promoteCh chan promoteResult

// promoteJoined is what awaitAutoPromote learned, held for finishAutoPromote to
// report once the command has written its own output. A nil pointer means no
// promoter ran, or none has been joined yet.
//
// Both of these are touched only from the main goroutine — PersistentPreRun and
// main() are the same one — so they need no lock. The promoter itself
// communicates solely through the channel.
var promoteJoined *promoteResult

// promoteTimedOut records that the barrier gave up on the promoter rather than
// joining it, so the exit seam reports the abandonment instead of a result it
// never received.
var promoteTimedOut bool

// startAutoPromote launches the promoter for cmd. It is called from the root
// PersistentPreRun, which cobra runs for every command after flags are bound —
// so --root is already resolved by the time this reads it.
//
// Starting is separate from waiting only so that awaitAutoPromote can put a
// timeout on the wait; the two are called back to back and cmd does not run
// until both have. Nothing between them may touch the store.
func startAutoPromote(cmd *cobra.Command) {
	// Reset first. In the binary this is a no-op — one process runs one command
	// — but an in-process test that executes rootCmd twice would otherwise see
	// the first run's join reported again by the second.
	promoteCh, promoteJoined, promoteTimedOut = nil, nil, false

	if os.Getenv(envNoAutoPromote) != "" {
		return
	}
	if autoPromoteSkip[cmd.Name()] {
		return
	}
	root, err := resolveRoot()
	if err != nil {
		// No workspace to resolve is not this goroutine's problem to report;
		// the command itself is about to fail with a much better message.
		return
	}

	delay, _ := time.ParseDuration(os.Getenv(envAutoPromoteDelay))

	ch := make(chan promoteResult, 1)
	promoteCh = ch
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		promoted, err := workitem.AutoPromote(root)
		ch <- promoteResult{promoted: promoted, err: err}
	}()
}

// awaitAutoPromote blocks until the promoter has finished with the store, or
// until the budget runs out. It is called from PersistentPreRun immediately
// after startAutoPromote, BEFORE the user's command runs — that ordering is the
// fix, and the long comment at the top of this file is why.
//
// It is idempotent and cheap to re-call: finishAutoPromote calls it too, so the
// exit seam still works on any path that reaches main() without a barrier
// having run.
func awaitAutoPromote() {
	if promoteCh == nil {
		return
	}
	select {
	case res := <-promoteCh:
		promoteJoined = &res
	case <-time.After(autoPromoteBudget):
		// Abandoned. Deliberately no second, non-blocking look at the channel
		// later: if the promoter lands after this point it has mutated the store
		// UNDER the command that is about to read it, and announcing "promoted
		// mg-xxxx to available" beside a listing that says pending is precisely
		// the self-contradiction this barrier exists to remove. The store is
		// level-triggered; the next invocation promotes from the same state and
		// gets to say so honestly.
		promoteTimedOut = true
	}
	promoteCh = nil
}

// finishAutoPromote reports on w what awaitAutoPromote already learned. It is
// called from the exit seam in main() once the command has finished writing its
// own output, so the aside lands below the data the caller asked for.
//
// It never returns an error and never influences the exit code. That is the
// whole contract: promotion is something mg does for the store, not something
// the caller asked for, and it may not turn the caller's successful command
// into a failed one.
func finishAutoPromote(w io.Writer) {
	awaitAutoPromote()

	if promoteTimedOut {
		// Abandoned — and SAID SO. The tempting call is to stay quiet, since the
		// gate is level-triggered and the next invocation retries from the same
		// state. But a promoter that gives up without a word is the exact shape
		// of the defect this whole feature removes: gates that do not open and
		// nothing anywhere saying so. If every invocation is timing out, nothing
		// is being promoted at all, and the operator has to be able to find that
		// out from the tool rather than from a ticket that sat for four days.
		fmt.Fprintf(w, "warning: gave up promoting elapsed snoozes after %s — the store is not responding.\n", autoPromoteBudget)
		fmt.Fprintf(w, "warning: this does not affect the command above. Run `mg schedule` for the full report.\n")
		return
	}
	if promoteJoined == nil {
		return
	}
	res := *promoteJoined

	for _, item := range res.promoted {
		fmt.Fprintf(w, "Snooze elapsed: promoted %s to available: %s\n", item.ID, item.Title)
	}
	if res.err != nil && !isMissingStore(res.err) {
		fmt.Fprintf(w, "warning: could not promote elapsed snoozes: %v\n", res.err)
		fmt.Fprintf(w, "warning: this does not affect the command above. Run `mg schedule` for the full report.\n")
	}
}

// isMissingStore reports whether err is just "there is no store here". A user
// running mg outside an initialised workspace gets a proper error from their
// actual command; a second warning from the promoter about the same absence is
// noise, and it would fire on every `mg --help`-adjacent invocation in a fresh
// checkout.
func isMissingStore(err error) bool {
	return err != nil && (os.IsNotExist(err) || strings.Contains(err.Error(), "no such file or directory"))
}

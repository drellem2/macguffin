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
// one. The useful half of "another goroutine" is CONCURRENCY, not periodicity:
// run the promotion beside the user's command rather than in front of it.
//
// So it runs once per invocation, started before the command and joined after
// it. The command's own work and the promotion overlap, and the user's output
// is already written by the time anything waits for anything.
//
// # It cannot fail the user's command
//
// A locked store, a full disk, a permissions change: none of it may turn
// `mg list` into a non-zero exit. Failures are collected and printed as a
// warning on STDERR after the command has produced its output — stderr so that
// `--json` consumers keep a clean stdout, and after so that a goroutine writing
// concurrently can never interleave with the command's own writes.
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

// promoteCh is non-nil exactly when a promoter is running for this process.
var promoteCh chan promoteResult

// startAutoPromote launches the promoter beside cmd. It is called from the root
// PersistentPreRun, which cobra runs for every command after flags are bound —
// so --root is already resolved by the time this reads it.
func startAutoPromote(cmd *cobra.Command) {
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

	ch := make(chan promoteResult, 1)
	promoteCh = ch
	go func() {
		promoted, err := workitem.AutoPromote(root)
		ch <- promoteResult{promoted: promoted, err: err}
	}()
}

// finishAutoPromote joins the promoter and reports on w. It is called from the
// exit seam in main() once the command has finished writing its own output, so
// nothing here can interleave with it.
//
// It never returns an error and never influences the exit code. That is the
// whole contract: promotion is something mg does for the store, not something
// the caller asked for, and it may not turn the caller's successful command
// into a failed one.
func finishAutoPromote(w io.Writer) {
	if promoteCh == nil {
		return
	}
	var res promoteResult
	select {
	case res = <-promoteCh:
	case <-time.After(autoPromoteBudget):
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

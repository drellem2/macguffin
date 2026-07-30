package workitem

import (
	"os"
	"strings"
)

// actor returns the identity that INVOKED the command being recorded. It is
// deliberately a property of the caller and of nothing else — in particular
// not of the item being acted on.
//
// It used to be actorFor(item), which preferred the item's ASSIGNEE, then its
// creator, and only then the OS user. That answers "who owns this" while
// wearing the label of "who did this" (mg-3122). Measured on the live log: an
// item assigned to `parked` recorded actor="parked"; an item assigned to a
// deliberate probe string recorded that string; an unassigned item recorded
// the unix user. All three read as real answers and two of them are false.
// That is strictly worse than an absent field, because a reader cannot see the
// substitution and so has no reason to distrust it.
//
// Resolution order, most specific to least:
//
//  1. MG_ACTOR — an explicit override, for a caller that knows its own
//     identity but is not a pogo agent (a wrapper script, a test asserting
//     attribution).
//
//  2. POGO_AGENT_NAME — pogod sets this on every agent it spawns, and it is
//     the only string that separates the dozen agents sharing this box's one
//     unix user. The mail audit log already attributes by it
//     (internal/mail/audit.go), so both logs now name a caller the same way.
//
//  3. The OS user. Weak on a single-user box — every agent is `daniel` — but
//     weak is not wrong. It is the honest answer to "who ran this" when
//     nothing more specific is on offer, and it degrades to vagueness rather
//     than to a different question's answer.
//
//     Measured 2026-07-30 (mg-35fc), the occupant of this step is not a human
//     at a terminal: it is POGOD. The daemon spawns agents with
//     POGO_AGENT_NAME set but runs itself with neither env var, so its own
//     claim-at-spawn and done-on-merge calls resolve here — every `daniel`
//     line in the live log was a work.claim or work.done written by pogod's
//     pid. `mg event list --help` tells the reader to read that value as
//     "pogod, or unknown". The durable fix is pogo-side: give pogod an
//     identity (MG_ACTOR=pogod on its own mg invocations) and step 3 stops
//     being reachable on the dispatch path.
//
//  4. "unknown". An empty answer is recoverable; a confident wrong one is not.
//
// The item is not consulted at ANY step, and the parameter is gone rather than
// ignored, so the assignee cannot quietly become the answer again.
func actor() string {
	if a := strings.TrimSpace(os.Getenv("MG_ACTOR")); a != "" {
		return a
	}
	if a := strings.TrimSpace(os.Getenv("POGO_AGENT_NAME")); a != "" {
		return a
	}
	if u := strings.TrimSpace(currentUser()); u != "" {
		return u
	}
	return "unknown"
}

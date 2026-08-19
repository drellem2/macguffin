package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
)

// UnclaimResult describes a released claim.
type UnclaimResult struct {
	ID  string
	PID int // PID recorded on the claim (0 if absent or unparseable)
	// Assignee is what the item carries as it lands in available/ — the one
	// field that says who the item is waiting on. Empty means nothing on the
	// item names anyone, which is the state RemainderOwed reports about.
	Assignee string
	// RemainderOwed reports that the released item DECLARES a remainder which
	// nothing tracks: the same condition `mg done` refuses to complete on. It
	// is a fact about the item, not about the release — see Unclaim.
	RemainderOwed bool

	// Status is where the item ACTUALLY landed — "available" or "pending" —
	// rather than where the command's name says it goes. A release is not
	// unconditionally a return to the dispatchable pool (see Unclaim), and a
	// caller that prints "returning to available/" from the verb alone would
	// state the opposite of what happened.
	Status string

	// Held is every gate that was still closed when the claim came off, and is
	// therefore the reason Status is "pending" rather than "available". Empty
	// when the item landed available/. Deps names the unsatisfied dependencies
	// with the parent's live status; Snoozed/BadSnooze cover the clock.
	Held Hold
}

// UnclaimOption configures an optional behaviour of Unclaim.
type UnclaimOption func(*unclaimOpts)

type unclaimOpts struct {
	assignee    string
	setAssignee bool
}

// WithUnclaimAssignee records who the item is waiting on, written onto the item
// BEFORE the claim is released.
//
// The ordering is the whole feature. `mg edit <id> --assignee=human` followed by
// `mg unclaim <id>` leaves the item gated but claimed for the gap, which is
// harmless; the other order — the one an agent reaches for, because it releases
// first and annotates second — leaves the item in available/ with no assignee
// for the gap, which is a live dispatchable ticket. That window is not
// hypothetical: mg-24d2 was released at 18:24:18Z on 2026-08-07 and did not get
// its assignee until 18:27:15Z, and a priority-wake named it as "ready and
// unclaimed — claim or dispatch now" inside those 2m57s.
//
// A single call cannot be run in the wrong order.
func WithUnclaimAssignee(who string) UnclaimOption {
	return func(o *unclaimOpts) {
		o.assignee = strings.TrimSpace(who)
		o.setAssignee = true
	}
}

// Unclaim atomically releases a claim, returning the work item to the directory
// its GATES say it belongs in — available/ when every gate is open, pending/
// when any is closed.
// The PID recorded on the claim is reported but not consulted — the recorded
// PID is unreliable because it may be the short-lived `mg claim` subprocess
// rather than the owning agent. Releasing a claim is therefore an explicit,
// targeted operation: the caller must know the work item ID.
//
// A CLAIM IS NOT A GATE, AND THIS IS WHERE THAT COSTS SOMETHING (mg-ed7b).
//
// On 2026-08-07 a sweeper collected seven claims held by polecats that had died
// with the daemon, and released the five it judged safe. It checked each one
// individually, and it checked the right thing for the work it knew about: a
// pushed branch and a merged commit. All five were gh-issue TRIAGES whose entire
// deliverable is a packet appended to the ticket body — no branch, no commit, by
// construction. Under that test a finished triage awaiting a human ruling and an
// abandoned claim are the same thing. Five completed items went back to
// available/, and the next dispatch onto one of them would have re-run the
// triage over the body carrying its only copy.
//
// The sweeper did the right procedure with care and the instrument was blind. mg
// was not: every one of the five carried `declares-remainder`, four of the five
// named no successor, and mg had that in hand at the exact moment it performed
// each release. It said nothing.
//
// So Unclaim now REPORTS what it is releasing, on the same predicate `mg done`
// refuses to complete on (requireRemainderDischarged): the item declares that
// its output is a recommendation, and nothing live tracks it. That predicate is
// deliberately not a stage, a type, a tag vocabulary or a body grep — it is the
// declaration the item makes about itself, so it sees work whose only artifact
// is prose exactly as well as it sees a build ticket.
//
// It REPORTS and does not refuse. A sweep of genuinely stranded claims must
// stay a single command that works; a guard here would fire on every abandoned
// triage claim too — the case the sweep exists for — and mg self-installs on
// merge, so a guard that blocks the routine sweep is removed by whoever it
// inconveniences (remainder.go records that failure). The caller keeps the
// decision and stops making it blind.
//
// # WHERE THE ITEM LANDS IS ASKED, NOT ASSUMED (mg-e7ff)
//
// The destination used to be the literal string "available". That is right for
// almost every release and wrong for the one that matters: an item whose gates
// closed WHILE it was claimed. `mg edit --add-depends` deliberately does not
// demote a claimed item — there is a worker on it — so the edge is recorded and
// the item stays in claimed/. Releasing the claim was then the moment the
// dependency stopped being enforced, because nothing on this path read it.
//
// Measured on 2026-08-19: two items carried the IDENTICAL unmet dependency and
// sat in different directories. The one that acquired the edge while available
// was demoted to pending/ and held. The one that acquired it while claimed came
// back to available/ when its polecat was stopped, and stall-watch and
// priority-wake then advertised it as "high-priority, unclaimed" — advice to
// dispatch an item whose dependency was deliberately unmet. Nothing errored,
// and `mg schedule` could not report it, because its held report reads pending/
// and the item was not in pending/ to be read.
//
// gateOpen is reused rather than re-derived, for the reason its own doc gives:
// every gate ANDs in one place, and adding a gate means editing that function
// rather than finding the call sites. This was a call site that had never been
// added. It answers the snooze gate too — a snoozed item that was claimed and
// released no longer lands back in the dispatchable pool with a future wake
// time on it.
//
// The failure direction is CLOSED here, unlike the dispatch gates in pogo that
// consume this state. Those fail open because a guard that halts the fleet over
// one bad path gets disarmed; this one is not a guard, it is a placement, and
// the store cannot be read at all if doneIDSet fails — so refusing the release
// leaves the item claimed, which is recoverable by re-running, rather than
// released into a pool it may not belong in.
func Unclaim(root, id string, opts ...UnclaimOption) (*UnclaimResult, error) {
	var o unclaimOpts
	for _, apply := range opts {
		apply(&o)
	}

	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "claimed" {
		return nil, explainUnclaimFailure(root, id)
	}

	src := m.Path
	pid := parseClaimPID(filepath.Base(src))

	item, err := readFile(src)
	if err != nil {
		return nil, err
	}

	// Written while the item is still in claimed/, so there is no instant at
	// which it sits in available/ without the assignee that explains it. A
	// failure here refuses the release outright: an item that stayed claimed is
	// recoverable by re-running, whereas one released without the gate it was
	// asked for is a dispatchable ticket nobody knows is unguarded.
	// A gate that does not gate is refused before the release, for the same
	// reason the write below happens before it: an item released into
	// available/ carrying a misspelled hold is a dispatchable ticket whose
	// assignee field reads, to a human, as held. See assigneegate.go.
	if o.setAssignee {
		if err := ValidateAssignee(o.assignee); err != nil {
			return nil, err
		}
	}

	if o.setAssignee && item.Assignee != o.assignee {
		item.Assignee = o.assignee
		if err := os.WriteFile(src, []byte(Render(item)), 0o644); err != nil {
			return nil, ioErr(fmt.Sprintf(
				"%s: claim NOT released — the assignee could not be recorded first: %s", id, fsErrText(err)))
		}
	}

	// Asked before the move, of the item as it will land. requireRemainderDischarged
	// is reused rather than re-derived so the thing `mg done` refuses to complete
	// and the thing this reports releasing can never drift apart. A read error
	// from resolving the successor counts as owed: this is a display flag, and
	// over-reporting costs a sentence while under-reporting is the silence the
	// whole change exists to end.
	owed := requireRemainderDischarged(root, item) != nil

	// Asked of the item as it will land, with the assignee already written, and
	// BEFORE the rename — so a gate that is closed decides the destination
	// rather than being discovered after the item is already dispatchable.
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, fmt.Errorf("%s: claim NOT released — its gates could not be evaluated: %w", id, err)
	}
	landing := "available"
	var held Hold
	if !gateOpen(item, doneIDs, snoozeNow()) {
		landing = "pending"
		held, err = holdFor(root, item)
		if err != nil {
			return nil, fmt.Errorf("%s: claim NOT released — its gates could not be described: %w", id, err)
		}
	}
	dst := filepath.Join(root, "work", landing, id+".md")

	if err := os.Rename(src, dst); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not release claim: %s", id, fsErrText(err)))
	}

	// The result sidecar must follow the .md, or it is orphaned in claimed/.
	if err := moveResultSidecar(filepath.Dir(src), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: claim released, but result sidecar could not follow: %s", id, fsErrText(err)))
	}

	kvs := map[string]string{
		"item_id":     id,
		"from_status": "claimed",
		// The directory the item actually landed in. It was hardcoded
		// "available" back when the destination was, and a log that said
		// available while the file went to pending would be worse than the bug
		// it records the fix for.
		"to_status": landing,
		// Every other transition records WHO (mg-3122); the release did not.
		// The five releases of 2026-08-07 were reported afterwards as
		// "attributed" by the agent that made them, and the log lines carry no
		// actor at all — an honest belief about a record nobody had re-read.
		// A transition that says what happened but not who did it is the half
		// of an audit trail that cannot answer the first question asked of it.
		"actor": actor(),
	}
	if pid > 0 {
		kvs["pid"] = strconv.Itoa(pid)
	}
	// The reason the item is not simply free. Emitted whenever the landed item
	// carries an assignee, whether this call set it or it was already there, so
	// a reader of the log sees the state the release produced rather than the
	// argument it was passed.
	if item.Assignee != "" {
		kvs["assignee"] = item.Assignee
	}
	// The negative is worth recording only when it is true, and it is the
	// searchable form of the failure: `remainder_owed=true` with no assignee is
	// precisely a completed-or-in-flight recommendation returned to the
	// dispatchable pool with nothing holding it.
	if owed {
		kvs["remainder_owed"] = "true"
	}
	// Searchable form of "this release did NOT return the item to the
	// dispatchable pool, and here is what held it". Emitted only when it
	// happened, like remainder_owed above.
	if landing == "pending" {
		kvs["held_by"] = held.Gates(snoozeNow())
	}
	event.Emit(root, "work.unclaim", kvs)

	return &UnclaimResult{
		ID:            id,
		PID:           pid,
		Assignee:      item.Assignee,
		RemainderOwed: owed,
		Status:        landing,
		Held:          held,
	}, nil
}

// parseClaimPID extracts the PID suffix from a claimed filename of the form
// "<id>.md.<pid>". Returns 0 if there is no PID suffix or it doesn't parse.
func parseClaimPID(name string) int {
	lastDot := strings.LastIndex(name, ".")
	if lastDot < 0 {
		return 0
	}
	pidStr := name[lastDot+1:]
	pid := 0
	for _, c := range pidStr {
		if c < '0' || c > '9' {
			return 0
		}
		pid = pid*10 + int(c-'0')
	}
	return pid
}

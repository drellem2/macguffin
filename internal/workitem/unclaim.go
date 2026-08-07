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

// Unclaim atomically releases a claim, moving the work item back to available/.
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
	dst := filepath.Join(root, "work", "available", id+".md")

	item, err := readFile(src)
	if err != nil {
		return nil, err
	}

	// Written while the item is still in claimed/, so there is no instant at
	// which it sits in available/ without the assignee that explains it. A
	// failure here refuses the release outright: an item that stayed claimed is
	// recoverable by re-running, whereas one released without the gate it was
	// asked for is a dispatchable ticket nobody knows is unguarded.
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
		"to_status":   "available",
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
	event.Emit(root, "work.unclaim", kvs)

	return &UnclaimResult{ID: id, PID: pid, Assignee: item.Assignee, RemainderOwed: owed}, nil
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

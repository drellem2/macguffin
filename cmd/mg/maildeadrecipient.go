package main

import (
	"fmt"
	"io"
	"os"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

// A maildir OUTLIVES the agent that read it. That is the gap this file closes.
//
// `mg mail send` learned to refuse an UNKNOWN recipient (mg-d639): a name
// nothing on disk has ever seen exits 3 with a did-you-mean, so a typo stops
// minting a dead drop that reports "Delivered". What that refusal cannot see is
// the other way a delivery goes unread — a recipient that is perfectly KNOWN,
// whose agent is gone. The box is real, the send is well-formed, exit is 0, and
// nothing on either side ever says the message will not be read.
//
// That is the shape drellem2/pogo#131 was reported from: a review polecat
// waiting on a builder that had already exited. pogo fixed the structural cause
// — the reaper no longer kills a builder mid-round, and the reviewer checks its
// counterparty before waiting. This is the remaining detect-after-the-fact half,
// and it is defence in depth, not the primary control.
//
// # What mg can actually see
//
// mg has no agent registry — mailregistry.go says so at length, and it is still
// true here. mg cannot answer "is that process alive?", and this file does not
// pretend to: nothing in the store records an agent's liveness, only a work
// item's LIFECYCLE. So the signal is the work item the mailbox is named for,
// which is available because polecat mailboxes are named for the item their
// agent runs — the same fact that makes a brand-new agent addressable before its
// first mail arrives.
//
// The honest limit, stated so nobody mistakes this for more than it is: an agent
// that died while its item was still `claimed` is INVISIBLE here. The item looks
// exactly like one an agent is working. Catching that needs process liveness,
// which mg has never inspected and which PID reuse and a shared store make
// unsound to bolt onto the mail hot path. pogo owns that half.
//
// # Warn, never refuse
//
// The delivery still happens and the exit code is still 0. Two reasons, both
// load-bearing:
//
//   - Mail to a finished agent's box is often CORRECT — an audit trail, a note
//     for whoever adopts the item next, a reply that arrives late.
//   - `mg mail` is on the hot path for every agent in the fleet. A new refusal
//     mode there can strand a coordinator mid-cycle, which is a worse failure
//     than the one being fixed.
//
// So this is a note the sender can act on, not a gate. It goes to STDERR, which
// keeps `--json` stdout parseable — the same rule warnShadowed follows in
// internal/workitem, and the reason `mg show --json` can be piped at all.

// pastStates are the lifecycle positions that put a work item BEHIND the sender
// rather than ahead of it: the item has been finished, filed away, or
// deliberately parked, so no agent was dispatched onto it and none is coming
// until somebody acts.
//
// Note the deliberate divergence from workitem.liveStates, which counts
// `shelved` as live. That set answers "can this still be worked?" — and shelved
// work can, after an unshelve. This set answers a different question: "is anyone
// reading this box right now?" A shelved item is parked, so the answer is no,
// and a sender who expects a reply should hear about it.
//
// The states left out are left out for a reason that is NOT "somebody is
// reading":
//
//   - `claimed` is the one status that positively says an agent holds the item.
//   - `available` and `pending` are the states BEFORE dispatch. A work-item
//     mailbox is addressable precisely so a polecat can be mailed before it
//     exists, and warning here would fire on every legitimate first contact —
//     on the hot path, on the happy path, for every agent the fleet spawns.
//
// Warn when the item is behind you; stay quiet when it is ahead of you.
var pastStates = map[string]bool{
	"done":     true,
	"archived": true,
	"shelved":  true,
}

// recipientItem is everything ONE walk of the store says about a recipient
// name. It exists so the send path walks once instead of twice.
//
// `mg mail send` asks the store two questions about its recipient — "is this
// name addressable at all?" (mg-d639's refusal) and "is anyone reading it?"
// (this ticket) — and both are answered by the same records. Asking them
// separately means two independent walks of a 2,600-item store on a command
// every agent in the fleet runs. Asking them together costs what the refusal
// already cost.
type recipientItem struct {
	// found reports whether a work item is called that, live or terminal. It
	// is what knownRecipient's work-item half consults.
	found bool
	// past is the lifecycle state when nobody is reading the box — "done",
	// "archived" or "shelved" — and "" otherwise.
	past string
}

// lookupRecipientItem walks the store for the work item a recipient is named
// for.
//
// Both spellings are tried, exactly as workItemNamed tries them: the recipient
// has been through canonicalAgent so it is the bare id ("cf1e"), and the store
// spells it with the workspace prefix ("mg-cf1e"). They are ALTERNATIVES, not a
// union — the first spelling that resolves to anything wins, and the second is
// not walked. That mirrors workItemNamed, which returns on its first hit, and it
// halves the cost for the polecat boxes that are the common case here.
//
// A LIVE match wins over a terminal one. An id can name more than one record —
// a done item shadowed by a live twin is the case workitem.ResolveUnique exists
// to arbitrate — and when any record for this name is live, an agent may well be
// running it. Under-warning is the correct direction to err on a hot path: a
// warning that fires when someone IS reading teaches the fleet to ignore it,
// which costs more than the silence it replaced.
func lookupRecipientItem(root, name string) recipientItem {
	if name == "" {
		return recipientItem{}
	}
	for _, id := range []string{workspace.Prefix(root) + name, name} {
		matches, err := workitem.Resolve(root, id)
		if err != nil || len(matches) == 0 {
			continue
		}
		st := recipientItem{found: true}
		for _, m := range matches {
			if m.Live() && !pastStates[m.Status] {
				// A live record — say nothing, whatever else shares the name.
				return recipientItem{found: true}
			}
			if pastStates[m.Status] && st.past == "" {
				st.past = m.Status
			}
		}
		return st
	}
	return recipientItem{}
}

// recipientPastState is the single-answer spelling, for callers that only want
// the warning half.
func recipientPastState(root, name string) string {
	return lookupRecipientItem(root, name).past
}

// pastRecipientNotice renders the warning for a delivery to a box whose work
// item is behind the fleet, or "" when there is nothing to say.
//
// It names the STATE rather than asserting the agent is dead, because the state
// is what mg observed and the agent's fate is an inference from it. It also
// names the move that resolves the doubt — reading the item — so a sender who
// meant this delivery can confirm it in one command instead of guessing whether
// the warning applies to them.
func pastRecipientNotice(root, recipient, state string) string {
	if state == "" {
		return ""
	}
	reason := map[string]string{
		"done":     "has been completed",
		"archived": "has been archived",
		"shelved":  "is shelved",
	}[state]
	id := workspace.Prefix(root) + recipient
	return fmt.Sprintf(
		"warning: delivered, but work item %s %s, so the agent that read %s's mailbox is probably gone.\n"+
			"  A mailbox outlives its agent, so this send cannot tell you nobody is there — only that nobody is expected to be.\n"+
			"  Run 'mg show %s' to see where the work went. If this message was meant for whoever picks the item up next, or for the record, nothing is wrong.\n",
		id, reason, recipient, id)
}

// warnPastRecipient writes the notice, if there is one. Callers pass os.Stderr;
// tests pass a buffer.
//
// Call it AFTER the delivery succeeds, never before. A warning printed ahead of
// a send that then fails describes a message that does not exist — the remedy
// telling the same kind of lie the defect told, which is the failure mode this
// whole file is about. registerViaCreate is ordered the same way and for the
// same reason.
func warnPastRecipient(w io.Writer, root, recipient, state string) {
	if notice := pastRecipientNotice(root, recipient, state); notice != "" {
		fmt.Fprint(w, notice)
	}
}

// stderrWarnPastRecipient is the production spelling, split out so the send and
// reply paths read as one call and tests can drive the writer directly.
func stderrWarnPastRecipient(root, recipient, state string) {
	warnPastRecipient(os.Stderr, root, recipient, state)
}

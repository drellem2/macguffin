package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

// This file gives `mg mail` the one thing it never had: a BAD ADDRESS.
//
// A mailbox used to be created by the act of delivering to it, which meant
// every send succeeded — a typo'd recipient minted a dead drop and reported
// "Delivered". The note it printed, "(new mailbox created)", is equally true of
// the first legitimate mail to a new agent, so it cannot separate the two: the
// mayor saw it five times in one night, reasoned "normal for a first mail", and
// moved on while four mails addressed to "9ecf" (the reviewer is "v9ecf") piled
// up in a box nobody opens. The review loop stalled forty minutes with both ends
// healthy, because both ends WERE healthy.
//
// Refusing a bad address needs a notion of which addresses are good, and mg has
// no agent registry to consult. It does, however, have two facts on disk that
// amount to one:
//
//   - A MAILBOX that already exists. Someone has received mail there, so the
//     name is real whatever it names.
//   - A WORK ITEM whose id matches the name. Polecat mailboxes are named for
//     the work item the agent is running, so the box for a brand-new agent is
//     addressable before its first mail arrives — which is exactly the case
//     that would otherwise force --create on every legitimate first delivery.
//
// Anything else is refused, and `--create` (or `mg mail register`) is the
// explicit spelling for "yes, this really is a new recipient". The point is not
// that --create is hard to type; it is that a caller who MEANT a new recipient
// says so, and a caller who typo'd does not — which is the distinction
// "(new mailbox created)" could never draw.

// workItemNamed reports whether name matches a work item anywhere in the store,
// live or terminal. The name has already been through canonicalAgent, so it is
// the bare id ("d639"); the store spells it with the workspace prefix
// ("mg-d639"). Both spellings are tried, because a workspace may configure a
// prefix canonicalAgent does not strip, and a caller may address the bare id
// either way.
//
// Terminal items count. A polecat keeps reading its mailbox after `mg done`
// moves the item to done/, and mail addressed to a finished agent is late, not
// misdelivered.
func workItemNamed(root, name string) bool {
	if name == "" {
		return false
	}
	for _, id := range []string{workspace.Prefix(root) + name, name} {
		if matches, err := workitem.Resolve(root, id); err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

// knownRecipient reports whether a recipient is addressable without --create:
// it already has a mailbox, or a work item names it. See the file comment.
func knownRecipient(mailRootDir, root, recipient string) bool {
	return mail.MailboxExists(mailRootDir, recipient) || workItemNamed(root, recipient)
}

// recipientCandidates is the set of names a mistyped recipient might have meant:
// every existing mailbox, plus every live work-item id (canonicalized to the
// bare form a mailbox is named for). It is built ONLY on a failure path — a
// successful send never walks the store — so the cost buys a correction rather
// than taxing the happy path.
func recipientCandidates(mailRootDir, root string) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}

	if boxes, err := mail.ListMailboxes(mailRootDir); err == nil {
		for _, b := range boxes {
			add(b.Name)
		}
	}
	for _, id := range workitem.LiveIDs(root) {
		add(canonicalAgent(id))
	}
	return names
}

// suggestNames returns the names a mistyped mailbox might have meant, nearest
// first, or nil when nothing in the store resembles it. Silence is the honest
// answer to a name with no near neighbour: a suggestion pulled from too far away
// sends the caller chasing a mailbox they never meant.
func suggestNames(mailRootDir, root, name string) []string {
	return mail.Suggest(recipientCandidates(mailRootDir, root), name, 3)
}

// suggestionSuffix renders the suggestions as a clause to append mid-sentence,
// or "" when there are none.
func suggestionSuffix(mailRootDir, root, name string) string {
	hits := suggestNames(mailRootDir, root, name)
	if len(hits) == 0 {
		return ""
	}
	return " — did you mean " + strings.Join(hits, " or ") + "?"
}

// suggestionLine renders the suggestions as their own indented line, or "" when
// there are none. `mg mail list` uses this rather than the mid-sentence clause:
// the sentence it would attach to already carries an em dash, and a second one
// in the same line turns the correction into punctuation noise exactly where a
// misdelivered agent needs to read it.
func suggestionLine(mailRootDir, root, name string) string {
	hits := suggestNames(mailRootDir, root, name)
	if len(hits) == 0 {
		return ""
	}
	return "  Did you mean " + strings.Join(hits, " or ") + "?  (run 'mg mail list' to see every mailbox)\n"
}

// unknownRecipientError is the refusal a bad address now earns. It is
// CatNotFound (exit 3) rather than a usage error: the caller's spelling is
// well-formed, the entity it names simply does not exist — the same category
// `mg show` uses for an unknown work-item id.
func unknownRecipientError(mailRootDir, root, recipient string) error {
	return mgerr.NotFound("no_such_mailbox",
		fmt.Sprintf("no mailbox named %q, and no work item is called that either: mg has never seen this recipient%s",
			recipient, suggestionSuffix(mailRootDir, root, recipient)),
		"run 'mg mail list' to see every mailbox. If this really is the first mail to a new recipient, "+
			"pass --create (or run 'mg mail register "+recipient+"' first) — that registers the mailbox and delivers.")
}

// A mailbox's STANDING is the after-the-fact answer to "what makes this name
// addressable?" — the same question `mg mail send` asks before it delivers,
// asked of a box that already exists.
//
// It has to be askable afterwards, not only at send time, because the send-time
// refusal fires once per name and then never again: existence is what it
// consults, so a name that got past it once (with --create, the documented and
// therefore reachable escape) is a good address forever after, indistinguishable
// from one somebody meant. The live example is `daniel` — in daily use, never
// registered, and until now identical on disk to a box that was set up.
//
// Three values, because there are exactly three ways a name can stand:
//
//   - registered: a registration record is present. Somebody performed the
//     deliberate act, and the record says who and when.
//   - work-item: no record, but a work item is called that. Nothing was
//     asserted, yet the name is not arbitrary — polecat boxes are named for the
//     item their agent runs, and the item is still on disk to check. This is
//     the bulk of the store and it is NOT a problem to report.
//   - unregistered: neither. The box exists only because mail was delivered to
//     it. Every phantom box ever minted is here, and so is `daniel`.
//
// The values apply to a box that does not exist yet, too, and stay meaningful:
// "work-item" on a missing box means mail sent there will be accepted, and
// "unregistered" on a missing box is precisely the address send refuses.
const (
	standingRegistered   = "registered"
	standingWorkItem     = "work-item"
	standingUnregistered = "unregistered"
)

// mailboxStanding answers for ONE name, resolving the work item directly. Use
// mailboxStandingFunc when asking about many.
func mailboxStanding(mailRootDir, root, name string) string {
	if mail.IsRegistered(mailRootDir, name) {
		return standingRegistered
	}
	if workItemNamed(root, name) {
		return standingWorkItem
	}
	return standingUnregistered
}

// mailboxStandingFunc returns a standing lookup backed by ONE walk of the work
// store, for callers asking about every mailbox at once. Resolve is a full walk
// per name — right for one name, ruinous for 1361 — so the enumeration pays for
// the store once and then answers from memory.
//
// The set is a snapshot: an item filed while the listing runs is not in it. That
// is the honest trade for a listing that returns, and it can only ever
// under-report a work item as an unregistered box, never invent standing a name
// does not have.
func mailboxStandingFunc(mailRootDir, root string) func(string) string {
	ids := workitem.AllIDs(root)
	prefix := workspace.Prefix(root)
	return func(name string) string {
		if mail.IsRegistered(mailRootDir, name) {
			return standingRegistered
		}
		// Both spellings, exactly as workItemNamed tries them: the store
		// writes "mg-d639" and a mailbox is named "d639".
		if ids[prefix+name] || ids[name] {
			return standingWorkItem
		}
		return standingUnregistered
	}
}

// callerOwnsMailbox reports whether the caller may treat agent's mailbox as its
// own even though the two names differ. It is the escape from a guard that was
// firing on people's own inboxes.
//
// The cross-box read guard compares $POGO_AGENT_NAME against the mailbox name,
// which assumed an agent's inbox is named for the agent. It is not: a mailbox is
// created by whoever first delivers to it, so an agent's inbox is whichever name
// its SENDERS used — routinely the work item it is running, not its agent name.
// Agent "pd639" reads work-item box "d639", and the guard refused, in wording
// that reads like a permissions error. A polecat meeting it concludes it is not
// allowed to read its own mail and leaves the mail unread: precisely the outcome
// the guard exists to prevent.
//
// Ownership is asserted only on THREE things together, never one:
//
//  1. The mailbox name appears inside the caller's own name ("d639" in
//     "pd639"), which is how the harness derives one from the other;
//  2. that name is a real work item in this store; and
//  3. the CALLER's own name is not itself a work item id.
//
// (1) alone would hand "639" to "pd639". (2) alone would open every work-item
// box to every agent. Together they say: this box is named for a work item, and
// the caller is named for that same work item. A mayor reading "d639" still
// trips the guard, which is correct; nothing about the mayor's name claims that
// item.
//
// (3) closes the one case where (1) and (2) can both hold and still be wrong.
// Work-item ids are fixed-width, so no two of them can contain one another — but
// a name that is itself an id can contain a SHORTER slice that happens to be
// another id ("0eac" inside "0eacb"). When the caller's own name resolves to a
// work item, that item names the caller's own box and a different id inside it
// is coincidence, not ownership. An agent name derived from an id ("pd639") is
// not itself an id, so the legitimate case is untouched.
func callerOwnsMailbox(root, caller, agent string) bool {
	c := canonicalAgent(caller)
	a := canonicalAgent(agent)
	if c == a {
		return true
	}
	if a == "" || !strings.Contains(strings.ToLower(c), strings.ToLower(a)) {
		return false
	}
	if workItemNamed(root, c) {
		return false
	}
	return workItemNamed(root, a)
}

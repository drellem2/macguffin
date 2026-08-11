package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// A MISSPELLED GATE IS AN OPEN GATE, AND IT LOOKS EXACTLY LIKE A CLOSED ONE.
//
// `assignee` is the dispatch gate. pogo's `config.IsDispatchGated` holds an item
// back when the field reads `human`, `parked`, or `blocked:<agent>` — the
// sentinel list is configurable, the `blocked:` shape is not. Every other value,
// including every typo of those three, is a value the dispatcher does not
// recognise and therefore does not gate on.
//
// mg accepted all of them. `--assignee=blocekd:pm-pogo`, `--assignee=blocked:`,
// `--assignee=Blocked:pm-pogo` and `--assignee=blocked-pm-pogo` each exited 0,
// printed `Updated <id>`, and stored a value that gates nothing. The caller who
// typed one of those believes the item is held; the dispatcher reads an ordinary
// name and offers it. There is no later signal, because a value nobody
// recognises is indistinguishable from an agent nobody has heard of.
//
// WHAT THIS CAN AND CANNOT CHECK. mg does not know the set of legitimate
// assignees — `mayor`, `pm-pogo`, `daniel`, an agent registered five minutes ago
// — so it cannot refuse "unrecognised". Any string really is a legal assignee.
// What it can recognise is an ATTEMPT AT THE GATE that missed: a value that is a
// near-miss of the gate vocabulary is not plausibly the name of an agent, it is
// a hold that will not hold. Only those are refused, so the check cannot stand
// between a caller and a name they meant.
//
// The residual is stated rather than papered over: a typo far enough from the
// three spellings (say `parkd` against `parked`) is indistinguishable from an
// agent name and IS allowed through. Widening the net to catch it would mean
// refusing values by edit distance from a five-letter word, which would
// eventually refuse a real agent — and a guard that refuses legitimate names is
// one that gets switched off. This errs the same way BlockedOnTags does: a false
// pass costs a typo nobody catches, a false refusal costs one command.
//
// There is no --force. The way past the guard is to spell the gate correctly,
// which is also the fix — the same shape the retitle guard uses, where naming
// --title is both the remedy and the way through.

// gateSentinels are the bare values pogo gates on. Kept here as the spellings
// this package compares AGAINST, not as an authority: pogo owns the list and it
// is configurable there. Adding a value here would not gate anything, and
// removing one would not un-gate it — this is a typo detector, and its whole
// job is to compare a caller's value against the spelling that works.
var gateSentinels = []string{"human", "parked"}

// gatePrefix is the one part of the vocabulary that is NOT configurable in
// pogo: the `blocked:<agent>` shape.
const gatePrefix = "blocked"

// ValidateAssignee refuses a value that is a near-miss of the dispatch-gate
// vocabulary, and passes everything else.
//
// The empty string is valid and clears the field. A value with no relationship
// to the gate vocabulary is valid: it is an agent name, and mg has no register
// of those to check it against.
func ValidateAssignee(v string) error {
	value := strings.TrimSpace(v)
	if value == "" {
		return nil
	}

	head, tail, hasColon := strings.Cut(value, ":")

	if hasColon {
		switch {
		case head == gatePrefix && strings.TrimSpace(tail) != "":
			return nil // the gate, spelled correctly
		case head == gatePrefix:
			// `blocked:` with nothing after it. The prefix is right and there
			// is no agent, so the value gates on a name that does not exist —
			// and reads, to anyone skimming the item, exactly like a hold.
			return errAssigneeGate(value,
				"names no agent after 'blocked:'",
				"Write the agent that owes the next move, e.g. --assignee=blocked:pm-pogo. "+
					"To hold the item without naming anyone, use --assignee=parked.")
		case nearMiss(head, gatePrefix):
			suggested := gatePrefix + ":" + strings.TrimSpace(tail)
			if strings.TrimSpace(tail) == "" {
				suggested = gatePrefix + ":<agent>"
			}
			return errAssigneeGate(value,
				fmt.Sprintf("looks like a dispatch gate but is spelled %q, not %q", head, gatePrefix),
				fmt.Sprintf("Did you mean --assignee=%s? Only the exact prefix 'blocked:' is gated; "+
					"anything else is stored as an ordinary assignee and the item stays dispatchable.", suggested))
		}
		// A colon-bearing value that is nowhere near `blocked` is not an
		// attempted gate. Not this guard's business.
		return nil
	}

	for _, s := range gateSentinels {
		if value == s {
			return nil
		}
		if strings.EqualFold(value, s) {
			// Case is the whole difference. pogo compares the stored string,
			// so `Parked` is a name and `parked` is a gate.
			return errAssigneeGate(value,
				fmt.Sprintf("differs from the gate value %q only in case", s),
				fmt.Sprintf("Use --assignee=%s exactly. The dispatcher compares the stored "+
					"string, so %q is read as an ordinary assignee and the item stays dispatchable.", s, value))
		}
	}

	// `blocked-pm-pogo`, `blocked_pm-pogo`, `blockedpmpogo`: the prefix is
	// there and the separator is not, so nothing gates. Also the bare word
	// `blocked` and its typos, which name no agent either way.
	if folded := strings.ToLower(value); strings.HasPrefix(folded, gatePrefix) || nearMiss(value, gatePrefix) {
		agent := strings.TrimLeft(value[min(len(value), len(gatePrefix)):], "-_ .")
		suggested := gatePrefix + ":<agent>"
		if strings.HasPrefix(folded, gatePrefix) && agent != "" {
			suggested = gatePrefix + ":" + agent
		}
		return errAssigneeGate(value,
			"looks like a dispatch gate but carries no 'blocked:' prefix",
			fmt.Sprintf("Did you mean --assignee=%s? The separator is a colon; "+
				"any other spelling is stored as an ordinary assignee and the item stays dispatchable.", suggested))
	}

	return nil
}

// errAssigneeGate is the refusal. Usage (exit 2), not conflict: the item is in
// no particular state, the argument is wrong, and re-running the identical
// command can never succeed.
func errAssigneeGate(value, what, hint string) *mgerr.Error {
	return mgerr.Usage("invalid_value",
		fmt.Sprintf("--assignee=%q %s — it would NOT gate dispatch.\n"+
			"  gated values: human, parked, blocked:<agent>", value, what),
		hint)
}

// nearMiss reports whether a is a plausible misspelling of the (short, fixed)
// vocabulary word b: same word in another case, or within two edits of it.
//
// Two edits is calibrated to `blocked` — a seven-character structural token
// where a transposition (`blocekd`) or a dropped suffix (`block`) is the
// realistic error, and where nothing legitimate lives nearby. It is deliberately
// NOT applied to the five-letter sentinels on their own, where a two-edit
// neighbourhood contains real names.
func nearMiss(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(b)
	if a == b {
		return true
	}
	if len(a) < 4 {
		return false
	}
	return editDistance(a, b) <= 2
}

// editDistance is Levenshtein over two short ASCII words.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

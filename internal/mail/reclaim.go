package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// Reclaim drains superseded generator mail out of an active mailbox.
//
// WHAT THIS IS FOR. A scheduler fire that cannot reach an agent's PTY falls
// back to writing the fire into that agent's mailbox. Nothing ever removed
// those copies, so one 33-hour crew outage rendered as 264 consecutive
// `From: scheduler` rows in one box and 12,295 fleet-wide — a pile every mail
// check then reads through. Two agents ran a bounded read over their own
// mailbox on 2026-08-09, saw nothing but scheduler rows, and nearly reported
// "no mail" over a real message 32 hours old at row ~108 of 265. Coalescing
// (mg-af83) bounds what a FUTURE outage writes; it reclaims nothing already
// written, which is what this does.
//
// WHY THE COPIES CAN GO. A fallback copy exists to tell an agent about a fire
// it missed. A LATER copy of the same recurring fire carries the same
// instruction, so once one exists the older copies have no remaining function —
// and every crew agent runs a mail check as its first action on startup anyway,
// so the mailbox is read on return regardless. What the older copies did buy
// was burying the one message that mattered.
//
// WHAT IT MUST NEVER DO. Take a message from a real sender. A reclaim pass that
// cannot tell generator noise from correspondence is strictly worse than the
// backlog, because its failure is silent. So selection is by the parsed From
// FIELD via opts.Match (the mg-5168 predicate, exact field equality — never a
// substring, never the subject text, never row volume), and three further
// conditions must all hold before a message moves:
//
//   - it is not among the newest opts.Keep copies of its recurring notification
//     (grouped by exact Subject — two fires of one schedule carry byte-identical
//     subjects and two different schedules never do), so a live pointer always
//     survives in every group;
//   - its Date parses AND is strictly older than Now-OlderThan;
//   - it is still where the listing said it was (a message consumed
//     concurrently is left alone, not chased).
//
// A message is MOVED to archive/, never deleted: 'mg mail list AGENT --archived'
// still shows it and the audit log names every move. The mistake this replaces —
// a 1,594-message bulk sweep that very nearly took a triage packet and a fleet
// notify report with it — was not archiving too much, it was selecting by
// volume. This selects by sender.
type ReclaimOpts struct {
	// Match reports whether a message's From field names a reclaimable
	// generator. Required: a nil Match is an error, never "match everything".
	Match func(from string) bool
	// Keep is how many of the newest copies to retain per Subject group.
	// Values below 1 are raised to 1 — a group is never emptied, because the
	// newest copy is the one an agent returning from an outage would read.
	Keep int
	// OlderThan is the retention window. A copy whose Date is not strictly
	// older than Now-OlderThan is retained even when superseded.
	OlderThan time.Duration
	// Now is the clock the window is measured against. Zero means time.Now().
	Now time.Time
	// DryRun counts what would be reclaimed without moving anything.
	DryRun bool
}

// ReclaimResult reports one mailbox's outcome. Scanned counts only messages
// whose From matched — mail from every other sender is never examined beyond
// its From field, so it cannot appear in any of these totals.
type ReclaimResult struct {
	Mailbox   string
	Scanned   int // sender-matching messages in new/ + cur/
	Reclaimed int // moved to archive/ (or, under DryRun, would be)
	Retained  int // sender-matching messages left in place
	Groups    int // distinct Subject groups among the matches
	Undated   int // retained because their Date header would not parse
}

// reclaimCand pairs a message with its parsed Date. ok is false when the Date
// header is missing or unparseable, which is a retain-forever condition: a
// window cannot be applied to a message whose age is unknown.
type reclaimCand struct {
	msg Message
	at  time.Time
	ok  bool
}

// Reclaim applies the policy above to one mailbox. A mailbox that does not
// exist yields a zero result and no error — the fleet-wide form walks every
// box, and a box with nothing to drain is the normal case.
func Reclaim(mailRoot, agent string, opts ReclaimOpts) (ReclaimResult, error) {
	res := ReclaimResult{Mailbox: agent}
	if err := checkMailbox(agent); err != nil {
		return res, err
	}
	if opts.Match == nil {
		// Defaulting to "match everything" here would turn a caller's bug into
		// a fleet-wide sweep of real correspondence. Refuse instead.
		return res, fmt.Errorf("reclaim: no sender predicate given")
	}
	if opts.Keep < 1 {
		opts.Keep = 1
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-opts.OlderThan)

	msgs, _, err := ListAll(mailRoot, agent)
	if err != nil {
		return res, err
	}

	groups := map[string][]reclaimCand{}
	for _, m := range msgs {
		if !opts.Match(m.From) {
			continue
		}
		res.Scanned++
		at, perr := time.Parse(time.RFC3339, m.Date)
		groups[m.Subject] = append(groups[m.Subject], reclaimCand{msg: m, at: at, ok: perr == nil})
	}
	res.Groups = len(groups)

	// Iterate in a stable order so a dry run and the run that follows it report
	// the same thing, and so a failure part-way through is reproducible.
	subjects := make([]string, 0, len(groups))
	for s := range groups {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)

	for _, s := range subjects {
		g := groups[s]
		// Undated copies sort OLDEST, deliberately. Sorting them newest would
		// let one occupy a Keep slot and push a genuinely-newest copy into the
		// reclaim range — losing the live pointer to protect a message the
		// date check retains anyway.
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].ok != g[j].ok {
				return !g[i].ok
			}
			if g[i].ok && !g[i].at.Equal(g[j].at) {
				return g[i].at.Before(g[j].at)
			}
			return g[i].msg.ID < g[j].msg.ID
		})

		cut := len(g) - opts.Keep
		if cut < 0 {
			cut = 0
		}
		res.Retained += len(g) - cut // the newest Keep, unconditionally

		for _, c := range g[:cut] {
			if !c.ok {
				res.Retained++
				res.Undated++
				continue
			}
			if !c.at.Before(cutoff) {
				res.Retained++
				continue
			}
			if opts.DryRun {
				res.Reclaimed++
				continue
			}
			moved, err := reclaimOne(mailRoot, agent, c.msg.ID)
			if err != nil {
				return res, err
			}
			if !moved {
				res.Retained++
				continue
			}
			res.Reclaimed++
		}
	}

	if !opts.DryRun && res.Reclaimed > 0 {
		event.Emit(eventsRoot(mailRoot), "mail.reclaimed", map[string]string{
			"mailbox":   agent,
			"reclaimed": fmt.Sprintf("%d", res.Reclaimed),
			"retained":  fmt.Sprintf("%d", res.Retained),
		})
	}

	return res, nil
}

// reclaimOne moves one message from new/ or cur/ into archive/ and records the
// move in the audit log. It reports false (with no error) when the message is
// no longer in either directory — consumed concurrently by a reader, not an
// error to fail the sweep over.
//
// This is Archive's move WITHOUT Archive's per-message event. A sweep drains
// thousands of copies at once, and one events.jsonl line per copy would rebuild
// inside the event log exactly the unbounded pile this command exists to drain —
// the remedy exhibiting the defect it remedies. Reclaim emits one summary event
// per mailbox instead. The per-message record still exists, in the audit log,
// which nothing reads to decide whether it has mail.
func reclaimOne(mailRoot, agent, msgID string) (bool, error) {
	if err := checkMsgID(agent, msgID); err != nil {
		return false, err
	}
	newPath := filepath.Join(mailRoot, agent, "new", msgID)
	curPath := filepath.Join(mailRoot, agent, "cur", msgID)
	archiveDir := filepath.Join(mailRoot, agent, "archive")

	src, fromDir := "", ""
	if _, err := os.Stat(curPath); err == nil {
		src, fromDir = curPath, "cur"
	} else if _, err := os.Stat(newPath); err == nil {
		src, fromDir = newPath, "new"
	}
	if src == "" {
		return false, nil
	}

	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return false, fmt.Errorf("creating archive/: %w", err)
	}
	// uniqueDest, as in MergeMailbox: an id already present in archive/ keeps
	// its content under a suffixed name rather than being overwritten.
	dst := uniqueDest(archiveDir, msgID)
	if err := os.Rename(src, dst); err != nil {
		return false, fmt.Errorf("moving to archive/: %w", err)
	}

	Audit(mailRoot, "reclaim", agent, filepath.Base(dst), map[string]string{"from_dir": fromDir})
	return true, nil
}

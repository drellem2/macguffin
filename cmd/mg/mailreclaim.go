package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/spf13/cobra"
)

var (
	mailReclaimFrom      []string
	mailReclaimOlderThan string
	mailReclaimKeep      int
	mailReclaimDryRun    bool
	mailReclaimForce     bool
)

// defaultReclaimSender is the one sender reclaimed when --from is not given.
// It is a single name on purpose: a default that swept several generators would
// make the blast radius of a bare `mg mail reclaim` something the caller has to
// remember rather than read.
const defaultReclaimSender = "scheduler"

// maxReclaimRows bounds the per-mailbox rows on the human report. The fleet has
// well over a hundred mailboxes; a row each would push the totals off the
// bottom of a bounded read, which is the failure this command was filed to end.
const maxReclaimRows = 20

// mailReclaimJSON is one NDJSON line of `mg mail reclaim --json`: one object per
// mailbox that had anything to reclaim.
type mailReclaimJSON struct {
	Mailbox   string `json:"mailbox"`
	Scanned   int    `json:"scanned"`
	Reclaimed int    `json:"reclaimed"`
	Retained  int    `json:"retained"`
	Groups    int    `json:"groups"`
	Undated   int    `json:"undated"`
	DryRun    bool   `json:"dry_run"`
}

// mailReclaimSummaryJSON is the trailing object, always emitted — including when
// nothing was reclaimed, so a scripted consumer is never handed an empty stream
// it has to interpret. It carries no "mailbox" field, so a consumer selecting on
// .mailbox skips it, the way the mail-list sentinel carries no "id".
type mailReclaimSummaryJSON struct {
	Mailboxes        int      `json:"mailboxes"`
	MailboxesTouched int      `json:"mailboxes_touched"`
	Scanned          int      `json:"scanned"`
	Reclaimed        int      `json:"reclaimed"`
	Retained         int      `json:"retained"`
	Undated          int      `json:"undated"`
	From             []string `json:"from"`
	OlderThan        string   `json:"older_than"`
	Keep             int      `json:"keep"`
	DryRun           bool     `json:"dry_run"`
}

var mailReclaimCmd = &cobra.Command{
	Use:   "reclaim [AGENT]",
	Short: "Move superseded scheduler fallback mail out of active mailboxes",
	Long: `Reclaim superseded generator mail, moving it out of the active mailbox.

A scheduler fire that cannot reach an agent's PTY falls back to writing the fire
into that agent's mailbox. Nothing ever removed those copies: one 33-hour crew
outage rendered as 264 consecutive "From: scheduler" rows in a single box, and
12,295 fleet-wide. Every mail check then reads through the pile. Two agents ran
a bounded read over their own mailbox, saw nothing but scheduler rows, and
nearly reported "no mail" over a real message 32 hours old at row ~108 of 265.

Coalescing bounds what a FUTURE outage writes. This drains what is already
there.

WHAT MOVES. A message is reclaimed only when ALL of these hold:

  1. its From FIELD exactly matches a --from sender (never a substring, never
     the subject text, never row volume) — case-insensitively, and with the same
     mg-/cat- prefix stripping every mailbox argument gets, so --from=scheduler
     matches a From of "scheduler" and not one of "scheduler-v2";
  2. it is not among the newest --keep copies of its recurring notification,
     grouped by exact Subject — two fires of one schedule carry byte-identical
     subjects and two different schedules never do, so every group keeps a live
     pointer;
  3. its Date parses and is strictly older than --older-than.

A copy whose Date will not parse is retained, and counted, rather than guessed
at. Everything reclaimed is MOVED to archive/, never deleted:

  mg mail list AGENT --archived

still shows it, and the mail audit log names every move (op=reclaim).

WHY SUPERSESSION IS SOUND. A fallback copy exists to tell an agent about a fire
it missed. A later copy of the same recurring fire carries the same instruction,
so the older ones have no remaining function — and every crew agent runs a mail
check as its first action on startup, so the mailbox is read on return anyway.
What the older copies did buy was burying the message that mattered.

WHY IT SELECTS BY SENDER. The alternative already happened: a 1,594-message bulk
sweep, run under pressure, very nearly took a triage packet and a fleet notify
report with it — caught only by re-listing the archive afterwards. That sweep
was not wrong because it archived too much; it was wrong because it selected by
volume. This selects by a parsed field, so no amount of noise can put a real
message in range.

SENDERS THAT CAN BE REPLIED TO ARE REFUSED. --from naming a sender that has a
mailbox of its own (mayor, architect, pa, ...) is refused without --force: that
name is a correspondent, and the supersession argument above holds for recurring
machine notifications, not for two messages that happen to share a subject.
Generators like "scheduler" and "stall-watch" have no mailbox — nobody replies
to them — which is what makes them safe to sweep.

WHAT IS OUT OF SCOPE. archive/ is not pruned, and nothing here deletes. This
command reclaims the READ PATH — the rows every mail check pages through — and
that is the damage on record: buried real mail and two near-miss false
negatives. Reclaimed copies still occupy disk under archive/, deliberately, so
a reclaim is always reversible. Deciding when an archived copy may actually be
destroyed is a separate policy, and deletion is the one operation whose failure
is silent and permanent.

  mg mail reclaim --dry-run                  # fleet-wide, count only
  mg mail reclaim architect                  # one mailbox
  mg mail reclaim --from=scheduler,stall-watch --older-than=7d
  mg mail reclaim --keep=3 --older-than=0    # keep 3 per schedule, no window

With no AGENT every mailbox is swept. Under --json each touched mailbox is one
NDJSON object, followed by one summary object carrying no "mailbox" field.

A named AGENT that never existed reports "No such mailbox", not "Nothing to
reclaim" — the second reads as "this box is clean" when the truth is "this box
is not a box". Exit stays 0, and under --json the summary carries
"mailboxes": 0 (a named box that exists always counts 1).`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		mr, err := mailRoot()
		if err != nil {
			return err
		}

		senders := mailReclaimFrom
		if !cmd.Flags().Changed("from") {
			senders = []string{defaultReclaimSender}
		}

		// Reuse the mg-5168 predicate rather than re-deriving sender matching:
		// same trimming, same lowercasing, same canonicalAgent prefix
		// stripping, same refusal of an empty or control-bearing name. An
		// include-only filter is exactly "reclaim these senders".
		filter, err := newSenderFilter(senders, nil, true, false)
		if err != nil {
			return err
		}

		if err := refuseCorrespondents(mr, senders); err != nil {
			return err
		}

		window, err := parseDuration(mailReclaimOlderThan)
		if err != nil {
			return mgerr.Usage("invalid_value",
				fmt.Sprintf("--older-than: %s", err),
				"pass a duration like 24h, 30m or 7d; --older-than=0 disables the window")
		}
		if window < 0 {
			return mgerr.Usage("invalid_value",
				fmt.Sprintf("--older-than must not be negative, got %q", mailReclaimOlderThan),
				"a negative window would reclaim mail from the future; pass 0 to disable the window")
		}
		if mailReclaimKeep < 1 {
			return mgerr.Usage("invalid_value",
				fmt.Sprintf("--keep must be at least 1, got %d", mailReclaimKeep),
				"--keep=1 retains the newest copy of each schedule; a group is never emptied")
		}

		total := mailReclaimSummaryJSON{
			From:      sortedCopy(senders),
			OlderThan: strings.TrimSpace(mailReclaimOlderThan),
			Keep:      mailReclaimKeep,
			DryRun:    mailReclaimDryRun,
		}

		var boxes []string
		if len(args) == 1 {
			agent := canonicalAgent(args[0])
			// A mailbox that never existed must not report "Nothing to
			// reclaim": that reads as "this box is clean" when the truth is
			// "this box is not a box" — the same manufactured absence
			// 'mg mail list' stopped producing (mg-d639). Exit stays 0; asking
			// after a box nobody has mailed is a fair question.
			if !mail.MailboxExists(mr, agent) {
				root, rerr := resolveRoot()
				if rerr != nil {
					return rerr
				}
				if mailJSON {
					// mailboxes:0 on a NAMED form is the machine-readable
					// "that box does not exist" — a named box that does exist
					// always counts 1.
					return encodeJSON(total)
				}
				fmt.Printf("No such mailbox: %s — it has never existed, so no mail has ever been delivered to it\n%s",
					agent, suggestionLine(mr, root, agent))
				return nil
			}
			boxes = []string{agent}
		} else {
			all, err := mail.ListMailboxes(mr)
			if err != nil {
				return err
			}
			for _, b := range all {
				boxes = append(boxes, b.Name)
			}
		}

		opts := mail.ReclaimOpts{
			Match:     func(from string) bool { return filter.keep(mail.Message{From: from}) },
			Keep:      mailReclaimKeep,
			OlderThan: window,
			DryRun:    mailReclaimDryRun,
		}

		var results []mail.ReclaimResult
		total.Mailboxes = len(boxes)
		for _, box := range boxes {
			res, err := mail.Reclaim(mr, box, opts)
			if err != nil {
				return fmt.Errorf("reclaiming %s: %w", box, err)
			}
			total.Scanned += res.Scanned
			total.Reclaimed += res.Reclaimed
			total.Retained += res.Retained
			total.Undated += res.Undated
			if res.Reclaimed > 0 {
				total.MailboxesTouched++
				results = append(results, res)
			}
		}

		// Report per-mailbox rows only for boxes that actually moved mail. A
		// fleet sweep walks ~150 mailboxes and a row each would be its own wall
		// of text to read past — the shape this command exists to remove.
		if mailJSON {
			for _, r := range results {
				if err := encodeJSON(mailReclaimJSON{
					Mailbox:   r.Mailbox,
					Scanned:   r.Scanned,
					Reclaimed: r.Reclaimed,
					Retained:  r.Retained,
					Groups:    r.Groups,
					Undated:   r.Undated,
					DryRun:    mailReclaimDryRun,
				}); err != nil {
					return err
				}
			}
			return encodeJSON(total)
		}

		verb, tense := "Reclaimed", "reclaimed"
		if mailReclaimDryRun {
			verb, tense = "Would reclaim", "would be reclaimed"
		}

		fmt.Printf("%s mail from %s older than %s, keeping the newest %d per schedule.\n\n",
			verb, strings.Join(total.From, ", "), humanWindow(window), mailReclaimKeep)

		// Heaviest first, and bounded. A fleet sweep can touch a hundred boxes,
		// and an unbounded row-per-box report is the same wall this command
		// exists to drain — one whose totals a `| head` would then cut off.
		// --json carries every row for anything that needs them all.
		sort.SliceStable(results, func(i, j int) bool { return results[i].Reclaimed > results[j].Reclaimed })
		shown := results
		if len(shown) > maxReclaimRows {
			shown = shown[:maxReclaimRows]
		}
		for _, r := range shown {
			fmt.Printf("  %-24s %6d %s, %d retained\n", r.Mailbox, r.Reclaimed, tense, r.Retained)
		}
		if rest := len(results) - len(shown); rest > 0 {
			restTotal := 0
			for _, r := range results[len(shown):] {
				restTotal += r.Reclaimed
			}
			fmt.Printf("  %-24s %6d %s (--json for every row)\n",
				fmt.Sprintf("… %d more mailbox(es)", rest), restTotal, tense)
		}
		if len(results) > 0 {
			fmt.Println()
		}

		if total.Reclaimed == 0 {
			fmt.Printf("Nothing to reclaim: %d matching message(s) across %d mailbox(es), all retained.\n",
				total.Scanned, total.Mailboxes)
		} else {
			fmt.Printf("%d of %d matching message(s) %s across %d of %d mailbox(es); %d retained.\n",
				total.Reclaimed, total.Scanned, tense, total.MailboxesTouched, total.Mailboxes, total.Retained)
		}
		if total.Undated > 0 {
			fmt.Printf("%d retained because their Date header would not parse.\n", total.Undated)
		}
		if !mailReclaimDryRun && total.Reclaimed > 0 {
			fmt.Println("Nothing was deleted — reclaimed mail is in archive/ ('mg mail list AGENT --archived').")
		}
		return nil
	},
}

// refuseCorrespondents rejects a --from sender that has a mailbox of its own.
//
// It is the structural half of "must not touch mail from real senders". The
// supersession rule — an older copy sharing a subject has no remaining function
// — is an argument about recurring machine notifications. It is false for
// correspondence: two replies in one thread carry the identical subject and the
// older one is not obsolete. A mailbox is the observable difference. Generators
// write a From nobody can reply to ("scheduler", "stall-watch" — neither has a
// box); a correspondent has one, because that is where its replies arrive.
//
// The check is deliberately one-directional: it can only ever refuse, and it is
// overridable with --force. If someone later registers a mailbox named
// "scheduler", a bare reclaim starts refusing rather than starts deleting.
func refuseCorrespondents(mailRootDir string, senders []string) error {
	if mailReclaimForce {
		return nil
	}
	for _, raw := range senders {
		// normalizeSender, so the name checked here is the same key the
		// predicate will compare against — otherwise a spelling could be
		// waved through by one and matched by the other.
		name := normalizeSender(raw)
		if name == "" || !mail.MailboxExists(mailRootDir, name) {
			continue
		}
		return mgerr.Conflict("reclaimable_sender",
			fmt.Sprintf("refusing to reclaim mail from %q: that sender has a mailbox of its own, so it is a correspondent, not a generator", name),
			"reclaim supersedes older copies of a RECURRING notification; two messages in a real thread also share a subject, and the older one is not obsolete. Pass --force if you mean it, and run --dry-run first")
	}
	return nil
}

// humanWindow renders the retention window for the report, naming the
// zero-window case outright rather than printing "0s" — a sweep with no window
// is the widest form of the command and should read that way.
func humanWindow(d time.Duration) string {
	if d == 0 {
		return "any age (no window)"
	}
	return d.String()
}

// sortedCopy returns the names in a stable order without mutating the caller's
// flag slice, which cobra hands back by reference.
func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	sort.Strings(out)
	return out
}

func init() {
	mailReclaimCmd.Flags().StringSliceVar(&mailReclaimFrom, "from", []string{defaultReclaimSender},
		"senders whose superseded copies are reclaimable (repeatable, comma-separated); exact match on the From field")
	mailReclaimCmd.Flags().StringVar(&mailReclaimOlderThan, "older-than", "24h",
		"only reclaim copies older than this (e.g. 30m, 24h, 7d); 0 disables the window")
	mailReclaimCmd.Flags().IntVar(&mailReclaimKeep, "keep", 1,
		"newest copies retained per schedule (exact Subject group); never below 1")
	mailReclaimCmd.Flags().BoolVar(&mailReclaimDryRun, "dry-run", false,
		"report what would be reclaimed without moving any mail")
	mailReclaimCmd.Flags().BoolVar(&mailReclaimForce, "force", false,
		"allow --from to name a sender that has a mailbox of its own (a correspondent)")
	mailReclaimCmd.Flags().BoolVar(&mailJSON, "json", false,
		"emit one JSON object per touched mailbox (NDJSON) plus a trailing summary")

	mailCmd.AddCommand(mailReclaimCmd)
}

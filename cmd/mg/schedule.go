package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Promote pending items whose gates have opened, and report those that can never be promoted",
	Long: `Promote pending items whose gates have all opened.

Two gates exist and both must open. A dependency is met once the parent has
passed through done — done/ and archive/ both count, because archiving is a
filing decision about completed work, not a repudiation of the completion. A
` + "`snooze:`" + ` gate opens once its wake time is in the past.

THIS COMMAND IS NO LONGER THE ONLY THING THAT OPENS A SNOOZE. Every mg
invocation promotes pending items whose wake time has passed, on a goroutine
beside whatever you actually ran, so a snooze opens on the next ` + "`mg`" + ` of any
kind rather than on the next sweep. That is what stops readiness depending on
one agent's cron still being alive — a lost sweep once hid four days of open
gates, and nothing but the sweep's own staleness warning noticed.

What this command still does, and nothing else does:

  - the DEPENDENCY gate, which the per-invocation promoter leaves alone (it
    opens on ` + "`mg done`" + `, which sweeps at that instant);
  - the HELD report — every pending item and the gates holding it;
  - the STRANDED report — items no completion can ever release, which automatic
    promotion by construction can never fix;
  - the spent-gate tidy-up, and the check that promotion is actually working.

Registering it on a clock is therefore still worth doing, for the reports:

  ` + workitem.DriverHint + `

The sweep is level-triggered — it asks whether a wake time has passed, not
whether it just arrived — so a run missed while the driver was down delays an
item and can never lose one.

Every pending item the sweep could NOT promote is listed, with each gate that
held it and that gate's state — the parent it waits on and what that parent is
doing, the wake time it has not reached, or both. The two gates are
independent and either can be the one still closed, so both are named. A sweep
that reported only one of them made "No items promoted." read as "nothing is
waiting", which is wrong exactly when a dependency gate is closed.

Items that no completion can ever release are reported rather than skipped
silently: a dependent waiting on a shelved parent, or on an id that does not
exist, or an item whose snooze value is not a parseable timestamp, is not
waiting — it is stranded. It cannot be seen from anywhere else, because it is
not available/ (so stall-watch and priority-wake do not reach it) and "pending"
is exactly what a correctly-waiting item looks like.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		// Read the previous stamp BEFORE overwriting it: a long gap is the one
		// symptom of an absent driver that this command is positioned to see.
		prior := workitem.CheckDriver(root, time.Now().UTC())

		// A failed sweep is HELD, not returned. It still exits non-zero at the
		// bottom, but the reports below run first — and they have to, because a
		// sweep that could not promote is exactly when the reader most needs to
		// be told WHICH items did not move. Returning here instead would make
		// the Unpromoted detector unreachable for the one cause it exists to
		// catch: promotion failing rather than promotion not being run.
		promoted, sweepErr := workitem.Schedule(root)
		if sweepErr != nil {
			fmt.Fprintf(os.Stderr, "warning: the sweep did not finish: %v\n", sweepErr)
		}

		// Stamp only from here, never from Done's internal sweep. The question
		// this record answers is "is something driving the sweep on a clock",
		// and a sweep that happened because somebody finished a ticket is not
		// an answer to it.
		if err := workitem.RecordSweep(root); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record the sweep time: %v\n", err)
		}

		if len(promoted) == 0 {
			fmt.Println("No items promoted.")
		}
		for _, item := range promoted {
			fmt.Printf("Promoted %s: %s\n", item.ID, item.Title)
		}

		// Tidy up promote()'s one residue: an elapsed wake time left on an item
		// that is no longer pending, because a process died — or a claim landed
		// — between the rename that promoted it and the write that clears the
		// spent gate. Inert, but it reads as though the item were still
		// scheduled. Doing it here rather than on every invocation keeps two
		// directory scans off the path of `mg list`.
		if cleared, err := workitem.ClearSpentGates(root); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not clear spent snooze values: %v\n", err)
		} else if len(cleared) > 0 {
			fmt.Printf("\nCleared %d spent snooze value(s) off items already promoted: %s\n",
				len(cleared), strings.Join(cleared, ", "))
		}

		// Report everything the sweep could not promote, with every gate that
		// held it and that gate's state. "No items promoted." over a non-empty
		// pending set is true and incomplete, and the reading it invites —
		// nothing is waiting — is wrong whenever a gate is closed. This is the
		// only view of that population, and it must be the WHOLE population:
		// reporting the snooze gate alone left an item blocked forever on a
		// dependency as the one case the sweep was silent about.
		held, err := workitem.Held(root)
		if err != nil {
			return err
		}
		if len(held) > 0 {
			now := time.Now().UTC()
			fmt.Printf("\n%d pending item(s) held:\n", len(held))
			for _, h := range held {
				fmt.Printf("  %s  %s — %s\n", h.Item.ID, h.Gates(now), h.Item.Title)
			}
		}

		// Report what the sweep could never promote. A pending item waiting on
		// a shelved or nonexistent parent is not waiting — it is stranded, and
		// it looks identical to a correctly-waiting item from every other
		// angle, which is why it goes unnoticed for weeks. This is the only
		// place that distinguishes the two, so it reports rather than stays
		// silent about the items it stepped over.
		stranded, err := workitem.Stranded(root)
		if err != nil {
			return err
		}
		if len(stranded) > 0 {
			badSnooze := false
			fmt.Printf("\n%d pending item(s) can never be promoted:\n", len(stranded))
			for _, s := range stranded {
				fmt.Printf("  %s: %s\n", s.Item.ID, s.Item.Title)
				fmt.Printf("    blocked because %s\n", s.Reason())
				badSnooze = badSnooze || s.BadSnooze != ""
			}
			fmt.Printf("\nUnshelve the parent, correct the dependency with `mg edit --rm-depends`,\n")
			if badSnooze {
				// Only offered when one was actually found: a remedy for a
				// problem nobody has is noise in the report that matters.
				fmt.Printf("correct a bad snooze with `mg unsnooze` then `mg snooze --until`,\n")
			}
			fmt.Printf("or shelve the dependent so it stops claiming to be waiting.\n")
		}

		// THE DETECTOR. An item still in pending/ with every gate open, the
		// instant after a sweep whose whole job was to move it, is not waiting
		// — it is a promotion that FAILED. A read-only store, a full disk, a
		// permissions change: causes the old staleness warning could never have
		// seen, because it measured the sweep's absence rather than the outcome.
		//
		// This is what replaces that warning as the snooze safety net, and it
		// has to be louder than the warning was, because it can only mean
		// something is broken. The warning it replaces could fire on a merely
		// idle fleet.
		stuck, err := workitem.Unpromoted(root)
		if err != nil {
			return err
		}
		if len(stuck) > 0 {
			fmt.Fprintf(os.Stderr, "\nwarning: %d pending item(s) have every gate open and were NOT promoted:\n", len(stuck))
			for _, item := range stuck {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", item.ID, item.Title)
			}
			fmt.Fprintf(os.Stderr, "This means promotion itself is failing — the store may be read-only or full.\n")
			fmt.Fprintf(os.Stderr, "Check write access to %s.\n", root)
		}

		// A long gap since the previous sweep no longer means snoozes are shut:
		// every mg invocation opens those now. It means nobody is READING the
		// two reports above on a clock, and the stranded population is the one
		// automatic promotion can never clear — an item waiting on a shelved or
		// nonexistent parent stays pending forever no matter how often mg runs.
		// So the warning survives, pointed at what it actually detects now.
		if prior.Stale && (len(held) > 0 || len(stranded) > 0) {
			gap := "no previous sweep was ever recorded"
			if prior.Ever {
				gap = fmt.Sprintf("the previous sweep ran %s ago", workitem.HumanUntil(prior.Since))
			}
			fmt.Fprintf(os.Stderr, "\nwarning: %s, and %d pending item(s) are held or stranded.\n", gap, len(held)+len(stranded))
			fmt.Fprintf(os.Stderr, "Elapsed snoozes open by themselves on any `mg` command — that no longer needs this sweep.\n")
			fmt.Fprintf(os.Stderr, "This report does: a stranded item waits forever and is visible from nowhere else.\n")
			fmt.Fprintf(os.Stderr, "Register a driver:\n\n  %s\n", workitem.DriverHint)
		}

		// Held until now so every report above ran. The exit code still tells
		// the truth about the sweep.
		return sweepErr
	},
}

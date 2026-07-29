package main

import (
	"fmt"
	"os"
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

THIS COMMAND IS THE DRIVER. A snooze does nothing on its own: this sweep is
what returns a snoozed item to available/, so something must run it on a clock.
Register it once with pogod, which replays through host sleep and NTP steps:

  ` + workitem.DriverHint + `

The sweep is level-triggered — it asks whether a wake time has passed, not
whether it just arrived — so a run missed while the driver was down delays an
item and can never lose one. It also stamps work/.last-sweep, which is what
` + "`mg snooze`" + ` checks before agreeing to set a gate at all.

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

		promoted, err := workitem.Schedule(root)
		if err != nil {
			return err
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
		anySnoozed := false
		if len(held) > 0 {
			now := time.Now().UTC()
			fmt.Printf("\n%d pending item(s) held:\n", len(held))
			for _, h := range held {
				fmt.Printf("  %s  %s — %s\n", h.Item.ID, h.Gates(now), h.Item.Title)
				anySnoozed = anySnoozed || h.Snoozed
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

		// A long gap since the previous sweep is the symptom of an absent
		// driver. Saying so here — while somebody is looking at sweep output —
		// is the difference between a gap that gets fixed and fifteen days.
		// Only a snooze needs the clock: a dependency gate opens on `mg done`,
		// so a dependency-only hold is no evidence about the driver either way.
		if prior.Stale && (anySnoozed || len(promoted) > 0) {
			gap := "no previous sweep was ever recorded"
			if prior.Ever {
				gap = fmt.Sprintf("the previous sweep ran %s ago", workitem.HumanUntil(prior.Since))
			}
			fmt.Fprintf(os.Stderr, "\nwarning: %s. Snoozes open only when this sweep runs.\n", gap)
			fmt.Fprintf(os.Stderr, "Register a driver:\n\n  %s\n", workitem.DriverHint)
		}
		return nil
	},
}

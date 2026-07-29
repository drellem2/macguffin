package main

import (
	"fmt"
	"os"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	snoozeUntil string
	snoozeFor   string
	snoozeForce bool
)

var snoozeCmd = &cobra.Command{
	Use:   "snooze ID (--until TIME | --for DURATION)",
	Short: "Set a work item aside until a time, returning it to available/ then",
	Long: `Snooze sets a work item aside until a wake time and files it in pending/,
which is already where an item waiting on a gate lives. ` + "`mg schedule`" + ` returns it
to available/ on the first sweep after that time.

Snooze is an ATTRIBUTE, not a status. Status in mg is the directory an item is
in; a snoozed item is a pending item carrying a ` + "`snooze:`" + ` timestamp. It waits on
a CLOCK exactly as ` + "`depends:`" + ` waits on an ITEM, and both gates must open before
the item is released.

  mg snooze mg-1234 --for 3d
  mg snooze mg-1234 --until 2026-08-03            # 09:00 local on that day
  mg snooze mg-1234 --until "2026-08-03 14:30"    # local time
  mg snooze mg-1234 --until 2026-08-03T14:30:00Z  # explicit zone
  mg unsnooze mg-1234                             # lift it early

--for accepts Go durations plus d (days) and w (weeks): 90m, 6h, 3d, 2w.
--until accepts: ` + workitem.SnoozeFormats() + `
A date with no time means 09:00 LOCAL on that date, not midnight. The resolved
absolute instant is always echoed back, and it is always stored as RFC3339 UTC.

Three refusals, because a snooze nothing will open is worse than no snooze at
all — it looks scheduled, it is not available, and nothing nags:

  - a wake time that has already passed, or that mg cannot parse, is refused
    here rather than written and forgotten;
  - a snooze is refused entirely when nothing has driven ` + "`mg schedule`" + ` recently
    (the sweep is what opens the gate). --force overrides, loudly.

Snoozing a claimed item releases the claim, the same way shelving does.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		id := args[0]

		until, err := resolveWakeTime(snoozeUntil, snoozeFor, time.Now())
		if err != nil {
			return err
		}

		if err := requireDriver(root, snoozeForce); err != nil {
			return err
		}

		item, from, err := workitem.SnoozeItem(root, id, until)
		if err != nil {
			return err
		}

		wait := workitem.HumanUntil(time.Until(item.Snooze))
		fmt.Printf("Snoozed %s until %s (in %s): %s\n", item.ID, item.SnoozeRaw, wait, item.Title)
		fmt.Printf("It is in pending/ (was %s) and returns to available/ on the first `mg schedule` sweep after that time.\n", from)
		return nil
	},
}

var unsnoozeCmd = &cobra.Command{
	Use:   "unsnooze ID",
	Short: "Lift a snooze early, releasing the item if its other gates are open",
	Long: `Unsnooze removes a work item's ` + "`snooze:`" + ` attribute.

A pending item is returned to available/ immediately if its dependencies are
also satisfied, and stays pending if they are not — lifting one gate does not
lift the others. An inert snooze on an item that is not pending (only a
hand-edit can produce one) is cleared where it lies; the item is not moved.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		item, dest, err := workitem.UnsnoozeItem(root, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Unsnoozed %s → %s: %s\n", item.ID, dest, item.Title)
		return nil
	},
}

// resolveWakeTime turns the operator's --until/--for into an absolute instant,
// refusing everything that would produce a gate nobody meant to set.
//
// A past wake time is refused rather than accepted-and-swept. The sweep is
// level-triggered, so a past time would in fact promote on the next run — but
// an operator who types last year's date meant a future one, and accepting it
// silently is how "until Mon 2026-07-14" sat unnoticed for fifteen days.
func resolveWakeTime(until, dur string, now time.Time) (time.Time, error) {
	switch {
	case until != "" && dur != "":
		return time.Time{}, mgerr.Usage("mutually_exclusive_flags",
			"cannot use both --until and --for",
			"--until names an absolute time; --for names a duration from now.")
	case until == "" && dur == "":
		return time.Time{}, mgerr.Usage("missing_flag",
			"a wake time is required: pass --until TIME or --for DURATION",
			"A snooze with no wake time is an item nothing will ever return to available/.")
	}

	var (
		t   time.Time
		err error
	)
	if until != "" {
		t, err = workitem.ParseSnoozeUntil(until, time.Local)
		if err != nil {
			return time.Time{}, mgerr.Usage("invalid_value",
				fmt.Sprintf("--until: %v", err),
				"Accepted formats: "+workitem.SnoozeFormats()+". A bare date means 09:00 local.")
		}
	} else {
		t, err = workitem.ParseSnoozeFor(dur, now)
		if err != nil {
			return time.Time{}, mgerr.Usage("invalid_value",
				fmt.Sprintf("--for: %v", err),
				"Accepted durations: Go syntax (90m, 6h) plus d and w (3d, 2w).")
		}
	}

	if !t.After(now) {
		return time.Time{}, mgerr.Usage("invalid_value",
			fmt.Sprintf("that wake time (%s) has already passed — a snooze must name a future time.",
				t.UTC().Format(time.RFC3339)),
			"Nothing would be scheduled: the item would move to pending/ and come straight back. Pick a future time, or leave the item where it is.")
	}
	return t, nil
}

// requireDriver refuses to set a gate when nothing is opening gates.
//
// This is the condition the whole feature ships under. `mg schedule` is what
// returns a snoozed item to available/, and for most of mg's life nothing ran
// it on a clock — which is precisely how two items sat fifteen days behind a
// gate whose date had passed. Rather than trust that a driver exists, mg
// checks: `mg schedule` stamps work/.last-sweep on every run, and a stamp
// older than DriverStaleAfter (or no stamp at all) means an item snoozed now
// would sit in pending/ indefinitely, looking exactly like an item that is
// waiting correctly.
func requireDriver(root string, force bool) error {
	st := workitem.CheckDriver(root, time.Now().UTC())
	if !st.Stale {
		return nil
	}

	last := "never — no sweep has ever been recorded in this store"
	if st.Ever {
		last = fmt.Sprintf("%s (%s ago)", st.Last.Format(time.RFC3339), workitem.HumanUntil(st.Since))
	}

	if force {
		fmt.Fprintf(os.Stderr, "warning: nothing is driving `mg schedule` (last sweep: %s).\n", last)
		fmt.Fprintf(os.Stderr, "warning: --force accepted anyway; this item will not return to available/ until something runs the sweep.\n")
		return nil
	}

	return mgerr.Conflict("no_snooze_driver",
		fmt.Sprintf("refusing to snooze: nothing is driving `mg schedule` (last sweep: %s).", last),
		"A snooze is only a gate — the sweep is what opens it, so an item snoozed now would\n"+
			"sit in pending/ indefinitely, looking exactly like an item that is waiting correctly.\n"+
			"Register the driver, then retry:\n\n  "+workitem.DriverHint+"\n\n"+
			"Running `mg schedule` once also clears this check. Use --force to snooze anyway.")
}

func init() {
	snoozeCmd.Flags().StringVar(&snoozeUntil, "until", "", "absolute wake time ("+workitem.SnoozeFormats()+"; a bare date means 09:00 local)")
	snoozeCmd.Flags().StringVar(&snoozeFor, "for", "", "wake time as a duration from now (90m, 6h, 3d, 2w)")
	snoozeCmd.Flags().BoolVar(&snoozeForce, "force", false, "snooze even when nothing is driving `mg schedule` (prints a warning)")
}

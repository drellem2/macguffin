package main

import (
	"fmt"
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
which is already where an item waiting on a gate lives. It returns to available/
on the first ` + "`mg`" + ` command of any kind after that time — every mg invocation
promotes elapsed snoozes, so nothing needs to be scheduled for this to work.

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

A wake time that has already passed, or that mg cannot parse, is refused here
rather than written and forgotten: a snooze nothing will open is worse than no
snooze at all, because it looks scheduled, it is not available, and nothing
nags.

mg used to refuse a snooze entirely when nothing had run ` + "`mg schedule`" + ` recently,
since the sweep was the only thing that opened a gate. It no longer is, so that
refusal is gone and --force has nothing left to override.

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

		item, from, err := workitem.SnoozeItem(root, id, until)
		if err != nil {
			return err
		}

		wait := workitem.HumanUntil(time.Until(item.Snooze))
		fmt.Printf("Snoozed %s until %s (in %s): %s\n", item.ID, item.SnoozeRaw, wait, item.Title)
		fmt.Printf("It is in pending/ (was %s) and returns to available/ on the first `mg` command after that time.\n", from)
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

// The driver refusal is GONE, and this comment is where it used to be.
//
// `mg snooze` used to refuse outright when work/.last-sweep was older than
// DriverStaleAfter. The reasoning was sound for the world it shipped into: a
// snooze was only a gate, `mg schedule` was the only thing that opened it, and
// an item snoozed with no cron running would sit in pending/ indefinitely
// looking exactly like an item that was waiting correctly. That is how two
// items sat fifteen days behind a wake time that had already passed.
//
// Opportunistic promotion makes the premise false. Any mg invocation opens an
// elapsed gate now (see cmd/mg/autopromote.go), so the opener is the binary
// itself and cannot be missing while anyone is using mg at all. Keeping the
// refusal would mean refusing to write a gate that in fact works — and worse,
// it would refuse precisely in the situation this feature was built for, a
// fleet whose sweep cron has been lost.
//
// What did NOT move to the promoter: the held and stranded reports. `mg
// schedule` remains worth running on a clock for those, and it still says so
// when its own stamp is stale. The precondition that has genuinely gone away is
// the one about snoozes opening, so that is the one refusal that has gone away.

func init() {
	snoozeCmd.Flags().StringVar(&snoozeUntil, "until", "", "absolute wake time ("+workitem.SnoozeFormats()+"; a bare date means 09:00 local)")
	snoozeCmd.Flags().StringVar(&snoozeFor, "for", "", "wake time as a duration from now (90m, 6h, 3d, 2w)")

	// --force is kept and ignored. Its only job was to override the driver
	// refusal above; removing the flag outright would turn every script that
	// passes it into an exit-2 unknown-flag failure, which is a worse outcome
	// than a deprecation notice for a flag that now has nothing to force.
	snoozeCmd.Flags().BoolVar(&snoozeForce, "force", false, "no longer has any effect")
	_ = snoozeCmd.Flags().MarkDeprecated("force",
		"snoozes now open on any mg command, so there is no driver check left to override.")
}

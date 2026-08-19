package main

import (
	"fmt"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var unclaimAssignee string

var unclaimCmd = &cobra.Command{
	Use:   "unclaim ID",
	Short: "Release a claim, returning the work item to available/ — or to pending/ if a gate is closed",
	Long: `Release a claim on a work item, returning it to available/.

WHERE IT LANDS DEPENDS ON THE ITEM'S GATES, not on this command's name. An item
whose ` + "`depends:`" + ` is unmet, or whose ` + "`snooze:`" + ` has not elapsed, goes to pending/
and the release says so, naming the gate. That is the same rule ` + "`mg edit`" + ` applies
when a dependency is added to an available item; the difference is that adding
one to a CLAIMED item deliberately does not demote it, so the release is the
moment the gate takes effect. Before mg-e7ff it did not: the item went to
available/ unconditionally, and an item with a deliberately unmet dependency was
advertised by pogo's stall-watch and priority-wake as ready to dispatch.

Unclaim is an explicit, targeted operation: it ignores the recorded claimant
PID. The recorded PID is unreliable because it may be the short-lived
'mg claim' subprocess rather than the owning agent — relying on it can
release claims held by live, healthy workers. To release a claim, the caller
must know the work item ID.

A CLAIM IS NOT A HOLD. A claimed item says someone took it and says nothing
about why; a sweeper collecting claims left by dead agents cannot tell a
deliberate hold from an abandoned one, and the usual test for "was anything
produced?" — a pushed branch, a merged commit — is blind to work whose only
artifact is the ticket body. On 2026-08-07 that released five COMPLETED triages
back into the dispatchable pool.

So an item that is waiting on someone should SAY SO, on the item:

    mg unclaim <id> --assignee=human

--assignee records who the item is waiting on and releases the claim, in that
order, so the item is never in available/ without the reason it is held. Pass it
whenever the claim was standing in for a gate; 'human', 'parked' and
'blocked:<agent>' are the values pogo's dispatcher gates on by default (as of
2026-08-07; the sentinel list is configurable), and any value at all is more
than a bare claim says. --assignee="" clears the field.

WHAT THIS COMMAND TELLS YOU. If the released item declares a remainder that
nothing tracks — the same condition 'mg done' refuses to complete on — and it
lands with no assignee, unclaim says so. That is a report and not a refusal: a
sweep of genuinely stranded claims has to stay one command that works. It is
also the discriminator a sweeper otherwise lacks, because an item's declaration
about its own output does not care whether that output was code.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		var opts []workitem.UnclaimOption
		// Keyed on whether the flag was PRESENT, not on whether it is empty, so
		// `--assignee=""` clears an assignee instead of silently doing nothing.
		if cmd.Flags().Changed("assignee") {
			opts = append(opts, workitem.WithUnclaimAssignee(unclaimAssignee))
		}

		res, err := workitem.Unclaim(root, args[0], opts...)
		if err != nil {
			return err
		}

		if res.PID > 0 {
			fmt.Printf("Unclaimed %s (was claimed by PID %d)\n", res.ID, res.PID)
		} else {
			fmt.Printf("Unclaimed %s\n", res.ID)
		}
		if res.Assignee != "" {
			fmt.Printf("Waiting on %s\n", res.Assignee)
		}

		// Where it actually went, and why, whenever that is not the
		// dispatchable pool. Said unprompted rather than left for `mg show`,
		// because the operator running this is usually releasing a claim in
		// order to hand the item on, and "it is in pending/ behind mg-12aa" is
		// the difference between that hand-off working and the item looking
		// lost. The reason is rendered by the same code `mg schedule` uses for
		// its held report, so the two cannot describe one gate differently.
		if res.Status == "pending" {
			reason := res.Held.Gates(time.Now().UTC())
			if reason == "" {
				// Defensive: gateOpen said closed and the describer found
				// nothing. Better a vague sentence than a dangling em-dash
				// that reads as a rendering bug rather than a placement.
				reason = "a gate on it is closed"
			}
			fmt.Printf("Held in pending/ — %s\n", reason)
			fmt.Printf("It is NOT dispatchable and will not be advertised as ready. `mg schedule` releases it when the gate opens.\n")
		}

		// Printed at the callsite, in front of the operator who just made the
		// decision, because that is the only moment the information is
		// actionable — mg-ed7b's sweeper was told nothing and reported its own
		// sweep clean. It names the item rather than saying "this item" so a
		// sweep releasing several claims in a loop produces a readable list.
		if res.RemainderOwed && res.Assignee == "" {
			fmt.Printf("Note: %s declares a remainder and nothing tracks it — the work it recommends is still owed — and it lands in available/ with no assignee, so nothing on it says who is waiting. If this claim was a HOLD rather than an abandoned one, record that now: mg edit %s --assignee=human\n", res.ID, res.ID)
		}
		return nil
	},
}

func init() {
	unclaimCmd.Flags().StringVar(&unclaimAssignee, "assignee", "", "who the item is waiting on; recorded on the item BEFORE the claim is released, so it never sits in available/ ungated")
}

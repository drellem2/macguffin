package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	unshelveClaim bool
	unshelvePID   int
	unshelveTag   string
)

var unshelveCmd = &cobra.Command{
	Use:   "unshelve [ID]",
	Short: "Restore a shelved work item and its dependents",
	Long: `Unshelve restores a previously shelved work item and the dependents
that were shelved WITH it — by the shelve cascade, or filed onto it while it
was shelved. Items with unmet dependencies are placed in pending/; others go
to available/. A dependent that was shelved ON ITS OWN stays shelved: lifting
the item does not lift a hold someone put on the dependent. Each one left is
named ("Left shelved <id>: ...") so you can see what did not move; restore it
with its own 'mg unshelve <id>'.

Use --tag to restore every shelved item with a given tag, mirroring
'mg shelve --tag'. Each tagged item comes back exactly as 'mg unshelve <id>'
would bring it back — so the dependents that were shelved along with it come
back too, EVEN WHEN THEY DO NOT CARRY THE TAG: the shelve cascade put them on
the shelf because of a tagged item, and they leave it the same way. An item
that cannot be restored is reported on stderr, the rest still come back, and
the command exits non-zero so a script cannot mistake a partial restore for a
whole one.
--tag cannot be combined with an ID or with --claim.

--claim takes back an item that was CLAIMED when it was shelved: instead of
available/, it moves the item straight into claimed/ as the caller's claim
(--pid, else $POGO_PID, else this process), in one rename, so no dispatcher
can claim it in between. It is a new claim, not the old owner's — shelve does
not record the PID it drops. It refuses unless the item's latest shelve record
says it was claimed, and an item with unmet dependencies still goes to
pending/, unclaimed — and that still exits 0, because the unshelve itself
succeeded: a script that needs the claim must check the output or 'mg show',
not the exit status. Dependents come back as they would without --claim.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		if cmd.Flags().Changed("pid") && !unshelveClaim {
			return mgerr.Usage("usage", "--pid only applies with --claim.", cmd.UseLine())
		}

		if cmd.Flags().Changed("tag") {
			if len(args) > 0 {
				return mgerr.Usage("usage",
					"give a work item ID or --tag, not both.",
					"Run 'mg unshelve <id>' or 'mg unshelve --tag=<tag>'.")
			}
			if unshelveClaim {
				// A claim is taken on one item the caller means to work; a bulk
				// one would hand them items they have not looked at.
				return mgerr.Usage("usage",
					"--claim takes back one named item and cannot be combined with --tag.",
					"Run 'mg unshelve <id> --claim' on the item you mean.")
			}
			if strings.TrimSpace(unshelveTag) == "" {
				return mgerr.Usage("usage", "--tag needs a tag.", "Run 'mg unshelve --tag=<tag>'.")
			}
			return unshelveByTag(cmd, root, unshelveTag)
		}

		if len(args) == 0 {
			return mgerr.Usage("usage",
				"requires a work item ID or --tag flag.",
				"Run 'mg unshelve <id>' or 'mg unshelve --tag=<tag>'.")
		}

		var res workitem.UnshelveResult
		if unshelveClaim {
			pid, err := resolveOwnerPID(unshelvePID)
			if err != nil {
				return err
			}
			res, err = workitem.UnshelveClaimReport(root, args[0], pid)
			if err != nil {
				return err
			}
		} else {
			res, err = workitem.UnshelveReport(root, args[0])
			if err != nil {
				return err
			}
		}

		for i, item := range res.Restored {
			if i == 0 && unshelveClaim {
				// Say where the named item landed: --claim may have put it in
				// pending/ rather than claimed/, and a caller who reads only
				// "Unshelved" would go on to work an item it does not hold.
				switch st, err := workitem.Status(root, item.ID); {
				case err != nil:
					fmt.Printf("Unshelved %s: %s (could not confirm the claim: %v)\n", item.ID, item.Title, err)
				case st == "claimed":
					fmt.Printf("Unshelved and claimed %s: %s\n", item.ID, item.Title)
				case st == "pending":
					fmt.Printf("Unshelved %s: %s (not claimed: it is pending — its gates are not open)\n", item.ID, item.Title)
				default:
					fmt.Printf("Unshelved %s: %s (not claimed: it is %s)\n", item.ID, item.Title, st)
				}
				continue
			}
			fmt.Printf("Unshelved %s: %s\n", item.ID, item.Title)
		}
		printLeftShelved(cmd.OutOrStdout(), res.Left)
		return nil
	},
}

// printLeftShelved names the shelved dependents an unshelve deliberately did
// not restore. It is not a failure — the hold is theirs — so it goes to stdout
// and the exit status stays 0; but it is printed, because an unshelve that
// names only what moved reads the same as one that moved everything.
func printLeftShelved(w io.Writer, left []workitem.LeftShelved) {
	for _, l := range left {
		fmt.Fprintf(w, "Left shelved %s: %s (%s) — 'mg unshelve %s' restores it\n",
			l.Item.ID, l.Item.Title, l.Reason, l.Item.ID)
	}
}

// unshelveByTag runs the bulk form and reports both halves of what it did.
func unshelveByTag(cmd *cobra.Command, root, tag string) error {
	res, skipped, err := workitem.UnshelveByTagReport(root, tag)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, item := range res.Restored {
		fmt.Fprintf(out, "Unshelved %s: %s\n", item.ID, item.Title)
	}
	printLeftShelved(out, res.Left)
	if len(skipped) > 0 {
		w := cmd.ErrOrStderr()
		fmt.Fprintf(w, "Could not unshelve %d tagged item(s):\n", len(skipped))
		for _, s := range skipped {
			fmt.Fprintf(w, "  %s: %s\n", s.Item.ID, s.Item.Title)
			fmt.Fprintf(w, "    %s\n", s.Reason)
		}
		fmt.Fprintln(w, "They are still shelved.")
		return mgerr.Conflict("unshelve_partial",
			fmt.Sprintf("%d of the items tagged %q could not be unshelved.", len(skipped), tag),
			"Retry each with 'mg unshelve <id>' to see why.")
	}
	return nil
}

func init() {
	unshelveCmd.Flags().StringVar(&unshelveTag, "tag", "", "unshelve all shelved items with this tag (and the dependents shelved with them)")
	unshelveCmd.Flags().BoolVar(&unshelveClaim, "claim", false, "take back an item that was claimed when shelved, as the caller's claim")
	unshelveCmd.Flags().IntVar(&unshelvePID, "pid", 0, "with --claim: PID of the owning process (default: $POGO_PID, else current process)")
}

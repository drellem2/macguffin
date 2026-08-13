package main

import (
	"encoding/json"
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	doneResult    string
	doneSuccessor string
)

var doneCmd = &cobra.Command{
	Use:   "done ID",
	Short: "Mark a claimed work item as done",
	Long: `Mark a claimed work item as done.

An item tagged 'declares-remainder' says its own output is a RECOMMENDATION — a
triage verdict, a design, a proposal — so at the moment it completes, the thing
it recommends is undone by construction. 'mg new' writes that tag by default for
the types and the triage carrier block where it holds (see 'mg new --help');
--no-declares-remainder files without it. Completing a declared item with
nothing tracking its recommendation discards it, so mg refuses:

    mg done <id>
    <id> declares a remainder ... and names no successor

File the item that carries it forward, then name it:

    mg done <id> --successor <id-of-the-item-that-tracks-it>

which records a "successor:<id>" tag on the item before completing it, so the
completed record names its own tracker for any later reader. The successor must
already exist and cannot be the item itself; a tag pointing at nothing tracks
nothing.

That is the ONLY thing mg can check about a successor. An id that exists but
names the WRONG item is a legal argument, and it gates a live item on a ticket
that will never carry the work. So mg prints what it linked:

    Successor mg-4b01 (available): build the thing the triage recommended

Read that line. It is printed whenever the completed item carries a successor:
tag — whether this run supplied it or an earlier edit did.

THE LINK IS WRITTEN ON BOTH ENDS. The completed item gets "successor:<sid>", and
<sid> gets "predecessor:<id>" back, so the chain is walkable in either direction
by 'mg show'. The reverse half is best-effort: it touches a second item that may
be claimed elsewhere, and a close that already satisfied its gate is never
turned into a refusal because that write failed. When it does fail it says so on
stderr, naming both ends — a reverse link that went missing quietly would be the
same defect at a smaller scale.

BOTH ENDS ARE QUERYABLE AS FIELDS, not only as tags. 'mg show --json' and
'mg list --json' carry 'successor', 'predecessor' and 'declares_remainder'
alongside 'tags', which makes the gate's own audit question answerable:

    mg list --all --json | jq -r 'select(.declares_remainder
      and (.status=="done" or .status=="archived")
      and (.successor|length)==0) | .id'

That is "every item that declared a remainder and completed without naming
anything to carry it". An empty result means the gate has not been bypassed —
but it asks whether a successor was NAMED, not whether one still EXISTS, so
'mg list --help' documents the companion query for links that rotted after they
were checked. Reading either empty result alone as clean is the same mistake
this trace exists to stop.

--result IS NOT LOST WHEN THIS COMMAND REFUSES. The result sidecar is written
before the guards run, beside the item where it currently sits, and travels
with the item to wherever it goes next. A refusal costs you a retry, never the
payload — so there is never a reason to invent a successor id to get a command
through. Re-run once the real successor exists; the result is carried into
done/ whether or not you pass --result again.

If a triage concludes that nothing is owed after all — or the default was
simply wrong for this item — the declaration is wrong and the right fix is to
retract it, not to work around it:

    mg edit <id> --rm-tags=declares-remainder

The refusal fires ONLY on the declaration, never on the item's type. An item
that does not carry the tag — every ordinary task — completes exactly as
before.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		var resultJSON json.RawMessage
		if doneResult != "" {
			raw := json.RawMessage(doneResult)
			if !json.Valid(raw) {
				return fmt.Errorf("invalid JSON for --result: %s", doneResult)
			}
			resultJSON = raw
		}

		root, err := resolveRoot()
		if err != nil {
			return err
		}

		var opts []workitem.DoneOption
		if doneSuccessor != "" {
			opts = append(opts, workitem.WithDoneSuccessor(doneSuccessor))
		}

		item, promoted, err := workitem.Done(root, id, resultJSON, opts...)
		if err != nil {
			return err
		}

		fmt.Printf("Done %s: %s\n", item.ID, item.Title)

		// Say what the successor IS, not merely that there was one. `--successor`
		// can only check that the id exists, so a real id naming the wrong item
		// used to complete with exit 0 and no output at all — see SuccessorRef
		// for why the two structural checks were measured and rejected. Printing
		// the title puts the mistake in front of the operator who made it, at the
		// callsite, while the intended item is still in mind.
		//
		// This reads the item's tags rather than the flag, so an item completing
		// on a successor: tag recorded earlier (by `mg edit --add-tags`, or a
		// previous run) is reported the same way. The flag is one route to the
		// tag, not the thing being reported.
		for _, s := range workitem.DescribeSuccessors(root, item) {
			switch {
			case s.Status == "":
				fmt.Printf("Successor %s: UNRESOLVED\n", s.ID)
			default:
				fmt.Printf("Successor %s (%s): %s\n", s.ID, s.Status, s.Title)
			}
		}

		if len(resultJSON) > 0 {
			fmt.Printf("Result written to %s.result.json\n", item.ID)
		}
		for _, p := range promoted {
			fmt.Printf("Promoted %s: %s (pending → available)\n", p.ID, p.Title)
		}
		return nil
	},
}

func init() {
	doneCmd.Flags().StringVar(&doneResult, "result", "", "result JSON to record as sidecar; merged into any existing result (these keys win) rather than replacing it")
	doneCmd.Flags().StringVar(&doneSuccessor, "successor", "", "id of the item that carries this item's recommendation forward; required to complete an item that declares a remainder")
}

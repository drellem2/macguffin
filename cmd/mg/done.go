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

An item filed with --declares-remainder says its own output is a
RECOMMENDATION — a triage verdict, a design, a proposal — so at the moment it
completes, the thing it recommends is undone by construction. Completing it
with nothing tracking that work discards the recommendation, so mg refuses:

    mg done <id>
    <id> declares a remainder ... and names no successor

File the item that carries it forward, then name it:

    mg done <id> --successor <id-of-the-item-that-tracks-it>

which records a "successor:<id>" tag on the item before completing it, so the
completed record names its own tracker for any later reader. The successor must
already exist and cannot be the item itself; a tag pointing at nothing tracks
nothing.

If a triage concludes that nothing is owed after all, the declaration was
wrong and the right fix is to retract it, not to work around it:

    mg edit <id> --rm-tags=declares-remainder

The refusal fires ONLY on the declaration. An item that never declared a
remainder — every ordinary task — completes exactly as before.`,
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

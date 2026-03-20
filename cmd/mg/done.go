package main

import (
	"encoding/json"
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var doneResult string

var doneCmd = &cobra.Command{
	Use:   "done ID",
	Short: "Mark a claimed work item as done",
	Args:  cobra.ExactArgs(1),
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

		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Done(root, id, resultJSON)
		if err != nil {
			return err
		}

		fmt.Printf("Done %s: %s\n", item.ID, item.Title)
		if len(resultJSON) > 0 {
			fmt.Printf("Result written to %s.result.json\n", item.ID)
		}
		return nil
	},
}

func init() {
	doneCmd.Flags().StringVar(&doneResult, "result", "", "result JSON to write as sidecar")
}

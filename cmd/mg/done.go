package main

import (
	"encoding/json"
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runDone(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("ID is required\nUsage: mg done ID [--result JSON]")
	}

	id := args[0]

	var resultJSON json.RawMessage
	for i := 1; i < len(args); i++ {
		if args[i] == "--result" && i+1 < len(args) {
			raw := json.RawMessage(args[i+1])
			if !json.Valid(raw) {
				return fmt.Errorf("invalid JSON for --result: %s", args[i+1])
			}
			resultJSON = raw
			break
		}
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
}

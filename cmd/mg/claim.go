package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runClaim(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("ID is required\nUsage: mg claim ID")
	}

	id := args[0]

	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}

	item, err := workitem.Claim(root, id)
	if err != nil {
		return err
	}

	fmt.Printf("Claimed %s: %s\n", item.ID, item.Title)
	return nil
}

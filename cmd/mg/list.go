package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runList() error {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}

	items, err := workitem.List(root)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		fmt.Println("No work items.")
		return nil
	}

	for _, item := range items {
		fmt.Printf("%-10s %-8s %s\n", item.ID, item.Type, item.Title)
	}

	return nil
}

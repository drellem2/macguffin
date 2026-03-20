package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runList(args []string) error {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}

	// Parse --status flag
	var status string
	for i, arg := range args {
		if arg == "--status" && i+1 < len(args) {
			status = args[i+1]
			break
		}
		if strings.HasPrefix(arg, "--status=") {
			status = strings.TrimPrefix(arg, "--status=")
			break
		}
	}

	if status != "" {
		// List items for a specific status
		items, err := workitem.ListByStatus(root, status)
		if err != nil {
			return err
		}

		if len(items) == 0 {
			fmt.Printf("No %s work items.\n", status)
			return nil
		}

		for _, item := range items {
			fmt.Printf("%-10s %-8s %s\n", item.ID, item.Type, item.Title)
		}
		return nil
	}

	// No --status: show all items grouped by state
	grouped, err := workitem.ListAll(root)
	if err != nil {
		return err
	}

	if len(grouped) == 0 {
		fmt.Println("No work items.")
		return nil
	}

	order := []string{"available", "claimed", "done"}
	for _, s := range order {
		items := grouped[s]
		if len(items) == 0 {
			continue
		}
		fmt.Printf("%s:\n", s)
		for _, item := range items {
			fmt.Printf("  %-10s %-8s %s\n", item.ID, item.Type, item.Title)
		}
	}

	return nil
}

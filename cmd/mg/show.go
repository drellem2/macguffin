package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runShow(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("id is required\nUsage: mg show ID")
	}

	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}

	item, err := workitem.Read(root, args[0])
	if err != nil {
		return err
	}

	fmt.Printf("%-10s %s\n", "ID:", item.ID)
	fmt.Printf("%-10s %s\n", "Type:", item.Type)
	fmt.Printf("%-10s %s\n", "Created:", item.Created.Format("2006-01-02 15:04:05Z"))
	fmt.Printf("%-10s %s\n", "Creator:", item.Creator)
	fmt.Printf("%-10s %s\n", "Title:", item.Title)

	if item.Body != "" {
		fmt.Printf("\n%s", item.Body)
	}

	return nil
}

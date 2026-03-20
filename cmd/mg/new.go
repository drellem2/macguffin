package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
)

func runNew(args []string) error {
	typ := "task" // default type
	var title string

	// Parse --type=X flag and collect remaining args as title
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--type=") {
			typ = strings.TrimPrefix(arg, "--type=")
		} else if arg == "--type" && i+1 < len(args) {
			i++
			typ = args[i]
		} else {
			rest = append(rest, arg)
		}
	}

	title = strings.Join(rest, " ")
	if title == "" {
		return fmt.Errorf("title is required\nUsage: mg new [--type=TYPE] TITLE")
	}

	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}

	item, err := workitem.Create(root, typ, title)
	if err != nil {
		return err
	}

	fmt.Printf("Created %s: %s\n", item.ID, item.Title)
	return nil
}

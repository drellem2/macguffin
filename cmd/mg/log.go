package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
)

func runLog(args []string) error {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}
	return workspace.Log(root, args)
}

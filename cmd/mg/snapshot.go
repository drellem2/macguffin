package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
)

func runSnapshot() error {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return err
	}
	return workspace.Snapshot(root)
}

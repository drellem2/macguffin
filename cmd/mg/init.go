package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var initGit bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize ~/.macguffin directory tree",
	Long:  "Initialize ~/.macguffin directory tree. Use --git to enable git snapshots.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}
		if err := workspace.Init(root); err != nil {
			return err
		}
		if initGit {
			return workspace.InitGit(root)
		}
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initGit, "git", false, "enable git snapshots")
}

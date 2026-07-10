package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	initGit    bool
	initPrefix string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the macguffin directory tree",
	Long: `Initialize the macguffin directory tree.

The workspace is resolved as --root > $MG_ROOT > $HOME/.macguffin; the working
directory is never consulted. Use --git to enable git snapshots. Use --prefix to
set a custom work item ID prefix.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		if err := workspace.Init(root); err != nil {
			return err
		}
		if cmd.Flags().Changed("prefix") {
			if err := workspace.WriteConfig(root, "prefix", initPrefix); err != nil {
				return err
			}
		}
		if initGit {
			return workspace.InitGit(root)
		}
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initGit, "git", false, "enable git snapshots")
	initCmd.Flags().StringVar(&initPrefix, "prefix", workspace.DefaultPrefix, "work item ID prefix (e.g. 'po-' gives po-a3f)")
}

package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var logCmd = &cobra.Command{
	Use:   "log [args]",
	Short: "Show git snapshot history (passes args to git log)",
	Long: `Show the workspace git snapshot history.

Any extra arguments are passed through verbatim to 'git log' (e.g.
'mg log --oneline -n 5' or 'mg log -- path'). Flag parsing is disabled so those
arguments reach git unchanged; 'mg log --help' / '-h' still shows this help
instead of being forwarded.`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// DisableFlagParsing forwards every arg (including flags) to git log,
		// so cobra never intercepts --help/-h — guard it here before exec.
		// See drellem2/pogo#54.
		if helpRequested(args) {
			return cmd.Help()
		}

		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}
		return workspace.Log(root, args)
	},
}

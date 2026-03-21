package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	newType    string
	newDepends string
)

var newCmd = &cobra.Command{
	Use:   "new [--type=TYPE] [--depends=ID,...] TITLE...",
	Short: "Create a new work item",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")
		if title == "" {
			return fmt.Errorf("title is required")
		}

		var deps []string
		if newDepends != "" {
			for _, d := range strings.Split(newDepends, ",") {
				d = strings.TrimSpace(d)
				if d != "" {
					deps = append(deps, d)
				}
			}
		}

		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		var opts []workitem.CreateOption
		if repo := detectRepo(); repo != "" {
			opts = append(opts, workitem.WithRepo(repo))
		}

		item, err := workitem.Create(root, newType, title, deps, opts...)
		if err != nil {
			return err
		}

		fmt.Printf("Created %s: %s\n", item.ID, item.Title)
		if len(deps) > 0 {
			fmt.Printf("  depends: %s (pending)\n", strings.Join(deps, ", "))
		}
		return nil
	},
}

func init() {
	newCmd.Flags().StringVar(&newType, "type", "task", "work item type")
	newCmd.Flags().StringVar(&newDepends, "depends", "", "comma-separated list of dependency IDs")
}

// detectRepo returns the git toplevel of the current working directory, or ""
// if not inside a git repo.
func detectRepo() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

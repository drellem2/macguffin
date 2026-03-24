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
	newType     string
	newDepends  string
	newAssignee string
	newBranch   string
	newPriority string
	newTags     string
	newTitle    string
	newBody     string
)

var newCmd = &cobra.Command{
	Use:     "new [--title=TITLE] [--body=BODY] [flags] [TITLE...]",
	Aliases: []string{"create"},
	Short:   "Create a new work item",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		title := newTitle
		if title == "" {
			title = strings.Join(args, " ")
		} else if len(args) > 0 {
			return fmt.Errorf("cannot use both --title flag and positional arguments")
		}
		if title == "" {
			return fmt.Errorf("title is required (use --title flag or positional arguments)")
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

		prefix := workspace.Prefix(root)

		var opts []workitem.CreateOption
		if repo := detectRepo(); repo != "" {
			opts = append(opts, workitem.WithRepo(repo))
		}
		if newAssignee != "" {
			opts = append(opts, workitem.WithAssignee(newAssignee))
		}
		if newBranch != "" {
			opts = append(opts, workitem.WithBranch(newBranch))
		}
		{
			priority := newPriority
			if priority == "" {
				priority = "medium"
			}
			switch priority {
			case "low", "medium", "high":
				opts = append(opts, workitem.WithPriority(priority))
			default:
				return fmt.Errorf("invalid priority %q: must be low, medium, or high", priority)
			}
		}
		if newTags != "" {
			var tags []string
			for _, t := range strings.Split(newTags, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
			opts = append(opts, workitem.WithTags(tags))
		}
		if newBody != "" {
			opts = append(opts, workitem.WithBody(newBody))
		}

		item, err := workitem.Create(root, prefix, newType, title, deps, opts...)
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
	newCmd.Flags().StringVar(&newAssignee, "assignee", "", "person to assign this item to")
	newCmd.Flags().StringVar(&newBranch, "branch", "", "branch name for this work item")
	newCmd.Flags().StringVar(&newPriority, "priority", "", "priority level: low, medium, high (default: medium)")
	newCmd.Flags().StringVar(&newTags, "tag", "", "comma-separated list of tags")
	newCmd.Flags().StringVar(&newTitle, "title", "", "work item title (alternative to positional args)")
	newCmd.Flags().StringVar(&newBody, "body", "", "work item body (markdown)")
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

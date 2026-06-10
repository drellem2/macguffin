package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	editTitle      string
	editBody       string
	editType       string
	editRepo       string
	editDepends    []string
	editAddDepends []string
	editRmDepends  []string
	editTags       []string
	editAddTags    []string
	editRmTags     []string
	editAssignee   string
	editPriority   string
	editBudget     int
)

var editCmd = &cobra.Command{
	Use:     "edit ID [flags]",
	Aliases: []string{"update"},
	Short:   "Update fields on an existing work item",
	Long: `Update fields on an existing work item.

Use --title, --body, --type, --repo, --assignee, --priority to replace fields directly.
Use --depends to replace all dependencies, or --add-depends / --rm-depends for incremental changes.
Use --tags to replace all tags, or --add-tags / --rm-tags for incremental changes.

The --assignee flag names the agent that owns triage and routing for the
item, not the agent that runs the work. Substantive work is performed by
an ephemeral polecat (named after the work-item ID at spawn time)
regardless of the assignee. Polecats are never named in advance, so they
cannot be assigned ahead of time; the assignee is the durable owner who
decides whether to dispatch the work, hold it, or close it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		fields := workitem.UpdateField{}
		changed := false

		if cmd.Flags().Changed("title") {
			fields.Title = &editTitle
			changed = true
		}
		if cmd.Flags().Changed("body") {
			fields.Body = &editBody
			changed = true
		}
		if cmd.Flags().Changed("type") {
			fields.Type = &editType
			changed = true
		}
		if cmd.Flags().Changed("repo") {
			fields.Repo = &editRepo
			changed = true
		}
		if cmd.Flags().Changed("depends") || cmd.Flags().Changed("depend") {
			fields.Depends = normSlice(editDepends)
			changed = true
		}
		if cmd.Flags().Changed("add-depends") {
			fields.AddDepends = normSlice(editAddDepends)
			changed = true
		}
		if cmd.Flags().Changed("rm-depends") {
			fields.RmDepends = normSlice(editRmDepends)
			changed = true
		}
		if cmd.Flags().Changed("tags") || cmd.Flags().Changed("tag") {
			fields.Tags = normSlice(editTags)
			changed = true
		}
		if cmd.Flags().Changed("add-tags") {
			fields.AddTags = normSlice(editAddTags)
			changed = true
		}
		if cmd.Flags().Changed("rm-tags") {
			fields.RmTags = normSlice(editRmTags)
			changed = true
		}
		if cmd.Flags().Changed("assignee") {
			fields.Assignee = &editAssignee
			changed = true
		}
		if cmd.Flags().Changed("priority") {
			switch editPriority {
			case "low", "medium", "high", "":
				fields.Priority = &editPriority
			default:
				return fmt.Errorf("invalid priority %q: must be low, medium, or high", editPriority)
			}
			changed = true
		}
		if cmd.Flags().Changed("budget") {
			if editBudget < 0 {
				return fmt.Errorf("invalid budget %d: must be non-negative (use 0 to unset)", editBudget)
			}
			fields.Budget = &editBudget
			changed = true
		}

		if !changed {
			return fmt.Errorf("no fields specified; use --title, --body, --type, --assignee, --depends, --tags, etc.")
		}

		item, err := workitem.Update(root, args[0], fields)
		if err != nil {
			return err
		}

		fmt.Printf("Updated %s: %s\n", item.ID, item.Title)
		return nil
	},
}

func init() {
	editCmd.Flags().StringVar(&editTitle, "title", "", "new title")
	editCmd.Flags().StringVar(&editBody, "body", "", "new body (markdown)")
	editCmd.Flags().StringVar(&editType, "type", "", "new type")
	editCmd.Flags().StringVar(&editRepo, "repo", "", "new repo path")
	stringSliceVarWithAlias(editCmd.Flags(), &editDepends, "depends", "depend", "replace all dependencies (comma-separated or repeated)")
	editCmd.Flags().StringSliceVar(&editAddDepends, "add-depends", nil, "add dependencies (comma-separated or repeated)")
	editCmd.Flags().StringSliceVar(&editRmDepends, "rm-depends", nil, "remove dependencies (comma-separated or repeated)")
	stringSliceVarWithAlias(editCmd.Flags(), &editTags, "tags", "tag", "replace all tags (comma-separated or repeated)")
	editCmd.Flags().StringSliceVar(&editAddTags, "add-tags", nil, "add tags (comma-separated or repeated)")
	editCmd.Flags().StringSliceVar(&editRmTags, "rm-tags", nil, "remove tags (comma-separated or repeated)")
	editCmd.Flags().StringVar(&editAssignee, "assignee", "", "person to assign this item to")
	editCmd.Flags().StringVar(&editPriority, "priority", "", "priority level: low, medium, high")
	editCmd.Flags().IntVar(&editBudget, "budget", 0, "estimated token budget (integer; --budget=0 unsets)")
}

// normSlice trims whitespace and drops empty entries from a parsed string-slice
// flag value, returning a non-nil slice. pflag's StringSlice already splits on
// commas and accumulates across repeated flag uses; this just normalizes the
// resulting elements (e.g. "foo, bar" -> ["foo","bar"]).
func normSlice(items []string) []string {
	result := []string{}
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it != "" {
			result = append(result, it)
		}
	}
	return result
}

// stringSliceVarWithAlias registers a repeatable, comma-splitting string-slice
// flag under name, plus alias pointing at the SAME underlying value. Sharing the
// value (rather than a second StringSliceVar binding) means repeated and mixed
// uses accumulate correctly — e.g. "--tag=foo --tags=bar" yields ["foo","bar"]
// instead of silently keeping only the last one.
func stringSliceVarWithAlias(fs *pflag.FlagSet, p *[]string, name, alias, usage string) {
	fs.StringSliceVar(p, name, nil, usage)
	fs.Var(fs.Lookup(name).Value, alias, "alias for --"+name)
}

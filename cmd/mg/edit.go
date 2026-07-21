package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	editTitle      string
	editBody       string
	editBodyFile   string
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

Use --title, --body-file/--body, --type, --repo, --assignee, --priority to
replace fields directly.

For the replacement body, reach for --body-file first. It reads the body
verbatim ("-" for stdin), with no shell in the path at all. The canonical form
is a QUOTED heredoc:

  mg edit mg-1234 --body-file - <<'EOF'
  body text with backticks and $VARS and $(cmd), all literal
  EOF

The quotes around 'EOF' are the entire property. <<'EOF' passes the bytes
through untouched; an unquoted <<EOF expands backticks, $VAR and $(cmd)
exactly as --body="..." does, silently reintroducing the bug. A file works the
same way: --body-file ./body.md.

--body is the inline-only shortcut, and stays correct for the many bodies that
carry no shell metacharacters. When a body does carry them, the shell expands
them before mg ever runs, so those terms are silently gone from the stored
body.

The two flags are mutually exclusive. A --body-file that cannot be read is an
error, never an empty body; like --body="", a --body-file naming an empty file
clears the body.

Use --depends to replace all dependencies, or --add-depends / --rm-depends for incremental changes.
Use --tags to replace all tags, or --add-tags / --rm-tags for incremental changes.

The --assignee flag names the agent that owns triage and routing for the
item, not the agent that runs the work. Substantive work is performed by
an ephemeral polecat (named after the work-item ID at spawn time)
regardless of the assignee. Polecats are never named in advance, so they
cannot be assigned ahead of time; the assignee is the durable owner who
decides whether to dispatch the work, hold it, or close it.

Workflow tags stay welded to the body across edits, and the check runs on the
resulting item rather than on the flags: adding a workflow tag (e.g.
--add-tags=gh-issue) to an item whose body does not declare that workflow is
refused, and so is a --body rewrite that drops the carrier block off an item
still carrying the tag. Advancing 'stage:' by rewriting the body is unaffected —
keep the carrier block at the top. See 'mg new --help' for the block's shape.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		fields := workitem.UpdateField{}
		changed := false

		if cmd.Flags().Changed("title") {
			fields.Title = &editTitle
			changed = true
		}
		body, bodySet, err := bodyFromFlags(cmd, editBody, editBodyFile)
		if err != nil {
			return err
		}
		if bodySet {
			fields.Body = &body
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
	editCmd.Flags().StringVar(&editBodyFile, "body-file", "", "read the new body verbatim from a file (\"-\" for stdin); mutually exclusive with --body")
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

package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	editTitle          string
	editBody           string
	editBodyFile       string
	editAppendBody     string
	editAppendBodyFile string
	editIfUnchanged    string

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

ADDING TO A BODY? USE --append-body-file, NOT --body-file.

  mg edit mg-1234 --append-body-file - <<'EOF'
  ## 2026-07-29 04:20 — reconciliation
  ...
  EOF

--body-file replaces the WHOLE body with text you composed from a read that
happened seconds or minutes ago. Anything another writer stored in between is
destroyed, silently, with exit 0 — three agents did exactly this to each other
in two hours (mg-f326). --append-body-file composes against the body on disk at
write time, so it cannot destroy a section it never saw. It is the right shape
for the dated sections these bodies actually accumulate, and it needs no
coordination with anyone.

The appended text is taken verbatim; it is joined to the existing body by
exactly one blank line, so a leading "## heading" renders as a heading.

WHEN A FULL REWRITE REALLY IS THE SHAPE, NAME THE VERSION YOU READ.

  HASH=$(mg show mg-1234 --body-hash)
  # ... compose the new body ...
  mg edit mg-1234 --if-unchanged="$HASH" --body-file ./new-body.md

--if-unchanged refuses the write (exit 4) if the stored body no longer hashes to
that value, instead of overwriting a change you never saw. It is opt-in: without
it, --body-file behaves exactly as it always has. The hash covers the stored
body INCLUDING its "# Title" heading, so it also catches a competing --title.
A prefix of 8 or more characters is accepted.

--title alone is body-safe: it rewrites the "# heading" line in place and leaves
every other byte of the body untouched. It is the one edit two agents can make
to a live item without racing each other's prose.

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
keep the carrier block at the top. See 'mg new --help' for the block's shape.

--append-body-file is exempt from that refusal on an item that ALREADY carried
the tag. An append lands below the prose, so it cannot author the body's leading
block: it can only inherit a missing carrier block, never create one. Without
the exemption, an item filed before the carrier block was a convention could not
be corrected at all except by the full-body rewrite --append-body-file exists to
avoid. The append prints a note on stderr saying the item still routes to the
default build template; a carrier block IN the appended text is still refused
(it would read as marked while routing as unmarked).`,
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
		appendBody, appendSet, err := appendBodyFromFlags(cmd, editAppendBody, editAppendBodyFile)
		if err != nil {
			return err
		}
		// Replace-and-append in one invocation is a caller who means one of the
		// two and will not find out which one they got until they re-read.
		// Refuse rather than pick.
		if bodySet && appendSet {
			return mgerr.Usage("mutually_exclusive_flags",
				"cannot replace and append the body in one edit",
				"replace it with --body-file, or add to it with --append-body-file — not both")
		}
		if bodySet {
			fields.Body = &body
			changed = true
		}
		if appendSet {
			fields.AppendBody = &appendBody
			changed = true
		}
		if cmd.Flags().Changed("if-unchanged") {
			// A precondition on nothing is a caller who believes they are
			// guarded and is not writing anything; and if it were allowed to
			// stand alone it would report "no fields specified" while looking
			// like a successful check. Carried as a field, not counted as one.
			fields.IfUnchanged = editIfUnchanged
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

		item, change, err := workitem.UpdateWithBodyChange(root, args[0], fields)
		if err != nil {
			return err
		}

		// Report the body's size delta on the write itself. In mg-f326's first
		// incident a body went 227 → 113 lines and the writer was told only
		// "Updated"; the loss surfaced seven minutes later, by luck, when
		// someone re-read and grepped for markers they expected. A number here
		// costs nothing and puts the evidence in front of the one agent
		// guaranteed to be looking.
		note := ""
		if change != nil && change.Changed {
			note = fmt.Sprintf(" (body %d → %d lines)", change.LinesBefore, change.LinesAfter)
		}
		fmt.Printf("Updated %s: %s%s\n", item.ID, item.Title, note)

		// The write succeeded on an item whose workflow tag and body still
		// disagree — only an append can get here (mg-d878). Saying so is the
		// whole difference between grandfathering and forgetting: the item is
		// still one dispatch routes to the default build template, and the
		// agent that just appended to it is the one agent guaranteed to be
		// looking. stderr, so it cannot corrupt anything parsing stdout.
		if tag := workitem.MissingWorkflowCarrier(item); tag != "" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"note: %s is tagged %s but its body does not lead with the carrier block, "+
					"so dispatch will route it to the DEFAULT BUILD template. The append was "+
					"allowed because it cannot fix that either way; mg will not invent the "+
					"stage/gh values. See 'mg new --help' for the block's shape.\n",
				item.ID, tag)
		}
		return nil
	},
}

func init() {
	editCmd.Flags().StringVar(&editTitle, "title", "", "new title")
	editCmd.Flags().StringVar(&editBody, "body", "", "new body (markdown)")
	editCmd.Flags().StringVar(&editBodyFile, "body-file", "", "read the new body verbatim from a file (\"-\" for stdin); mutually exclusive with --body")
	editCmd.Flags().StringVar(&editAppendBody, "append-body", "", "append text to the existing body instead of replacing it")
	editCmd.Flags().StringVar(&editAppendBodyFile, "append-body-file", "", "append the verbatim contents of a file (\"-\" for stdin) to the existing body; cannot clobber a concurrent write")
	editCmd.Flags().StringVar(&editIfUnchanged, "if-unchanged", "", "refuse the edit unless the stored body still hashes to this (from 'mg show ID --body-hash'); a prefix of 8+ chars is accepted")
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

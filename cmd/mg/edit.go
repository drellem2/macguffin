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
	editIfAssignee     string

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

THE SAME GUARD, ON THE DISPATCH GATE.

  mg edit mg-1234 --if-assignee=blocked:pm-pogo --append-body-file - <<'EOF'
  ## note written on the assumption that this item is still held
  EOF

--if-assignee refuses the edit (exit 4) unless the stored assignee is EXACTLY
that value. --if-unchanged guards the body; this guards the one field that
decides whether the item is dispatched at all, and until mg-5eee it had no
guard. On 2026-08-12 the mayor gated mg-27d4 with 'blocked:pm-pogo', pm-pogo set
it back to 'mayor' a minute later, and the mayor's next four writes each printed
'Updated mg-27d4' while the hold was gone. Nothing there was a bug: both agents
wrote what they meant and events.jsonl names both. What was missing was any way
for the holder to SAY the hold was still in place, so a hold survived only as
long as nobody disagreed with it — silently, in the holder's direction.

--if-assignee="" requires the field to be unset, which makes the flag a
compare-and-swap: '--if-assignee="" --assignee=blocked:me' takes a gate only if
nobody else already holds one. The refusal names both values and the file's mtime;
'mg event list --type=work.edited' names the actor who moved it.

'Updated <id>' IS NOT EVIDENCE A FIELD YOU DID NOT NAME STILL SAYS WHAT IT SAID.
mg cannot warn about that on its own — inside one invocation there is a read and
a write microseconds apart and no record of what the CALLER last saw. Only the
caller knows the value they are relying on, which is why this is a precondition
you pass rather than a check mg performs.

A MISSPELLED GATE IS AN OPEN GATE.

'human', 'parked' and 'blocked:<agent>' are what pogo's dispatcher gates on. A
near-miss of those spellings is refused (exit 2) instead of stored: --assignee=
'blocekd:pm-pogo', 'blocked-pm-pogo', 'blocked:', 'Blocked:pm-pogo' and 'Human'
each used to exit 0 and store a value that gates nothing, which reads to a human
exactly like a hold and to the dispatcher like an ordinary name.

mg does NOT know the set of legitimate assignees, so it cannot refuse
"unrecognised" — any agent name is valid and passes. Only attempts at the gate
that missed are refused, and a typo far enough from the three spellings (say
'parkd') is indistinguishable from a name and still gets through. There is no
--force: the way past the guard is to spell the gate correctly.

THE TITLE LIVES IN THE BODY. EDITING ONE EDITS THE OTHER.

There is no 'title:' in an item's frontmatter. The title is read back, on every
read, from the body's FIRST line beginning with "# " — that heading IS the
title, and it is the only place the title is stored. Two consequences, and both
of them bit (mg-bac6):

  * A --body/--body-file whose first heading differs from the current title
    RENAMES the item. That is refused (exit 4) unless you also pass --title, so
    a field you did not name cannot move without you.
  * --title rewrites that first heading in place. The heading is not preserved
    alongside the new title; it is replaced, because two headings claiming to be
    the item's name is the corrupted state — a reader only ever sees the first.

So when both are passed, --title wins and the body's leading heading is
rewritten to match it. When only a body is passed, the body's heading would win,
and that is exactly the case mg refuses instead of performing silently.

The direction-agnostic procedure, which does not care which field wins because
it names both:

  mg edit mg-1234 --title="the title I mean" --body-file ./body.md
  # ...where body.md has NO leading "# " heading at all.

mg writes the heading from --title, and writes exactly one. A body with no
leading heading is the only shape that cannot surprise you: there is nothing for
the two rules to disagree about. Headings BELOW the first are ordinary content
and are left alone — mg counts them on stderr and never rewrites your prose to
satisfy a count. A blockquoted "> # heading" is not a heading to either rule, so
it can neither become nor displace the title.

AND IF IT GOES WRONG ANYWAY, THE PRIOR BODY IS STILL THERE.

  mg restore-body mg-1234 --list
  mg restore-body mg-1234

Every --body/--body-file write saves the body it is about to destroy first, to
~/.macguffin/work/.bodybak/<id>/, ten deep. That is not the same guarantee
--if-unchanged makes: the guard proves nobody ELSE wrote in the window, and says
nothing about whether YOUR OWN READ succeeded. mg-9fc8 is the incident — a
'mg show <id> --body' with no such flag wrote its usage error into the file, an
unconditional 'mg edit' on the next line sent it, and the guard passed. The
backup assumes every other defence has already failed and predicts nothing.

Appends and --title are not backed up because they do not overwrite anything.

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

EVERY EDIT IS LOGGED, INCLUDING METADATA-ONLY ONES.

Any edit that actually moves the stored item writes a 'work.edited' event to
events.jsonl ('mg event list --type=work.edited'). A metadata-only edit records
mode=metadata, a 'fields' list naming what moved, and a before/after pair per
field — so an --assignee change is greppable with its old and new values. That
matters most for --assignee, which is the dispatch gate: 'human' and 'parked'
suppress both stall-watch and dispatch, so the field deciding whether an item is
ever worked on should not be changeable without a record.

The event's 'actor' is whoever RAN the command (MG_ACTOR, else POGO_AGENT_NAME,
else the OS user), not the item's assignee. Setting an edit to no-op values
writes nothing at all.

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
		if cmd.Flags().Changed("if-assignee") {
			// Carried, not counted, exactly like --if-unchanged: a precondition
			// on its own writes nothing, and letting it stand alone would report
			// "no fields specified" while looking like a check that passed.
			fields.IfAssignee = &editIfAssignee
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

		// The title as READ BACK from the body just written, and its transition
		// when it moved. This line used to print the in-memory title, which in
		// the retitle case was the value that had just been destroyed — the
		// success line asserted a title that was already false as it printed
		// (mg-bac6). An unrequested move is refused outright now, so reaching
		// this with a transition means the caller passed --title and is being
		// shown it took effect.
		shown := item.Title
		if change.TitleMoved() {
			shown = fmt.Sprintf("%s (title was %q)", change.TitleAfter, change.TitleBefore)
		}
		fmt.Printf("Updated %s: %s%s\n", item.ID, shown, note)

		// mg no longer stacks a heading of its own, so any extra H1 is the
		// caller's own prose — but a body that came in with a near-duplicate of
		// its title still reads as two titles to anyone skimming it, and 196
		// stored bodies are in exactly that state. Counted on stderr, not
		// refused: multi-section bodies are legitimate, and the caller who just
		// wrote it is the one agent guaranteed to be looking.
		if change.ExtraHeadings > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"note: %s's body has %d '# ' heading(s) below the title heading. "+
					"The title is read back from the FIRST one only (%q). If one of the "+
					"others is a stale copy of the title, remove it — nothing else will.\n",
				item.ID, change.ExtraHeadings, change.TitleAfter)
		}

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
	editCmd.Flags().StringVar(&editIfAssignee, "if-assignee", "", "refuse the edit (exit 4) unless the stored assignee is exactly this; --if-assignee=\"\" requires it to be unset")
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

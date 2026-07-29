package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	newType     string
	newDepends  []string
	newAssignee string
	newBranch   string
	newPriority string
	newTags     []string
	newTitle    string
	newBody     string
	newBodyFile string
	newRepo     string
	newNoRepo   bool
	newBudget   int
	newPrefix   string

	// newDeclaresRemainder marks the item as one whose OUTPUT IS A
	// RECOMMENDATION, so `mg done` refuses it until something tracks what it
	// recommends. See internal/workitem/remainder.go for why this is declared
	// rather than inferred from type or stage.
	newDeclaresRemainder bool
)

// validPrefixRe matches lowercase alphanumeric + hyphens, ending with a single
// hyphen (e.g. "dr-", "po-", "team1-"). The trailing hyphen separates the
// prefix from the generated hex suffix in the final ID.
var validPrefixRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*-$`)

var newCmd = &cobra.Command{
	Use:     "new [--title=TITLE] [--body=BODY] [flags] [TITLE...]",
	Aliases: []string{"create"},
	Short:   "Create a new work item",
	Long: `Create a new work item.

Reach for --body-file first. It reads the body verbatim ("-" for stdin), with
no shell in the path at all. The canonical form is a QUOTED heredoc:

  mg new --title="t" --body-file - <<'EOF'
  body text with backticks and $VARS and $(cmd), all literal
  EOF

The quotes around 'EOF' are the entire property. <<'EOF' passes the bytes
through untouched; an unquoted <<EOF expands backticks, $VAR and $(cmd)
exactly as --body="..." does, silently reintroducing the bug. A file works the
same way: --body-file ./body.md.

--body is the inline-only shortcut, and stays correct for the many bodies that
carry no shell metacharacters. When a body does carry them, the shell expands
them before mg ever runs, so those terms land in the item silently gone.

The two flags are mutually exclusive. A --body-file that cannot be read is an
error, never an empty body.

The --assignee flag names the agent that owns triage and routing for the
item, not the agent that runs the work. Substantive work is performed by
an ephemeral polecat (named after the work-item ID at spawn time)
regardless of the assignee. Polecats are never named in advance, so they
cannot be assigned ahead of time; the assignee is the durable owner who
decides whether to dispatch the work, hold it, or close it.

Workflow tags are welded to the body. A tag that names a workflow (currently
just 'gh-issue') asserts the same fact as the body's leading 'workflow:' line,
but only the body line is what dispatch routes on. So the body is the single
source of truth: declaring the workflow in the body adds the tag for you, while
passing the tag without declaring it in the body is refused rather than filed as
an item that would silently route to the default build template. mg will not
fill in the rest of the carrier block ('stage:', 'gh:') — it cannot know the
stage or the issue, and guessing a 'gh:' ref would aim a builder at the wrong
issue. The block belongs at the top of the body, ahead of any prose:

    workflow: gh-issue
    stage: triage
    gh: <owner>/<repo>#<n>

--declares-remainder marks an item whose OUTPUT IS A RECOMMENDATION: a triage
verdict, a design, a proposal. Its build is undone by construction at the
moment it completes, so 'mg done' refuses it until --successor names the item
carrying it forward (and 'mg archive' refuses it as a backstop). Declare it,
rather than letting mg infer it from a type or a workflow stage — those are
proxies that miss triages and over-fire on paused items. An item that does not
declare a remainder is never touched by the guard.

The --prefix flag overrides the work item ID prefix for this call only.
It is not persisted to workspace config (see 'mg init --prefix=...' for
the workspace-wide default). Useful for filing items under a different
namespace (e.g. 'dr-' for director-issued items) without changing the
workspace default. The prefix must be lowercase alphanumeric with
optional internal hyphens, and must end in a hyphen.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		title := newTitle
		if title == "" {
			title = strings.Join(args, " ")
		} else if len(args) > 0 {
			return mgerr.Usage("mutually_exclusive_flags", "cannot use both --title flag and positional arguments", "")
		}
		if title == "" {
			return mgerr.Usage("missing_required", "title is required (use --title flag or positional arguments)", "")
		}

		deps := normSlice(newDepends)

		root, err := resolveRoot()
		if err != nil {
			return err
		}

		prefix := workspace.Prefix(root)
		if cmd.Flags().Changed("prefix") {
			if !validPrefixRe.MatchString(newPrefix) {
				return mgerr.Usage("invalid_value", fmt.Sprintf("invalid prefix %q: must be lowercase alphanumeric (with internal hyphens) ending in a hyphen, e.g. 'dr-'", newPrefix), "")
			}
			prefix = newPrefix
		}

		var opts []workitem.CreateOption
		if newNoRepo && cmd.Flags().Changed("repo") && newRepo != "" {
			return mgerr.Usage("mutually_exclusive_flags", "cannot use --no-repo with a non-empty --repo", "")
		}
		var repo string
		switch {
		case newNoRepo:
			repo = ""
		case cmd.Flags().Changed("repo"):
			repo = newRepo
		default:
			repo = autoDetectRepo()
		}
		if repo != "" {
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
				return mgerr.Usage("invalid_value", fmt.Sprintf("invalid priority %q: must be low, medium, or high", priority), "")
			}
		}
		tags := normSlice(newTags)
		if newDeclaresRemainder {
			// mg writes the marker itself, in its canonical spelling, rather
			// than asking the filer to remember a tag string — a declaration
			// the tool writes cannot be forgotten or misspelled the way a
			// convention a prompt merely asks for can.
			tags = addUniqueTag(tags, workitem.DeclaresRemainderTag)
		}
		if len(tags) > 0 {
			opts = append(opts, workitem.WithTags(tags))
		}
		body, _, err := bodyFromFlags(cmd, newBody, newBodyFile)
		if err != nil {
			return err
		}
		if body != "" {
			opts = append(opts, workitem.WithBody(body))
		}
		if cmd.Flags().Changed("budget") {
			if newBudget < 0 {
				return mgerr.Usage("invalid_value", fmt.Sprintf("invalid budget %d: must be non-negative", newBudget), "")
			}
			if newBudget > 0 {
				opts = append(opts, workitem.WithBudget(newBudget))
			}
		}

		item, err := workitem.Create(root, prefix, newType, title, deps, opts...)
		if err != nil {
			return err
		}

		fmt.Printf("Created %s: %s\n", item.ID, item.Title)
		if len(deps) > 0 {
			// Report where the item ACTUALLY landed. This line used to say
			// "(pending)" unconditionally, which was a guess dressed as a fact:
			// an item filed onto an already-completed parent lands available,
			// and one filed onto a shelved parent lands shelved.
			status, err := workitem.Status(root, item.ID)
			if err != nil {
				status = "unknown"
			}
			fmt.Printf("  depends: %s (%s)\n", strings.Join(deps, ", "), status)

			// A dependent parked behind a shelved parent is the strand this
			// guard exists to prevent. Filing it is allowed — pre-filing work
			// behind a gate is a legitimate pattern — but it must not be
			// silent, because a silently-parked item is indistinguishable from
			// one that is waiting correctly.
			if status == "shelved" {
				blocking, err := workitem.ShelvedDeps(root, item.Depends)
				if err == nil && len(blocking) > 0 {
					fmt.Printf("\nNOTE: %s is shelved, not pending — it depends on shelved %s.\n",
						item.ID, strings.Join(blocking, ", "))
					fmt.Printf("      A shelved parent never reaches done, so a pending dependent would\n")
					fmt.Printf("      wait forever while looking like it was waiting correctly.\n")
					fmt.Printf("      Release it with: mg unshelve %s\n", blocking[0])
				}
			}
		}
		return nil
	},
}

func init() {
	newCmd.Flags().StringVar(&newType, "type", "task", "work item type")
	stringSliceVarWithAlias(newCmd.Flags(), &newDepends, "depends", "depend", "dependency IDs (comma-separated or repeated)")
	newCmd.Flags().StringVar(&newAssignee, "assignee", "", "person to assign this item to")
	newCmd.Flags().StringVar(&newBranch, "branch", "", "branch name for this work item")
	newCmd.Flags().StringVar(&newPriority, "priority", "", "priority level: low, medium, high (default: medium)")
	// Canonical flag name is --tags (plural), matching edit's --tags and the
	// --add-tags/--rm-tags/--depends family; --tag stays as a back-compat alias.
	// new and edit previously disagreed on which was canonical (gh drellem2/pogo#60).
	stringSliceVarWithAlias(newCmd.Flags(), &newTags, "tags", "tag", "tags (comma-separated or repeated)")
	newCmd.Flags().StringVar(&newTitle, "title", "", "work item title (alternative to positional args)")
	newCmd.Flags().StringVar(&newBody, "body", "", "work item body (markdown)")
	newCmd.Flags().StringVar(&newBodyFile, "body-file", "", "read the body verbatim from a file (\"-\" for stdin); mutually exclusive with --body")
	newCmd.Flags().StringVar(&newRepo, "repo", "", "repo path (defaults to current git toplevel for interactive use; auto-detection is skipped under pogo automation, where POGO_PID is set — pass --repo=PATH explicitly there; --repo=\"\" or --no-repo leaves it empty)")
	newCmd.Flags().BoolVar(&newNoRepo, "no-repo", false, "do not auto-detect or set a repo (use for non-coding work items)")
	newCmd.Flags().IntVar(&newBudget, "budget", 0, "estimated token budget (integer; omit or 0 to leave unset)")
	newCmd.Flags().StringVar(&newPrefix, "prefix", "", "override work item ID prefix for this call only (e.g. 'dr-'); not persisted")
	newCmd.Flags().BoolVar(&newDeclaresRemainder, "declares-remainder", false, "this item's output is a recommendation: 'mg done' refuses it until --successor names the item carrying it forward")
}

// addUniqueTag appends tag unless an equal-folded copy is already present, so
// `--tags=declares-remainder --declares-remainder` files one tag, not two.
func addUniqueTag(tags []string, tag string) []string {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), tag) {
			return tags
		}
	}
	return append(tags, tag)
}

// autoDetectRepo returns the repo path to auto-fill when neither --repo nor
// --no-repo was given. For interactive (human) use it is the git toplevel of
// the current working directory. Under pogo automation (POGO_PID set) it
// returns "" instead: a crew agent or polecat files from its own prompt/work
// directory, whose git toplevel is the agent's scratch dir — not the code repo
// the item is actually about — so auto-detection there records a misleading
// path (e.g. the mayor's prompt dir instead of the target repo). Automation
// should pass --repo=PATH explicitly. See gh drellem2/macguffin#5 (ia-51a5),
// superseded by #9 (mg-1866) which re-confirmed this end to end.
func autoDetectRepo() string {
	if os.Getenv("POGO_PID") != "" {
		return ""
	}
	return detectRepo()
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

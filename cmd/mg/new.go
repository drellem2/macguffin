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
	newRepo     string
	newNoRepo   bool
	newBudget   int
	newPrefix   string
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

The --assignee flag names the agent that owns triage and routing for the
item, not the agent that runs the work. Substantive work is performed by
an ephemeral polecat (named after the work-item ID at spawn time)
regardless of the assignee. Polecats are never named in advance, so they
cannot be assigned ahead of time; the assignee is the durable owner who
decides whether to dispatch the work, hold it, or close it.

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

		root, err := workspace.DefaultRoot()
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
		if tags := normSlice(newTags); len(tags) > 0 {
			opts = append(opts, workitem.WithTags(tags))
		}
		if newBody != "" {
			opts = append(opts, workitem.WithBody(newBody))
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
			fmt.Printf("  depends: %s (pending)\n", strings.Join(deps, ", "))
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
	newCmd.Flags().StringVar(&newRepo, "repo", "", "repo path (defaults to current git toplevel for interactive use; auto-detection is skipped under pogo automation, where POGO_PID is set — pass --repo=PATH explicitly there; --repo=\"\" or --no-repo leaves it empty)")
	newCmd.Flags().BoolVar(&newNoRepo, "no-repo", false, "do not auto-detect or set a repo (use for non-coding work items)")
	newCmd.Flags().IntVar(&newBudget, "budget", 0, "estimated token budget (integer; omit or 0 to leave unset)")
	newCmd.Flags().StringVar(&newPrefix, "prefix", "", "override work item ID prefix for this call only (e.g. 'dr-'); not persisted")
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

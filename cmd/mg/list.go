package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var listStatus string
var listAll bool
var listArchived bool
var listShelved bool
var listRepo string
var listTag string
var listAssignee string
var listJSON bool
var listWide bool

// listStatusValues is the single canonical set of values accepted by
// `mg list --status`, in the order humans expect to read them. BOTH the
// --status flag-usage string (see listStatusUsage) and the RunE validation
// (see isValidListStatus) derive from this slice, so the documented values and
// the accepted values cannot drift apart. Previously the usage string listed
// "archived" but not "pending" while the validation accepted "pending" but not
// "archived" — see gh drellem2/pogo#58.
var listStatusValues = []string{"available", "claimed", "pending", "done", "shelved", "archived"}

// listStatusUsage renders the --status flag help text from listStatusValues.
func listStatusUsage() string {
	return "filter by status (" + strings.Join(listStatusValues, ", ") + ")"
}

// isValidListStatus reports whether s is one of the canonical --status values.
func isValidListStatus(s string) bool {
	for _, v := range listStatusValues {
		if v == s {
			return true
		}
	}
	return false
}

// listJSONItem is the stable on-the-wire shape for `mg list --json` output.
// Field names and order are part of the public CLI contract — callers
// (kanban viewer, scripts, dashboards) parse this with `json.Unmarshal`.
type listJSONItem struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Status   string    `json:"status"`
	Title    string    `json:"title"`
	Tags     []string  `json:"tags"`
	Assignee string    `json:"assignee"`
	Priority string    `json:"priority"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	Depends  []string  `json:"depends"`
	Created  time.Time `json:"created"`
	Mtime    time.Time `json:"mtime"`
	// Snooze is the item's `snooze:` attribute VERBATIM (normally RFC3339 UTC),
	// empty when it carries none. It is a string rather than a timestamp so a
	// hand-written value mg could not parse reaches the consumer as written
	// instead of arriving as a zero time indistinguishable from "no snooze".
	Snooze string `json:"snooze"`

	// Successor, Predecessor and DeclaresRemainder are DERIVED from Tags — the
	// `successor:`/`predecessor:`/`declares-remainder` carriers, projected into
	// fields. They add no state; every one of them is recoverable from Tags by a
	// caller who knows the convention.
	//
	// They exist because a caller who does NOT know the convention had no way to
	// find them, and that cost a false bug report (mg-3386). An operator asked
	// whether a completed item had named a successor, ran
	//
	//	mg show mg-0e8c --json | jq 'keys'
	//
	// grepped the result for succ|next|child, found nothing, and reported the
	// declares-remainder gate as bypassed. The gate had fired and been satisfied;
	// the link was sitting in `tags` as "successor:mg-28b6", one level below where
	// a field lookup reaches. A trace that only answers a question you already
	// know how to ask is why "an enforced gate and a bypassed one look identical"
	// was a reasonable thing to conclude from real evidence.
	//
	// Together they make the gate's audit query writable from fields alone:
	//
	//	mg list --all --json | jq -r 'select(.declares_remainder
	//	  and (.status=="done" or .status=="archived")
	//	  and (.successor|length)==0) | .id'
	//
	// which is the check the ticket asked to be made possible: every item that
	// declared a remainder and completed without naming anything to carry it.
	// Empty arrays, never null, so `|length` and `[]` comparisons hold on every
	// item rather than only on the ones that have links.
	Successor         []string `json:"successor"`
	Predecessor       []string `json:"predecessor"`
	DeclaresRemainder bool     `json:"declares_remainder"`
}

func toJSONItem(item *workitem.Item, status string) listJSONItem {
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	depends := item.Depends
	if depends == nil {
		depends = []string{}
	}
	successor := workitem.SuccessorIDs(item)
	if successor == nil {
		successor = []string{}
	}
	predecessor := workitem.PredecessorIDs(item)
	if predecessor == nil {
		predecessor = []string{}
	}
	return listJSONItem{
		ID:       item.ID,
		Type:     item.Type,
		Status:   status,
		Title:    item.Title,
		Tags:     tags,
		Assignee: item.Assignee,
		Priority: item.Priority,
		Repo:     item.Repo,
		Branch:   item.Branch,
		Depends:  depends,
		Created:  item.Created,
		Mtime:    item.Mtime,
		Snooze:   item.SnoozeRaw,

		Successor:         successor,
		Predecessor:       predecessor,
		DeclaresRemainder: workitem.DeclaresRemainder(item),
	}
}

// formatSnooze returns a dim wake-time marker for the listing, so a snoozed
// item is distinguishable at a glance from one that is merely pending. That
// distinction is the whole point: "pending" is exactly what a correctly-waiting
// item looks like, and a snooze nobody can see is a snooze nobody audits.
func formatSnooze(item *workitem.Item) string {
	if item.SnoozeRaw == "" {
		return ""
	}
	if item.SnoozeMalformed() {
		return " \033[2m[snooze ⚠ unparseable]\033[0m"
	}
	if !item.Snoozed(time.Now().UTC()) {
		return " \033[2m[snooze elapsed]\033[0m"
	}
	return fmt.Sprintf(" \033[2m[snoozed %s]\033[0m", item.SnoozeRaw)
}

func writeJSONItems(out *os.File, items []*workitem.Item, status string) error {
	enc := json.NewEncoder(out)
	for _, item := range items {
		if err := enc.Encode(toJSONItem(item, status)); err != nil {
			return err
		}
	}
	return nil
}

// resolveCurrentUser returns the current OS username.
func resolveCurrentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return ""
}

// formatAssignee returns a styled assignee label to append after the title.
// If the assignee matches the current user, it shows as lowercase blue "human".
// Other assignees show as dim. No assignee returns an empty string.
func formatAssignee(assignee, currentUser string) string {
	if assignee == "" {
		return ""
	}
	if currentUser != "" && (assignee == currentUser || assignee == "human") {
		return " \033[34mhuman\033[0m"
	}
	return fmt.Sprintf(" \033[2m%s\033[0m", assignee)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List work items",
	Long: `List work items grouped by status (available, claimed, pending, done).

By default, archived and shelved items are hidden. Use --archived/-a to include
archived items, --shelved to include shelved items, or --all to include both.

Examples:
  mg list                       # active items + done
  mg list --archived            # also show archived items
  mg list --shelved             # also show shelved items
  mg list --all                 # include both archived and shelved
  mg list --status=shelved      # show only shelved items
  mg list --status=archived     # show only archived items
  mg list --tag=urgent          # filter by tag
  mg list --assignee=human      # only items assigned to current user
  mg list --wide                # never truncate, however narrow the terminal

On a terminal, each line is fitted to the terminal width: the id, type, assignee
and snooze marker are always shown, and the title and tags are shortened (with a
… marker) to make room. Piped or redirected output is never truncated, so
scripts reading ` + "`mg list`" + ` see the full line, and --json is unaffected.

--json emits one object per item (NDJSON). Alongside 'tags' it carries the
structured carriers that live inside them as their own fields — 'successor',
'predecessor' (both always arrays) and 'declares_remainder' — so a check does
not have to know the tag spelling to be written. The audit the
declares-remainder gate exists for is one of them:

  mg list --all --json | jq -r 'select(.declares_remainder
    and (.status=="done" or .status=="archived")
    and (.successor|length)==0) | .id'

  # every item that declared a remainder and completed naming nothing to
  # carry it forward. Empty output means the gate has not been bypassed.

That query asks whether a successor was NAMED, which is not the same as asking
whether one still EXISTS. A link whose target was deleted afterwards passes it,
so run the second half too rather than reading one empty result as clean:

  mg list --all --json | jq -sr '[.[].id] as $ids
    | .[] | select(.successor - $ids | length > 0)
    | .id + " -> " + ((.successor - $ids) | join(","))'

  # every item naming a successor that no longer exists. 'mg done' and
  # 'mg archive' refuse a dangling link at close time, so a hit here is one
  # that rotted after it was checked.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		currentUser := resolveCurrentUser()
		width := resolveListWidth(os.Stdout, listWide)

		if listStatus != "" {
			if !isValidListStatus(listStatus) {
				return fmt.Errorf("invalid status %q (must be one of: %s)", listStatus, strings.Join(listStatusValues, ", "))
			}
			var items []*workitem.Item
			if listStatus == "archived" {
				items, err = workitem.ListArchived(root)
			} else {
				items, err = workitem.ListByStatus(root, listStatus)
			}
			if err != nil {
				return err
			}

			items = filterByRepo(items, listRepo)
			items = filterByTag(items, listTag)
			items = filterByAssignee(items, listAssignee)

			if listJSON {
				return writeJSONItems(os.Stdout, items, listStatus)
			}

			if len(items) == 0 {
				fmt.Printf("No %s work items.\n", listStatus)
				return nil
			}

			for _, item := range items {
				fmt.Println(renderListLine("", item, currentUser, width))
			}
			return nil
		}

		grouped, err := workitem.ListAll(root)
		if err != nil {
			return err
		}

		// Include archived items if --all or --archived is set
		if listAll || listArchived {
			archived, err := workitem.ListArchived(root)
			if err != nil {
				return err
			}
			if len(archived) > 0 {
				grouped["archived"] = archived
			}
		}

		// Include shelved items if --all or --shelved is set
		if listAll || listShelved {
			shelved, err := workitem.ListByStatus(root, "shelved")
			if err != nil {
				return err
			}
			if len(shelved) > 0 {
				grouped["shelved"] = shelved
			}
		}

		// Apply repo filter to each group
		if listRepo != "" {
			for s, items := range grouped {
				grouped[s] = filterByRepo(items, listRepo)
			}
		}

		// Apply tag filter to each group
		if listTag != "" {
			for s, items := range grouped {
				grouped[s] = filterByTag(items, listTag)
			}
		}

		// Apply assignee filter to each group
		if listAssignee != "" {
			for s, items := range grouped {
				grouped[s] = filterByAssignee(items, listAssignee)
			}
		}

		order := []string{"available", "claimed", "pending", "done"}
		if listAll || listShelved {
			order = append(order, "shelved")
		}
		if listAll || listArchived {
			order = append(order, "archived")
		}

		if listJSON {
			for _, s := range order {
				if err := writeJSONItems(os.Stdout, grouped[s], s); err != nil {
					return err
				}
			}
			return nil
		}

		printed := false
		for _, s := range order {
			items := grouped[s]
			if len(items) == 0 {
				continue
			}
			printed = true
			fmt.Printf("%s:\n", s)
			for _, item := range items {
				fmt.Println(renderListLine("  ", item, currentUser, width))
			}
		}
		if !printed {
			fmt.Println("No work items.")
		}

		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", listStatusUsage())
	listCmd.Flags().BoolVar(&listAll, "all", false, "include archived and shelved items")
	listCmd.Flags().BoolVarP(&listArchived, "archived", "a", false, "include archived items")
	listCmd.Flags().BoolVar(&listShelved, "shelved", false, "include shelved items")
	listCmd.Flags().StringVar(&listRepo, "repo", "", "filter by repository path (substring match)")
	listCmd.Flags().StringVar(&listTag, "tag", "", "filter by tag")
	listCmd.Flags().StringVar(&listAssignee, "assignee", "", "filter by assignee (use 'human' for current user)")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "emit one JSON object per item (NDJSON), instead of human-formatted output")
	listCmd.Flags().BoolVar(&listWide, "wide", false, "do not fit lines to the terminal width (titles and tags are never shortened)")
	boolVarAlias(listCmd.Flags(), "wide", "no-truncate")
}

// formatTags returns a dim-styled tag string like " [tag1, tag2]" for display,
// or an empty string if the item has no tags.
func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return " \033[2m[" + strings.Join(tags, ", ") + "]\033[0m"
}

// filterByRepo returns only items whose Repo contains the given substring.
// If repo is empty, all items are returned.
func filterByRepo(items []*workitem.Item, repo string) []*workitem.Item {
	if repo == "" {
		return items
	}
	var filtered []*workitem.Item
	for _, item := range items {
		if strings.Contains(item.Repo, repo) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// filterByAssignee returns only items whose Assignee matches the given name.
// The special value "human" matches the current OS user. Items with no assignee
// are excluded when filtering. If assignee is empty, all items are returned.
func filterByAssignee(items []*workitem.Item, assignee string) []*workitem.Item {
	if assignee == "" {
		return items
	}
	resolvedAssignee := assignee
	if assignee == "human" {
		if u, err := user.Current(); err == nil {
			resolvedAssignee = u.Username
		} else if u := os.Getenv("USER"); u != "" {
			resolvedAssignee = u
		}
	}
	var filtered []*workitem.Item
	for _, item := range items {
		if item.Assignee == "" {
			continue
		}
		// Match both the literal value and the resolved username
		// (e.g. assignee "human" in the file should match --assignee=human)
		if item.Assignee == resolvedAssignee || item.Assignee == assignee {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// filterByTag returns only items that have the given tag.
// If tag is empty, all items are returned.
func filterByTag(items []*workitem.Item, tag string) []*workitem.Item {
	if tag == "" {
		return items
	}
	var filtered []*workitem.Item
	for _, item := range items {
		for _, t := range item.Tags {
			if t == tag {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

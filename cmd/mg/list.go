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
	}
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
  mg list --assignee=human      # only items assigned to current user`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		currentUser := resolveCurrentUser()

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
				fmt.Printf("%-10s %-8s %s%s%s\n", item.ID, item.Type, item.Title, formatTags(item.Tags), formatAssignee(item.Assignee, currentUser))
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
				fmt.Printf("  %-10s %-8s %s%s%s\n", item.ID, item.Type, item.Title, formatTags(item.Tags), formatAssignee(item.Assignee, currentUser))
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

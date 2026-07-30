package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/spend"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	showJSON     bool
	showBodyHash bool
)

// showJSONItem is the stable on-the-wire shape for `mg show ID --json`: the
// full work item as ONE JSON object. It embeds listJSONItem so the fields
// shared with `mg list --json` keep identical names (the frozen, additive-only
// CLI contract), and adds the single-item extras that the list view omits:
// creator, the full body, and budget/spend. Field names are FROZEN — new
// fields may be added, but existing ones are never renamed or removed.
type showJSONItem struct {
	listJSONItem
	Creator string `json:"creator"`
	Body    string `json:"body"`
	// BodyHash is the SHA-256 of the Body field's exact bytes — the version
	// token 'mg edit --if-unchanged' checks against. It rides alongside the
	// body so a reader gets the content and its version in ONE read: any gap
	// between fetching them is a window in which the pair can disagree, which
	// is the same defect at a smaller scale.
	BodyHash string `json:"body_hash"`
	Budget   *int   `json:"budget"` // null when unset
	Spent    int    `json:"spent"`  // total tokens recorded against this item
}

var showCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show a work item by ID",
	Long: `Show a work item by ID.

When two archived twins share a short ID across different month partitions the
bare ID is ambiguous. Disambiguate with an @partition qualifier:

  mg show mg-4fa7@2026-04   # the twin archived in partition 2026-04

'Creator' is whoever ran 'mg new' (MG_ACTOR, else POGO_AGENT_NAME, else the OS
user). It is self-asserted and forgeable, so it is attribution and not
authentication. On items filed before mg-ddf4 it records the unix user, which
was the SAME STRING for every agent on this box — read 'daniel' on an older
item as "unknown", not as Daniel.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		// One resolve, not two: reading the body and the status separately
		// let `show` render one item under another item's status.
		item, status, err := workitem.ReadWithStatus(root, args[0])
		if err != nil {
			return err
		}

		if showJSON && showBodyHash {
			return mgerr.Usage("mutually_exclusive_flags",
				"cannot use both --json and --body-hash",
				"--json already carries the hash in its body_hash field")
		}

		// --body-hash prints the bare hash and nothing else, so the safe write
		// is one command rather than a pipe through jq:
		//   mg edit ID --if-unchanged="$(mg show ID --body-hash)" --body-file …
		if showBodyHash {
			fmt.Println(workitem.BodyHash(item.Body))
			return nil
		}

		if showJSON {
			return writeShowJSON(root, item, status)
		}

		fmt.Printf("%-10s %s\n", "ID:", item.ID)
		fmt.Printf("%-10s %s\n", "Type:", item.Type)
		fmt.Printf("%-10s %s\n", "Status:", status)
		fmt.Printf("%-10s %s\n", "Created:", item.Created.Format("2006-01-02 15:04:05Z"))
		fmt.Printf("%-10s %s\n", "Creator:", item.Creator)
		if item.Assignee != "" {
			fmt.Printf("%-10s %s\n", "Assignee:", item.Assignee)
		}
		if item.Priority != "" {
			fmt.Printf("%-10s %s\n", "Priority:", item.Priority)
		}
		if item.Branch != "" {
			fmt.Printf("%-10s %s\n", "Branch:", item.Branch)
		}
		if item.Budget != nil {
			fmt.Printf("%-10s %s tokens\n", "Budget:", formatThousands(*item.Budget))
			if recs, err := spend.ReadItem(root, item.ID); err == nil && len(recs) > 0 {
				spent := 0
				for _, r := range recs {
					spent += r.Input + r.CacheRead + r.CacheCreate + r.Output
				}
				line := fmt.Sprintf("%s tokens (%d%% of budget)", formatThousands(spent), pctOfBudget(spent, *item.Budget))
				if *item.Budget > 0 && spent > *item.Budget {
					line += " ⚠"
				}
				fmt.Printf("%-10s %s\n", "Spent:", line)
			}
		}
		if len(item.Tags) > 0 {
			fmt.Printf("%-10s %s\n", "Tags:", strings.Join(item.Tags, ", "))
		}
		if len(item.Depends) > 0 {
			fmt.Printf("%-10s %s\n", "Depends:", strings.Join(item.Depends, ", "))
		}
		if item.SnoozeRaw != "" {
			// A gate you cannot see is a gate nobody audits. Malformed values
			// are shown as they are stored and named as malformed, because a
			// snooze mg cannot parse is one that never opens.
			switch {
			case item.SnoozeMalformed():
				fmt.Printf("%-10s %s  ⚠ not an RFC3339 timestamp — this gate can never open\n", "Snooze:", item.SnoozeRaw)
			case item.Snoozed(time.Now().UTC()):
				fmt.Printf("%-10s %s (in %s)\n", "Snooze:", item.SnoozeRaw, workitem.HumanUntil(time.Until(item.Snooze)))
			default:
				fmt.Printf("%-10s %s (elapsed; the next `mg schedule` sweep releases it)\n", "Snooze:", item.SnoozeRaw)
			}
		}
		if item.Repo != "" {
			fmt.Printf("%-10s %s\n", "Repo:", item.Repo)
		}
		fmt.Printf("%-10s %s\n", "Title:", item.Title)

		if item.Body != "" {
			fmt.Printf("\n%s", item.Body)
		}

		return nil
	},
}

func init() {
	showCmd.Flags().BoolVar(&showJSON, "json", false, "emit the full work item as one JSON object")
	showCmd.Flags().BoolVar(&showBodyHash, "body-hash", false, "print only the body's SHA-256, for 'mg edit --if-unchanged'")
}

// writeShowJSON marshals the work item as a single JSON object on stdout.
// Spend is summed from the per-item spend records (0 when none are recorded),
// mirroring the human view's Spent line.
func writeShowJSON(root string, item *workitem.Item, status string) error {
	spent := 0
	if recs, err := spend.ReadItem(root, item.ID); err == nil {
		for _, r := range recs {
			spent += r.Input + r.CacheRead + r.CacheCreate + r.Output
		}
	}
	out := showJSONItem{
		listJSONItem: toJSONItem(item, status),
		Creator:      item.Creator,
		Body:         item.Body,
		BodyHash:     workitem.BodyHash(item.Body),
		Budget:       item.Budget,
		Spent:        spent,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// pctOfBudget rounds spent/budget to a whole percentage. A zero budget is
// reported as 0% to avoid divide-by-zero — the ⚠ marker still fires when spent>0.
func pctOfBudget(spent, budget int) int {
	if budget <= 0 {
		return 0
	}
	return (spent * 100) / budget
}

// formatThousands renders an integer with comma separators (e.g. 200000 → "200,000").
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

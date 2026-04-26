package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/drellem2/macguffin/internal/spend"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	spendBy      string
	spendSince   string
	spendJSON    bool
	spendRebuild bool
)

var spendCmd = &cobra.Command{
	Use:   "spend",
	Short: "Aggregate token spend per work item, tag, repo, agent, etc.",
	Long: `Harvest Claude Code transcripts + events.jsonl and aggregate token usage.

The first run (or --rebuild) scans all transcripts and writes a per-item
NDJSON store under ~/.macguffin/spend/. Subsequent runs are incremental:
records are keyed on (session, message_uuid) and re-scans skip duplicates.

Examples:
  mg spend --by item                  # per-mg-id totals (default)
  mg spend --by tag                   # all tags, sorted by total
  mg spend --by tag:ux                # one tag, with item breakdown
  mg spend --by repo                  # cross-product overview
  mg spend --by agent                 # who's spending the most
  mg spend --since 7d                 # last 7 days
  mg spend --json                     # machine-readable output
  mg spend --rebuild                  # rescan all transcripts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		// --rebuild: drop the existing store before scanning.
		if spendRebuild {
			if err := os.RemoveAll(spend.StoreRoot(root)); err != nil {
				return fmt.Errorf("clearing store: %w", err)
			}
		}

		run := &spend.Run{Root: root}
		res, err := run.Aggregate()
		if err != nil {
			return fmt.Errorf("aggregator: %w", err)
		}
		_ = res // counts available for verbose mode later

		var since time.Duration
		if spendSince != "" {
			since, err = parseDuration(spendSince)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
		}

		groups, err := spend.Query(root, root, spend.QueryOpts{
			By:    spendBy,
			Since: since,
		})
		if err != nil {
			return err
		}

		if len(groups) == 0 {
			if spendJSON {
				fmt.Println("[]")
				return nil
			}
			fmt.Println("no spend data yet — run `mg spend --rebuild` to scan transcripts")
			return nil
		}

		if spendJSON {
			return writeSpendJSON(groups)
		}
		writeSpendTable(groups, spendBy)
		return nil
	},
}

func init() {
	spendCmd.Flags().StringVar(&spendBy, "by", "item", "group axis: item, tag, tag:<value>, repo, agent, priority, assignee")
	spendCmd.Flags().StringVar(&spendSince, "since", "", "filter to records within the last duration (e.g. 24h, 7d)")
	spendCmd.Flags().BoolVar(&spendJSON, "json", false, "emit JSON array (one object per group)")
	spendCmd.Flags().BoolVar(&spendRebuild, "rebuild", false, "drop the existing store and rescan all transcripts")
}

// jsonGroup is the stable wire shape for `mg spend --json`. PMs and
// dashboards parse this with json.Unmarshal.
type jsonGroup struct {
	Key         string `json:"key"`
	Items       int    `json:"items"`
	Input       int    `json:"input"`
	CacheRead   int    `json:"cache_read"`
	CacheCreate int    `json:"cache_create"`
	Output      int    `json:"output"`
	TotalIn     int    `json:"total_in"`
	TotalOut    int    `json:"total_out"`
}

func writeSpendJSON(groups []spend.Group) error {
	out := make([]jsonGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, jsonGroup{
			Key:         g.Key,
			Items:       g.Totals.ItemCount,
			Input:       g.Totals.Input,
			CacheRead:   g.Totals.CacheRead,
			CacheCreate: g.Totals.CacheCreate,
			Output:      g.Totals.Output,
			TotalIn:     g.Totals.TotalIn(),
			TotalOut:    g.Totals.TotalOut(),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSpendTable(groups []spend.Group, by string) {
	header := strings.ToUpper(groupHeaderFor(by))
	fmt.Printf("%-24s %6s %12s %12s %12s %12s %12s %12s\n",
		header, "ITEMS", "INPUT", "CACHE_READ", "CACHE_CREATE", "OUTPUT", "TOTAL_IN", "TOTAL_OUT")
	for _, g := range groups {
		fmt.Printf("%-24s %6d %12s %12s %12s %12s %12s %12s\n",
			truncate(g.Key, 24),
			g.Totals.ItemCount,
			formatThousands(g.Totals.Input),
			formatThousands(g.Totals.CacheRead),
			formatThousands(g.Totals.CacheCreate),
			formatThousands(g.Totals.Output),
			formatThousands(g.Totals.TotalIn()),
			formatThousands(g.Totals.TotalOut()),
		)
	}
}

func groupHeaderFor(by string) string {
	switch {
	case by == "" || by == "item":
		return "item"
	case strings.HasPrefix(by, "tag:"):
		return "item (tag:" + strings.TrimPrefix(by, "tag:") + ")"
	default:
		return by
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// parseDuration accepts Go's standard duration syntax plus a "Nd" suffix
// (days) since `time.ParseDuration` does not. Examples: "24h", "7d", "30m".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		num := strings.TrimSuffix(s, "d")
		// Allow fractional days for parity with "1.5d" if anyone tries it.
		for _, r := range num {
			if !(unicode.IsDigit(r) || r == '.') {
				return 0, fmt.Errorf("bad duration %q", s)
			}
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, fmt.Errorf("bad duration %q: %w", s, err)
		}
		return time.Duration(f * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q: %w", s, err)
	}
	return d, nil
}

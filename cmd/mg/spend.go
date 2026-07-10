package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/spend"
	"github.com/spf13/cobra"
)

var (
	spendBy      string
	spendSince   string
	spendWindow  string
	spendJSON    bool
	spendRebuild bool
	spendTotal   bool
)

var spendCmd = &cobra.Command{
	Use:   "spend",
	Short: "Aggregate token spend per work item, tag, repo, agent, etc.",
	Long: `Harvest Claude Code transcripts + events.jsonl and aggregate token usage.

The first run (or --rebuild) scans all transcripts and writes a per-item
NDJSON store under ~/.macguffin/spend/. Subsequent runs are incremental:
records are keyed on (session, message_uuid) and re-scans skip duplicates.

The rolling --since window and the calendar-anchored --window are two ways
to bound the same data: --since 24h is "the last 24 hours" (rolling), while
--window today is "since local midnight" (calendar). They are mutually
exclusive. --total prints a single headline of grand totals for today, this
week, and all time — it measures transcript token CONSUMPTION, not Anthropic's
usage-limit meter.

Examples:
  mg spend --by item                  # per-mg-id totals (default)
  mg spend --by tag                   # all tags, sorted by total
  mg spend --by tag:ux                # one tag, with item breakdown
  mg spend --by repo                  # cross-product overview
  mg spend --by agent                 # who's spending the most
  mg spend --since 7d                 # rolling last 7 days
  mg spend --window today             # calendar day (since local midnight)
  mg spend --window week              # calendar week (since Monday)
  mg spend --total                    # today / this week / all-time headline
  mg spend --json                     # machine-readable output
  mg spend --rebuild                  # rescan all transcripts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
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

		sinceSet := cmd.Flags().Changed("since")
		windowSet := cmd.Flags().Changed("window")
		if sinceSet && windowSet {
			return mgerr.Usage("mutually_exclusive_flags", "--since and --window are mutually exclusive: --since is a rolling duration, --window is a calendar anchor", "")
		}

		now := time.Now()

		// --total is a standalone grand-total headline; it doesn't compose
		// with the grouping/windowing flags.
		if spendTotal {
			if sinceSet || windowSet || cmd.Flags().Changed("by") {
				return mgerr.Usage("mutually_exclusive_flags", "--total is a standalone summary; drop --since/--window/--by", "")
			}
			return runSpendTotal(root, now)
		}

		var since time.Duration
		var sinceTime time.Time
		switch {
		case windowSet:
			sinceTime, err = spend.WindowStart(spendWindow, now)
			if err != nil {
				return mgerr.Usage("invalid_value", fmt.Sprintf("--window: %s", err), "")
			}
		case spendSince != "":
			since, err = parseDuration(spendSince)
			if err != nil {
				return mgerr.Usage("invalid_value", fmt.Sprintf("--since: %s", err), "")
			}
		}

		groups, err := spend.Query(root, root, spend.QueryOpts{
			By:        spendBy,
			Since:     since,
			SinceTime: sinceTime,
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
	spendCmd.Flags().StringVar(&spendSince, "since", "", "rolling window: keep records within the last duration (e.g. 24h, 7d)")
	spendCmd.Flags().StringVar(&spendWindow, "window", "", "calendar window: today (since local midnight) or week (since Monday)")
	spendCmd.Flags().BoolVar(&spendJSON, "json", false, "emit JSON array (one object per group)")
	spendCmd.Flags().BoolVar(&spendRebuild, "rebuild", false, "drop the existing store and rescan all transcripts")
	spendCmd.Flags().BoolVar(&spendTotal, "total", false, "print grand totals for today, this week, and all time")
}

// runSpendTotal prints the grand-total headline: today, this week, and all
// time. "today" and "week" are calendar-anchored (see spend.WindowStart), so
// the headline lines up with the --window views. It reports token consumption
// measured from transcripts, not Anthropic's usage-limit meter.
func runSpendTotal(root string, now time.Time) error {
	todayStart, err := spend.WindowStart("today", now)
	if err != nil {
		return err
	}
	weekStart, err := spend.WindowStart("week", now)
	if err != nil {
		return err
	}

	today, err := spend.GrandTotal(root, todayStart)
	if err != nil {
		return err
	}
	week, err := spend.GrandTotal(root, weekStart)
	if err != nil {
		return err
	}
	all, err := spend.GrandTotal(root, time.Time{})
	if err != nil {
		return err
	}

	if spendJSON {
		return writeSpendTotalJSON(today, week, all)
	}
	writeSpendTotalTable(today, week, all)
	return nil
}

// jsonTotal is the stable wire shape for one window of `mg spend --total --json`.
type jsonTotal struct {
	Items       int `json:"items"`
	Input       int `json:"input"`
	CacheRead   int `json:"cache_read"`
	CacheCreate int `json:"cache_create"`
	Output      int `json:"output"`
	TotalIn     int `json:"total_in"`
	TotalOut    int `json:"total_out"`
}

func totalOf(t spend.Totals) jsonTotal {
	return jsonTotal{
		Items:       t.ItemCount,
		Input:       t.Input,
		CacheRead:   t.CacheRead,
		CacheCreate: t.CacheCreate,
		Output:      t.Output,
		TotalIn:     t.TotalIn(),
		TotalOut:    t.TotalOut(),
	}
}

func writeSpendTotalJSON(today, week, all spend.Totals) error {
	out := struct {
		Today    jsonTotal `json:"today"`
		ThisWeek jsonTotal `json:"this_week"`
		AllTime  jsonTotal `json:"all_time"`
	}{
		Today:    totalOf(today),
		ThisWeek: totalOf(week),
		AllTime:  totalOf(all),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSpendTotalTable(today, week, all spend.Totals) {
	fmt.Println("GRAND TOTAL — transcript token consumption (not the Anthropic usage-limit meter)")
	fmt.Printf("%-12s %6s %12s %12s\n", "WINDOW", "ITEMS", "TOTAL_IN", "TOTAL_OUT")
	rows := []struct {
		label string
		t     spend.Totals
	}{
		{"today", today},
		{"this week", week},
		{"all time", all},
	}
	for _, row := range rows {
		fmt.Printf("%-12s %6d %12s %12s\n",
			row.label,
			row.t.ItemCount,
			formatThousands(row.t.TotalIn()),
			formatThousands(row.t.TotalOut()),
		)
	}
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
	out := make([]jsonGroup, 0, len(groups)+1)
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
	// Mirror the table's grand-total row as a trailing, self-identifying
	// object with the reserved uppercase key "TOTAL". This keeps the wire
	// shape a []jsonGroup, so consumers that unmarshal the array or select
	// groups by their (lowercase) item/tag/agent key are unaffected; the
	// key is uppercase precisely so it cannot collide with a real group key.
	tot := sumGroups(groups)
	out = append(out, jsonGroup{
		Key:         spendTotalKey,
		Items:       tot.ItemCount,
		Input:       tot.Input,
		CacheRead:   tot.CacheRead,
		CacheCreate: tot.CacheCreate,
		Output:      tot.Output,
		TotalIn:     tot.TotalIn(),
		TotalOut:    tot.TotalOut(),
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// spendTotalKey is the reserved group key used for the grand-total row/object
// in `mg spend` output. Uppercase so it never collides with an mg-id, tag,
// repo, agent, priority, or assignee (all lowercase).
const spendTotalKey = "TOTAL"

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
	// Grand-total row: the "total token usage from a single command" that
	// pogo#46 asks for by default. It sums the column across the groups shown
	// (the same convention as any table TOTAL row), so for the default
	// per-item view it is the true grand total.
	tot := sumGroups(groups)
	fmt.Printf("%-24s %6d %12s %12s %12s %12s %12s %12s\n",
		spendTotalKey,
		tot.ItemCount,
		formatThousands(tot.Input),
		formatThousands(tot.CacheRead),
		formatThousands(tot.CacheCreate),
		formatThousands(tot.Output),
		formatThousands(tot.TotalIn()),
		formatThousands(tot.TotalOut()),
	)
}

// sumGroups column-sums the token counts across the displayed groups. Callers
// use it to render the grand-total row/object; because it sums exactly the
// rows shown, the total is always internally consistent with the visible
// table (for the default per-item and per-agent axes each record is counted
// once, so it equals the true grand total).
func sumGroups(groups []spend.Group) spend.Totals {
	var t spend.Totals
	for _, g := range groups {
		t.ItemCount += g.Totals.ItemCount
		t.Input += g.Totals.Input
		t.CacheRead += g.Totals.CacheRead
		t.CacheCreate += g.Totals.CacheCreate
		t.Output += g.Totals.Output
	}
	return t
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

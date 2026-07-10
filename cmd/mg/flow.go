package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/drellem2/macguffin/internal/flow"
	"github.com/spf13/cobra"
)

var (
	flowLive            bool
	flowRepo            string
	flowBlockedAfter    time.Duration
	flowInterval        time.Duration
	flowGroupBy         string
	flowAgeDistribution bool
	flowJSONOut         bool
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Surface bottlenecks in work-item throughput",
	Long: `Read-only viewer over mg state, oriented around flow rather than columns.

By default, partitions items by lifecycle status and prints in/out/net per
24h and 7d, median age, and the oldest in-flight item. The status with the
highest median-age-to-throughput ratio is flagged. Items stuck in claimed
beyond --blocked-after (default 24h) are listed with the items waiting on
them.

--group-by swaps the partitioning axis. Accepted values:
  status (default), repo, tag, tag:<value>, assignee, priority, age

--age-distribution appends a four-bucket histogram (<24h, 24h–7d, 7d–30d,
>30d) computed under the active --repo / --group-by filter.

Examples:
  mg flow
  mg flow --live
  mg flow --repo=/path/to/repo
  mg flow --blocked-after=12h
  mg flow --group-by repo
  mg flow --group-by tag
  mg flow --group-by tag:ux
  mg flow --group-by age
  mg flow --age-distribution
  mg flow --repo=/path/to/repo --group-by tag:ux --age-distribution`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		// Non-default --group-by takes the grouped path (no event-derived
		// in/out/net, no blocked-chains, no spawn-pressure — those are
		// status-grouping concepts).
		gb := normalizeGroupBy(flowGroupBy)
		if gb != "status" {
			gsnap, err := flow.ComputeGrouped(root, flow.GroupedOptions{
				GroupBy:    gb,
				RepoFilter: flowRepo,
			})
			if err != nil {
				return err
			}
			if flowJSONOut {
				return writeFlowGroupedJSON(cmd.OutOrStdout(), gsnap)
			}
			flow.RenderGrouped(cmd.OutOrStdout(), gsnap, true)
			if flowAgeDistribution {
				flow.RenderAgeDistribution(cmd.OutOrStdout(), flow.ComputeAgeDistribution(gsnap.Records))
			}
			return nil
		}

		opts := flow.Options{
			BlockedAfter: flowBlockedAfter,
			RepoFilter:   flowRepo,
		}
		if flowJSONOut {
			// --json emits a single snapshot; the --live re-render loop is a
			// human-terminal affordance and does not apply.
			snap, err := flow.Compute(root, opts)
			if err != nil {
				return err
			}
			return writeFlowStatusJSON(cmd.OutOrStdout(), snap)
		}
		if flowLive {
			return runLive(cmd.OutOrStdout(), root, opts, flowInterval)
		}
		snap, err := flow.Compute(root, opts)
		if err != nil {
			return err
		}
		flow.Render(cmd.OutOrStdout(), snap, true)
		if flowAgeDistribution {
			recs, err := flow.LoadGroupRecs(root, snap.GeneratedAt)
			if err == nil {
				if flowRepo != "" {
					recs = filterRecsByRepoCmd(recs, flowRepo)
				}
				flow.RenderAgeDistribution(cmd.OutOrStdout(), flow.ComputeAgeDistribution(recs))
			}
		}
		return nil
	},
}

// normalizeGroupBy treats empty/whitespace as the default "status".
func normalizeGroupBy(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "status"
	}
	return s
}

// filterRecsByRepoCmd is a thin wrapper so the cmd layer can apply the same
// repo substring filter the grouped path does, when computing
// --age-distribution alongside the default status snapshot.
func filterRecsByRepoCmd(recs []flow.GroupRec, repo string) []flow.GroupRec {
	out := make([]flow.GroupRec, 0, len(recs))
	for _, r := range recs {
		if strings.Contains(r.Item.Repo, repo) {
			out = append(out, r)
		}
	}
	return out
}

// runLive re-renders the flow view whenever events.jsonl changes (mtime
// based), with a hard floor of `interval` between renders so we don't
// hammer the disk on a busy event log. Exits cleanly on SIGINT/SIGTERM.
func runLive(w writerFlusher, root string, opts flow.Options, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	eventsPath := filepath.Join(root, "events.jsonl")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var lastMtime time.Time
	var lastRender time.Time
	render := func() error {
		snap, err := flow.Compute(root, opts)
		if err != nil {
			return err
		}
		// Clear screen + home cursor (ANSI). Avoids flicker on most terminals.
		fmt.Fprint(w, "\033[2J\033[H")
		flow.Render(w, snap, true)
		fmt.Fprintln(w, "\n(--live mode — Ctrl-C to exit)")
		lastRender = time.Now()
		return nil
	}

	if err := render(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return nil
		case <-ticker.C:
			info, err := os.Stat(eventsPath)
			changed := err == nil && info.ModTime().After(lastMtime)
			if changed {
				lastMtime = info.ModTime()
			}
			// Re-render if events changed or it's been >5s since last paint
			// (so age numbers tick along on idle workspaces).
			if changed || time.Since(lastRender) > 5*time.Second {
				if err := render(); err != nil {
					return err
				}
			}
		}
	}
}

// writerFlusher is a minimal io.Writer alias kept distinct so future flushing
// behaviour (bufio.Writer) can slot in without touching call sites.
type writerFlusher interface {
	Write([]byte) (int, error)
}

func init() {
	flowCmd.Flags().BoolVar(&flowLive, "live", false, "tail events.jsonl and re-render on change")
	flowCmd.Flags().StringVar(&flowRepo, "repo", "", "filter by repository path (substring match)")
	flowCmd.Flags().DurationVar(&flowBlockedAfter, "blocked-after", 24*time.Hour, "flag claimed items older than this (e.g. 12h, 2d)")
	flowCmd.Flags().DurationVar(&flowInterval, "interval", time.Second, "polling interval for --live mode")
	flowCmd.Flags().StringVar(&flowGroupBy, "group-by", "status", "partition axis: status, repo, tag, tag:<value>, assignee, priority, age")
	flowCmd.Flags().BoolVar(&flowAgeDistribution, "age-distribution", false, "append a four-bucket age histogram below the main table")
	flowCmd.Flags().BoolVar(&flowJSONOut, "json", false, "emit the computed snapshot as one JSON object instead of the rendered table")
}

// flowJSON is the single, stable wire shape for `mg flow --json`. It covers
// BOTH the default status path and every --group-by path with ONE schema: the
// top-level keys never change with the invocation flag. On the status path
// `statuses`, `blocked`, and `spawn` are populated and `groups` is empty; on a
// --group-by path `groups` is populated and the status-only fields are empty
// (`spawn` is null). `group_by` names the active axis ("status" by default, or
// the grouping name). Field names are FROZEN and additive-only — new fields may
// be added, existing ones are never renamed or removed. See drellem2/pogo#55.
type flowJSON struct {
	GeneratedAt time.Time         `json:"generated_at"`
	GroupBy     string            `json:"group_by"`
	Bottleneck  string            `json:"bottleneck"`
	Statuses    []flowStatusJSON  `json:"statuses"`
	Groups      []flowGroupJSON   `json:"groups"`
	Blocked     []flowBlockedJSON `json:"blocked"`
	Spawn       *flowSpawnJSON    `json:"spawn"`
}

// flowStatusJSON mirrors flow.StatusMetrics for the status path. Durations are
// serialized as float hours (median_age_hours / oldest_age_hours) to stay both
// machine-friendly and consistent with the grouped path's hour-based ages.
type flowStatusJSON struct {
	Status         string  `json:"status"`
	Count          int     `json:"count"`
	In24h          int     `json:"in_24h"`
	Out24h         int     `json:"out_24h"`
	Net24h         int     `json:"net_24h"`
	In7d           int     `json:"in_7d"`
	Out7d          int     `json:"out_7d"`
	Net7d          int     `json:"net_7d"`
	MedianAgeHours float64 `json:"median_age_hours"`
	OldestAgeHours float64 `json:"oldest_age_hours"`
	OldestID       string  `json:"oldest_id"`
}

// flowGroupJSON mirrors flow.GroupedMetrics for the --group-by paths.
type flowGroupJSON struct {
	Key            string  `json:"key"`
	Label          string  `json:"label"`
	Active         int     `json:"active"`
	Done7d         int     `json:"done_7d"`
	MedianAgeHours float64 `json:"median_age_hours"`
	OldestID       string  `json:"oldest_id"`
	OldestAgeHours float64 `json:"oldest_age_hours"`
}

// flowBlockedJSON mirrors flow.BlockedItem (status path only).
type flowBlockedJSON struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Title    string   `json:"title"`
	AgeHours float64  `json:"age_hours"`
	Blocking []string `json:"blocking"`
}

// flowSpawnJSON mirrors flow.SpawnPressure (status path only).
type flowSpawnJSON struct {
	Available  int  `json:"available"`
	Polecats   int  `json:"polecats"`
	PolecatsOK bool `json:"polecats_ok"`
}

func writeFlowStatusJSON(w io.Writer, snap flow.Snapshot) error {
	out := flowJSON{
		GeneratedAt: snap.GeneratedAt,
		GroupBy:     "status",
		Bottleneck:  snap.Bottleneck,
		Statuses:    make([]flowStatusJSON, 0, len(snap.Statuses)),
		Groups:      []flowGroupJSON{},
		Blocked:     make([]flowBlockedJSON, 0, len(snap.Blocked)),
	}
	for _, s := range snap.Statuses {
		out.Statuses = append(out.Statuses, flowStatusJSON{
			Status:         s.Status,
			Count:          s.Count,
			In24h:          s.In24h,
			Out24h:         s.Out24h,
			Net24h:         s.Net24h,
			In7d:           s.In7d,
			Out7d:          s.Out7d,
			Net7d:          s.Net7d,
			MedianAgeHours: s.MedianAge.Hours(),
			OldestAgeHours: s.OldestAge.Hours(),
			OldestID:       s.OldestID,
		})
	}
	for _, b := range snap.Blocked {
		blocking := b.Blocking
		if blocking == nil {
			blocking = []string{}
		}
		out.Blocked = append(out.Blocked, flowBlockedJSON{
			ID:       b.ID,
			Status:   b.Status,
			Title:    b.Title,
			AgeHours: b.Age.Hours(),
			Blocking: blocking,
		})
	}
	out.Spawn = &flowSpawnJSON{
		Available:  snap.Spawn.Available,
		Polecats:   snap.Spawn.Polecats,
		PolecatsOK: snap.Spawn.PolecatsOK,
	}
	return encodeFlowJSON(w, out)
}

func writeFlowGroupedJSON(w io.Writer, snap *flow.GroupedSnapshot) error {
	out := flowJSON{
		GeneratedAt: snap.GeneratedAt,
		GroupBy:     snap.GroupBy,
		Bottleneck:  snap.Bottleneck,
		Statuses:    []flowStatusJSON{},
		Groups:      make([]flowGroupJSON, 0, len(snap.Groups)),
		Blocked:     []flowBlockedJSON{},
		Spawn:       nil,
	}
	for _, g := range snap.Groups {
		out.Groups = append(out.Groups, flowGroupJSON{
			Key:            g.Key,
			Label:          g.Label,
			Active:         g.Active,
			Done7d:         g.Done7d,
			MedianAgeHours: g.MedianAgeHours,
			OldestID:       g.OldestID,
			OldestAgeHours: g.OldestAgeHours,
		})
	}
	return encodeFlowJSON(w, out)
}

func encodeFlowJSON(w io.Writer, out flowJSON) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

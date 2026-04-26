package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/drellem2/macguffin/internal/flow"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	flowLive            bool
	flowRepo            string
	flowBlockedAfter    time.Duration
	flowInterval        time.Duration
	flowGroupBy         string
	flowAgeDistribution bool
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
		root, err := workspace.DefaultRoot()
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
}

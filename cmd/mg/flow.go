package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/drellem2/macguffin/internal/flow"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	flowLive         bool
	flowRepo         string
	flowBlockedAfter time.Duration
	flowInterval     time.Duration
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Surface bottlenecks in work-item throughput",
	Long: `Read-only viewer over mg state, oriented around flow rather than columns.

For each status, prints in/out/net per 24h and 7d, median age, and the
oldest in-flight item. The status with the worst median-age-vs-throughput
ratio is flagged. Items stuck in claimed beyond --blocked-after (default
24h) are listed with the items waiting on them.

Examples:
  mg flow
  mg flow --live
  mg flow --repo=/path/to/repo
  mg flow --blocked-after=12h`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
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
		return nil
	},
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
}

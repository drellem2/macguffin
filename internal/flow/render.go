package flow

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Render writes a compact, human-readable view of the snapshot to w. The
// format is row-oriented per status (no kanban columns) and intentionally
// fits in <=80 columns of terminal width.
func Render(w io.Writer, snap Snapshot, bottleneckHighlight bool) {
	fmt.Fprintf(w, "mg flow @ %s\n", snap.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintln(w, strings.Repeat("─", 72))

	header := fmt.Sprintf("%-10s %5s %12s %12s %10s  %s", "status", "count", "in/out/net 24h", "in/out/net 7d", "med-age", "oldest")
	fmt.Fprintln(w, header)

	for _, m := range snap.Statuses {
		marker := "  "
		if bottleneckHighlight && m.Status == snap.Bottleneck {
			marker = "▶ "
		}
		oldest := "—"
		if m.OldestID != "" {
			oldest = fmt.Sprintf("%s (%s)", m.OldestID, shortDuration(m.OldestAge))
		}
		fmt.Fprintf(w, "%s%-8s %5d %12s %12s %10s  %s\n",
			marker,
			m.Status,
			m.Count,
			fmt.Sprintf("%d/%d/%+d", m.In24h, m.Out24h, m.Net24h),
			fmt.Sprintf("%d/%d/%+d", m.In7d, m.Out7d, m.Net7d),
			shortDuration(m.MedianAge),
			oldest,
		)
	}

	if snap.Bottleneck != "" {
		fmt.Fprintf(w, "\nhighest median-age-to-throughput ratio: %s\n", snap.Bottleneck)
	} else {
		fmt.Fprintln(w, "\nhighest median-age-to-throughput ratio: none — flow is healthy")
	}

	fmt.Fprintln(w)
	if len(snap.Blocked) == 0 {
		fmt.Fprintln(w, "blocked chains: none")
	} else {
		fmt.Fprintln(w, "blocked chains:")
		for _, b := range snap.Blocked {
			title := b.Title
			if title == "" {
				title = "(no title)"
			}
			line := fmt.Sprintf("  %s in %s for %s — %s", b.ID, b.Status, shortDuration(b.Age), title)
			if len(b.Blocking) > 0 {
				line += fmt.Sprintf(" — blocking %s", strings.Join(b.Blocking, ", "))
			}
			fmt.Fprintln(w, line)
		}
	}

	fmt.Fprintln(w)
	if snap.Spawn.PolecatsOK {
		fmt.Fprintf(w, "spawn pressure: available:%d polecats:%d", snap.Spawn.Available, snap.Spawn.Polecats)
		if snap.Spawn.Available > snap.Spawn.Polecats {
			fmt.Fprintf(w, " — %d items queued without workers", snap.Spawn.Available-snap.Spawn.Polecats)
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "spawn pressure: available:%d polecats:? (pogo agent list unavailable)\n", snap.Spawn.Available)
	}
}

// RenderGrouped writes a compact view of a non-status grouped snapshot. The
// columns are simpler than Render's because non-status groupings have no
// notion of in/out events: items don't transition between repos or tags.
func RenderGrouped(w io.Writer, snap *GroupedSnapshot, bottleneckHighlight bool) {
	fmt.Fprintf(w, "mg flow @ %s — group-by %s\n",
		snap.GeneratedAt.Format("2006-01-02 15:04:05 MST"), snap.GroupBy)
	fmt.Fprintln(w, strings.Repeat("─", 72))

	header := fmt.Sprintf("%-26s %7s %7s %10s  %s", "group", "active", "done7d", "med-age", "oldest")
	fmt.Fprintln(w, header)

	for _, m := range snap.Groups {
		marker := "  "
		if bottleneckHighlight && m.Key == snap.Bottleneck {
			marker = "▶ "
		}
		oldest := "—"
		if m.OldestID != "" {
			oldest = fmt.Sprintf("%s (%s)", m.OldestID, shortHours(m.OldestAgeHours))
		}
		fmt.Fprintf(w, "%s%-24s %7d %7d %10s  %s\n",
			marker,
			truncate(m.Label, 24),
			m.Active,
			m.Done7d,
			shortHours(m.MedianAgeHours),
			oldest,
		)
	}

	if snap.Bottleneck != "" {
		fmt.Fprintf(w, "\nhighest median-age-to-throughput ratio: %s\n", snap.Bottleneck)
	} else {
		fmt.Fprintln(w, "\nhighest median-age-to-throughput ratio: none — flow is healthy")
	}
}

// RenderAgeDistribution prints the four-bucket histogram below the main
// table. The bar widths scale to the largest bucket so even tiny workspaces
// produce a readable shape.
func RenderAgeDistribution(w io.Writer, dist AgeDistribution) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "age distribution:")
	if dist.Total == 0 {
		fmt.Fprintln(w, "  (no items)")
		return
	}
	maxCount := 0
	for _, b := range AgeBuckets {
		if c := dist.Count(b); c > maxCount {
			maxCount = c
		}
	}
	const barWidth = 32
	for _, b := range AgeBuckets {
		c := dist.Count(b)
		bar := ""
		if maxCount > 0 {
			n := (c * barWidth) / maxCount
			bar = strings.Repeat("█", n)
		}
		fmt.Fprintf(w, "  %-8s %4d  %s\n", b, c, bar)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// shortHours formats a duration-in-hours like shortDuration but for the
// float64 hours used by GroupedMetrics.
func shortHours(h float64) string {
	if h <= 0 {
		return "—"
	}
	return shortDuration(time.Duration(h * float64(time.Hour)))
}

// shortDuration prints durations in a compact form: 12s, 4m, 3h, 2d.
func shortDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

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
		fmt.Fprintf(w, "\nbottleneck: %s (worst median-age vs throughput)\n", snap.Bottleneck)
	} else {
		fmt.Fprintln(w, "\nbottleneck: none — flow is healthy")
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

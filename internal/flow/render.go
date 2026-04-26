package flow

import (
	"fmt"
	"io"
	"strings"
)

// Render writes a flow Result to w as a plain-text table. If dist is non-nil,
// it appends an age-distribution histogram below the main table.
func Render(w io.Writer, res *Result, dist *AgeDistribution) {
	header := strings.ToUpper(res.GroupBy)
	// For tag:<v> grouping, the underlying group keys are statuses; the
	// header label should reflect that the rows are statuses inside the
	// filter.
	if strings.HasPrefix(res.GroupBy, "tag:") {
		header = "STATUS (tag=" + strings.TrimPrefix(res.GroupBy, "tag:") + ")"
	}

	// Compute column widths.
	groupColWidth := len(header)
	for _, m := range res.Groups {
		if l := len(m.Label); l > groupColWidth {
			groupColWidth = l
		}
	}
	if groupColWidth < 8 {
		groupColWidth = 8
	}

	const (
		itemsCol     = 8
		medianAgeCol = 12
		throughCol   = 8
	)

	// Header row.
	fmt.Fprintf(w, "%-*s  %*s  %*s  %*s\n",
		groupColWidth, header,
		itemsCol, "ITEMS",
		medianAgeCol, "MEDIAN AGE",
		throughCol, "DONE 7d")
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		strings.Repeat("─", groupColWidth),
		strings.Repeat("─", itemsCol),
		strings.Repeat("─", medianAgeCol),
		strings.Repeat("─", throughCol))

	if len(res.Groups) == 0 {
		fmt.Fprintf(w, "(no items)\n")
		return
	}

	for _, m := range res.Groups {
		ageStr := FormatDuration(m.MedianAgeHours)
		if m.Active == 0 {
			ageStr = "—"
		}
		fmt.Fprintf(w, "%-*s  %*d  %*s  %*d\n",
			groupColWidth, m.Label,
			itemsCol, m.Active,
			medianAgeCol, ageStr,
			throughCol, m.Done7d)
	}

	fmt.Fprintf(w, "\nTotals: %d active, %d done in last 7d\n", res.TotalActive, res.TotalDone7d)

	if res.Bottleneck != "" {
		// Use the group's display label (looks up via the result groups so
		// "tag *" annotations and the like carry through).
		label := res.Bottleneck
		for _, m := range res.Groups {
			if m.Key == res.Bottleneck {
				label = m.Label
				break
			}
		}
		fmt.Fprintf(w, "highest median-age-to-throughput ratio: %s (median age %s)\n",
			label, FormatDuration(res.BottleneckAgeH))
	}

	if dist != nil {
		renderAgeDistribution(w, *dist)
	}
}

func renderAgeDistribution(w io.Writer, d AgeDistribution) {
	fmt.Fprintf(w, "\nAge distribution:\n")
	max := 0
	for _, b := range AgeBuckets {
		if c := d.Count(b); c > max {
			max = c
		}
	}
	const barWidth = 24
	for _, b := range AgeBuckets {
		c := d.Count(b)
		bar := ""
		if max > 0 && c > 0 {
			n := (c * barWidth) / max
			if n < 1 {
				n = 1
			}
			bar = strings.Repeat("█", n)
		}
		fmt.Fprintf(w, "  %-7s %4d  %s\n", b, c, bar)
	}
	fmt.Fprintf(w, "  total   %4d\n", d.Total)
}

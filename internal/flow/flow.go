// Package flow computes throughput and bottleneck metrics across work items.
//
// The entry point is Compute, which loads items from the workitem store,
// applies an optional repo filter, partitions items via a Grouping, and
// returns per-group metrics plus a bottleneck pick (the group with the
// highest median-age-to-throughput ratio).
package flow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// activeStatuses are the lifecycle states considered "in flight" — items here
// are counted toward median-age computations.
var activeStatuses = []string{"available", "claimed", "pending"}

// recentDoneWindow is how far back we look for completed items when measuring
// throughput. Items in done/ with mtime within this window count as throughput.
const recentDoneWindow = 7 * 24 * time.Hour

// ItemRec pairs a work item with its current lifecycle status and (for done
// items) the completion timestamp proxy (file mtime). AgeBucket is precomputed
// against the same `now` used elsewhere in Compute, so age-based groupings
// can partition without re-deriving it.
type ItemRec struct {
	Item      *workitem.Item
	Status    string    // "available", "claimed", "pending", "done"
	DoneAt    time.Time // mtime of the done file; zero for active items
	AgeBucket string    // "<24h", "24h–7d", "7d–30d", ">30d"
}

// Active reports whether this record is in an active (non-done) status.
func (r ItemRec) Active() bool {
	return r.Status != "done"
}

// GroupMetrics is the per-group flow report.
type GroupMetrics struct {
	Key            string  // raw grouping key, e.g. "available", "tag:ux", "user@host"
	Label          string  // human-readable label, e.g. "available"
	Active         int     // count of active items in this group
	Done7d         int     // count of recently-done items in this group
	MedianAgeHours float64 // median age (hours) of active items in this group; 0 when no active items
	// ExcludeFromBottleneck is set for groups that should not be considered
	// for bottleneck synthesis (e.g. the "done" group in a status grouping —
	// it's the destination, not a stuck queue).
	ExcludeFromBottleneck bool
}

// Result is the output of Compute.
type Result struct {
	GroupBy        string         // the grouping name actually used
	Groups         []GroupMetrics // ordered for display
	Bottleneck     string         // group key with highest median-age-to-throughput ratio; empty if undecidable
	BottleneckAgeH float64        // median age of the bottleneck group (for display)
	TotalActive    int            // total active items considered (post-filter)
	TotalDone7d    int            // total recently-done items considered (post-filter)
}

// Options configures Compute.
type Options struct {
	Root    string    // macguffin root
	GroupBy string    // raw --group-by flag value (e.g. "status", "tag:ux")
	Repo    string    // optional repo filter (substring match on item.Repo)
	Now     time.Time // injected for tests; zero = time.Now().UTC()
}

// Compute loads items, applies the repo filter, partitions via the requested
// grouping, and computes per-group metrics plus a bottleneck pick.
func Compute(opts Options) (*Result, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	grouping, err := ParseGroupBy(opts.GroupBy)
	if err != nil {
		return nil, err
	}

	records, err := loadRecords(opts.Root, now)
	if err != nil {
		return nil, err
	}

	records = FilterByRepo(records, opts.Repo)

	// Filter records to those the grouping accepts (e.g. tag:<value> drops
	// items missing the tag).
	records = grouping.Filter(records)

	// Partition.
	groups := make(map[string][]ItemRec)
	for _, r := range records {
		for _, key := range grouping.Assign(r) {
			groups[key] = append(groups[key], r)
		}
	}

	// Build metrics per group.
	var metrics []GroupMetrics
	for key, recs := range groups {
		m := GroupMetrics{
			Key:                   key,
			Label:                 grouping.Label(key),
			ExcludeFromBottleneck: grouping.ExcludesFromBottleneck(key),
		}
		var ages []float64
		for _, r := range recs {
			if r.Active() {
				m.Active++
				ages = append(ages, now.Sub(r.Item.Created).Hours())
			} else if r.Status == "done" && !r.DoneAt.IsZero() && now.Sub(r.DoneAt) <= recentDoneWindow {
				m.Done7d++
			}
		}
		m.MedianAgeHours = median(ages)
		metrics = append(metrics, m)
	}

	// Order groups via grouping preference, falling back to label sort.
	order := grouping.Order()
	orderIdx := make(map[string]int, len(order))
	for i, k := range order {
		orderIdx[k] = i
	}
	sort.SliceStable(metrics, func(i, j int) bool {
		oi, iOK := orderIdx[metrics[i].Key]
		oj, jOK := orderIdx[metrics[j].Key]
		if iOK && jOK {
			return oi < oj
		}
		if iOK {
			return true
		}
		if jOK {
			return false
		}
		return metrics[i].Label < metrics[j].Label
	})

	// Compute totals + bottleneck.
	res := &Result{
		GroupBy: grouping.Name(),
		Groups:  metrics,
	}
	for _, m := range metrics {
		res.TotalActive += m.Active
		res.TotalDone7d += m.Done7d
	}
	res.Bottleneck, res.BottleneckAgeH = pickBottleneck(metrics)

	return res, nil
}

// pickBottleneck returns the group key with the highest median-age-to-
// throughput ratio. Groups with no active items are skipped (they cannot be
// the bottleneck — there's nothing stuck there). Groups marked
// ExcludeFromBottleneck are also skipped (e.g. "done" in a status grouping).
// Throughput is treated as max(Done7d, 0.5) to avoid divide-by-zero and to
// penalize groups with zero throughput more than groups with throughput=1.
func pickBottleneck(metrics []GroupMetrics) (string, float64) {
	var bestKey string
	var bestRatio float64
	var bestAge float64
	for _, m := range metrics {
		if m.ExcludeFromBottleneck || m.Active == 0 {
			continue
		}
		throughput := float64(m.Done7d)
		if throughput < 0.5 {
			throughput = 0.5
		}
		ratio := m.MedianAgeHours / throughput
		if ratio > bestRatio {
			bestRatio = ratio
			bestKey = m.Key
			bestAge = m.MedianAgeHours
		}
	}
	return bestKey, bestAge
}

// LoadAllRecords loads every relevant work item with status + completion-time
// proxy. Exposed for callers (e.g. age distribution) that want to share the
// same load.
func LoadAllRecords(root string, now time.Time) ([]ItemRec, error) {
	return loadRecords(root, now)
}

func loadRecords(root string, now time.Time) ([]ItemRec, error) {
	var out []ItemRec

	for _, status := range activeStatuses {
		items, err := workitem.ListByStatus(root, status)
		if err != nil {
			// Missing directories aren't fatal — treat as empty.
			continue
		}
		for _, it := range items {
			out = append(out, ItemRec{
				Item:      it,
				Status:    status,
				AgeBucket: ageBucket(now.Sub(it.Created)),
			})
		}
	}

	// Done items: include completion-time proxy via file mtime so we can
	// compute throughput. We only include done files modified within
	// recentDoneWindow because anything older doesn't influence current
	// throughput, and dragging in years of done items is wasteful.
	doneDir := filepath.Join(root, "work", "done")
	entries, err := os.ReadDir(doneDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			mtime := info.ModTime()
			// Skip ancient done items entirely — they don't contribute to
			// any current metric and bloat memory on long-lived workspaces.
			if now.Sub(mtime) > recentDoneWindow*4 {
				continue
			}
			path := filepath.Join(doneDir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			it, err := workitem.Parse(string(data))
			if err != nil {
				continue
			}
			out = append(out, ItemRec{
				Item:      it,
				Status:    "done",
				DoneAt:    mtime,
				AgeBucket: ageBucket(now.Sub(it.Created)),
			})
		}
	}

	return out, nil
}

// FilterByRepo returns only records whose item.Repo contains the given
// substring. Empty repo passes everything through.
func FilterByRepo(records []ItemRec, repo string) []ItemRec {
	if repo == "" {
		return records
	}
	out := make([]ItemRec, 0, len(records))
	for _, r := range records {
		if strings.Contains(r.Item.Repo, repo) {
			out = append(out, r)
		}
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// FormatDuration renders a duration in hours as a compact "Nd Mh" / "Nh" /
// "Nm" string suitable for display alongside group rows.
func FormatDuration(hours float64) string {
	if hours <= 0 {
		return "—"
	}
	d := time.Duration(hours * float64(time.Hour))
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins < 1 {
			mins = 1
		}
		return fmt.Sprintf("%dm", mins)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d / (24 * time.Hour))
	rem := d - time.Duration(days)*24*time.Hour
	hrs := int(rem.Hours())
	if hrs == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hrs)
}

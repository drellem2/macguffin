package flow

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// recentDoneWindow is how far back we look at done/ for "recently completed"
// throughput counting. Items in done/ with mtime within this window count
// toward Done7d.
const recentDoneWindow = 7 * 24 * time.Hour

// GroupedMetrics is the per-group flow report produced by ComputeGrouped.
// This model is simpler than StatusMetrics — non-status groupings have no
// notion of in/out events (items don't transition between repos or tags).
type GroupedMetrics struct {
	Key                   string
	Label                 string
	Active                int     // count of in-flight items in this group
	Done7d                int     // count of recently-done items in this group
	MedianAgeHours        float64 // median age in hours of active items; 0 if none
	OldestID              string
	OldestAgeHours        float64
	ExcludeFromBottleneck bool
}

// GroupedSnapshot is the result of ComputeGrouped.
type GroupedSnapshot struct {
	GeneratedAt    time.Time
	GroupBy        string
	Groups         []GroupedMetrics
	Bottleneck     string  // group key with highest median-age-to-throughput ratio; empty if undecidable
	BottleneckAgeH float64 // median age of the bottleneck group
	TotalActive    int
	TotalDone7d    int
	// Records is the post-filter, post-grouping-filter record set used to
	// compute Groups; exposed so callers can derive --age-distribution from
	// the same view.
	Records []GroupRec
}

// GroupedOptions configures ComputeGrouped.
type GroupedOptions struct {
	GroupBy    string    // raw --group-by flag value
	RepoFilter string    // optional substring match on item.Repo
	Now        time.Time // injected for tests; zero = time.Now().UTC()
}

// ComputeGrouped loads items, applies the repo filter, partitions via the
// requested grouping, and computes per-group metrics + a bottleneck pick.
// Use this for any --group-by other than the default "status" path; the
// existing Compute() handles the default case with its richer event-derived
// per-status metrics.
func ComputeGrouped(root string, opts GroupedOptions) (*GroupedSnapshot, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	grouping, err := ParseGroupBy(opts.GroupBy)
	if err != nil {
		return nil, err
	}

	records, err := LoadGroupRecs(root, now)
	if err != nil {
		return nil, err
	}
	records = filterRecsByRepo(records, opts.RepoFilter)
	records = grouping.Filter(records)

	buckets := make(map[string][]GroupRec)
	for _, r := range records {
		for _, key := range grouping.Assign(r) {
			buckets[key] = append(buckets[key], r)
		}
	}

	var metrics []GroupedMetrics
	for key, recs := range buckets {
		m := GroupedMetrics{
			Key:                   key,
			Label:                 grouping.Label(key),
			ExcludeFromBottleneck: grouping.ExcludesFromBottleneck(key),
		}
		var ages []float64
		for _, r := range recs {
			if r.Active() {
				m.Active++
				age := now.Sub(r.Item.Created).Hours()
				if age < 0 {
					age = 0
				}
				ages = append(ages, age)
				if age > m.OldestAgeHours {
					m.OldestAgeHours = age
					m.OldestID = r.Item.ID
				}
			} else if r.Status == "done" && !r.DoneAt.IsZero() && now.Sub(r.DoneAt) <= recentDoneWindow {
				m.Done7d++
			}
		}
		m.MedianAgeHours = medianHours(ages)
		metrics = append(metrics, m)
	}

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

	res := &GroupedSnapshot{
		GeneratedAt: now,
		GroupBy:     grouping.Name(),
		Groups:      metrics,
		Records:     records,
	}
	for _, m := range metrics {
		res.TotalActive += m.Active
		res.TotalDone7d += m.Done7d
	}
	res.Bottleneck, res.BottleneckAgeH = pickGroupedBottleneck(metrics)
	return res, nil
}

// pickGroupedBottleneck selects the group with the highest median-age-to-
// throughput ratio. Groups with no active items are skipped (nothing stuck
// there) and groups marked ExcludeFromBottleneck are skipped. Throughput is
// floored at 0.5 to avoid divide-by-zero and to penalize zero-throughput
// groups more than throughput-of-1 groups.
func pickGroupedBottleneck(metrics []GroupedMetrics) (string, float64) {
	var bestKey string
	var bestRatio, bestAge float64
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

// LoadGroupRecs loads every relevant work item with status + completion-time
// proxy + age bucket precomputed against `now`. Active items come from
// available/claimed/pending; done items come from done/ with mtime ≤
// recentDoneWindow*4 (older done items don't influence current metrics).
//
// Exposed so callers wanting --age-distribution under the default --group-by
// status path can share the same load.
func LoadGroupRecs(root string, now time.Time) ([]GroupRec, error) {
	var out []GroupRec

	for _, status := range []string{"available", "claimed", "pending"} {
		items, err := workitem.ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, it := range items {
			out = append(out, GroupRec{
				Item:      it,
				Status:    status,
				AgeBucket: ageBucket(now.Sub(it.Created)),
			})
		}
	}

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
			out = append(out, GroupRec{
				Item:      it,
				Status:    "done",
				DoneAt:    mtime,
				AgeBucket: ageBucket(now.Sub(it.Created)),
			})
		}
	}

	return out, nil
}

func filterRecsByRepo(records []GroupRec, repo string) []GroupRec {
	if repo == "" {
		return records
	}
	out := make([]GroupRec, 0, len(records))
	for _, r := range records {
		if strings.Contains(r.Item.Repo, repo) {
			out = append(out, r)
		}
	}
	return out
}

func medianHours(xs []float64) float64 {
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

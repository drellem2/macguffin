// Package flow computes throughput metrics, bottleneck signals, and blocked
// chains across mg work items. It is a read-only analytics layer over the
// event log and the work-item filesystem snapshot.
package flow

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/workitem"
)

// Statuses returned in StatusMetrics, in display order. "pending" is included
// because items can sit there waiting on dependencies; bottlenecks often look
// like a tall pending pile.
var Statuses = []string{"available", "claimed", "pending", "done"}

// StatusMetrics holds throughput and aging numbers for a single status.
type StatusMetrics struct {
	Status    string
	Count     int // current item count in this status
	In24h     int // events with to_status=this in last 24h
	Out24h    int // events with from_status=this in last 24h
	Net24h    int // In24h - Out24h
	In7d      int
	Out7d     int
	Net7d     int
	MedianAge time.Duration // median age (now - last-arrived-at-status) of current items
	OldestAge time.Duration // oldest current item's age
	OldestID  string        // id of the oldest item
}

// BlockedItem describes a work item stuck in a status long enough to be
// considered blocking, plus the items waiting on it.
type BlockedItem struct {
	ID       string
	Status   string
	Title    string
	Age      time.Duration
	Blocking []string // IDs of items whose Depends list includes this ID
}

// SpawnPressure compares queued work to live polecats.
type SpawnPressure struct {
	Available  int
	Polecats   int
	PolecatsOK bool // false if pogo agent list could not be queried
}

// Snapshot is everything `mg flow` needs for one render.
type Snapshot struct {
	GeneratedAt time.Time
	Statuses    []StatusMetrics
	Bottleneck  string // status with the worst median-age vs throughput ratio, or "" if none
	Blocked     []BlockedItem
	Spawn       SpawnPressure
}

// Options controls Compute.
type Options struct {
	Now            time.Time     // injection point for tests; defaults to time.Now
	BlockedAfter   time.Duration // items in claimed older than this are flagged; default 24h
	RepoFilter     string        // substring match against item.Repo
	PolecatFetcher func() (int, bool)
}

// Compute pulls the current snapshot of items + recent events and produces
// the analytics needed for `mg flow`. All inputs come from `root` (the
// macguffin workspace) — no network, no daemons.
func Compute(root string, opts Options) (Snapshot, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.BlockedAfter == 0 {
		opts.BlockedAfter = 24 * time.Hour
	}
	if opts.PolecatFetcher == nil {
		opts.PolecatFetcher = countRunningPolecats
	}

	itemsByStatus, err := workitem.ListAll(root)
	if err != nil {
		return Snapshot{}, err
	}
	if opts.RepoFilter != "" {
		for s, items := range itemsByStatus {
			itemsByStatus[s] = filterByRepo(items, opts.RepoFilter)
		}
	}

	allItems := flatten(itemsByStatus)

	events, err := event.List(root, event.ListOpts{})
	if err != nil {
		return Snapshot{}, err
	}
	if opts.RepoFilter != "" {
		// Limit event-derived metrics to items we know about.
		// (Archived items dropped from view; acceptable for v0.)
		filteredIDs := make(map[string]struct{}, len(allItems))
		for _, it := range allItems {
			filteredIDs[it.ID] = struct{}{}
		}
		events = filterEventsByItemIDs(events, filteredIDs)
	}

	cutoff24h := opts.Now.Add(-24 * time.Hour)
	cutoff7d := opts.Now.Add(-7 * 24 * time.Hour)

	// arrivedAt[itemID][status] = most recent event time when item entered status.
	arrivedAt := make(map[string]map[string]time.Time)
	for _, e := range events {
		to := e.Extra["to_status"]
		id := e.Extra["item_id"]
		if to == "" || id == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.Ts)
		if err != nil {
			continue
		}
		m, ok := arrivedAt[id]
		if !ok {
			m = make(map[string]time.Time)
			arrivedAt[id] = m
		}
		if t.After(m[to]) {
			m[to] = t
		}
	}

	snap := Snapshot{GeneratedAt: opts.Now}

	for _, status := range Statuses {
		items := itemsByStatus[status]
		m := StatusMetrics{Status: status, Count: len(items)}

		for _, e := range events {
			t, err := time.Parse(time.RFC3339, e.Ts)
			if err != nil {
				continue
			}
			to := e.Extra["to_status"]
			from := e.Extra["from_status"]
			if t.After(cutoff24h) {
				if to == status {
					m.In24h++
				}
				if from == status {
					m.Out24h++
				}
			}
			if t.After(cutoff7d) {
				if to == status {
					m.In7d++
				}
				if from == status {
					m.Out7d++
				}
			}
		}
		m.Net24h = m.In24h - m.Out24h
		m.Net7d = m.In7d - m.Out7d

		// Age = now - last-arrived-at-this-status; fall back to item.Created
		// if no event recorded the transition.
		var ages []time.Duration
		var oldest time.Duration
		var oldestID string
		for _, it := range items {
			ts, ok := arrivedAt[it.ID][status]
			if !ok {
				ts = it.Created
			}
			if ts.IsZero() {
				continue
			}
			age := opts.Now.Sub(ts)
			if age < 0 {
				age = 0
			}
			ages = append(ages, age)
			if age > oldest {
				oldest = age
				oldestID = it.ID
			}
		}
		m.MedianAge = median(ages)
		m.OldestAge = oldest
		m.OldestID = oldestID

		snap.Statuses = append(snap.Statuses, m)
	}

	snap.Bottleneck = pickBottleneck(snap.Statuses)
	snap.Blocked = computeBlocked(itemsByStatus["claimed"], allItems, arrivedAt, opts.Now, opts.BlockedAfter)

	available := 0
	for _, m := range snap.Statuses {
		if m.Status == "available" {
			available = m.Count
			break
		}
	}
	pcCount, ok := opts.PolecatFetcher()
	snap.Spawn = SpawnPressure{Available: available, Polecats: pcCount, PolecatsOK: ok}

	return snap, nil
}

// pickBottleneck selects the status whose median-age divided by 24h-out-rate
// is highest. Statuses with no items, or terminal "done", are skipped.
// We use median age in seconds and Out24h+1 to avoid divide-by-zero and to
// favour statuses where work is sitting longer relative to how often it leaves.
func pickBottleneck(metrics []StatusMetrics) string {
	bestRatio := 0.0
	best := ""
	for _, m := range metrics {
		if m.Count == 0 || m.Status == "done" {
			continue
		}
		ratio := float64(m.MedianAge.Seconds()) / float64(m.Out24h+1)
		if ratio > bestRatio {
			bestRatio = ratio
			best = m.Status
		}
	}
	return best
}

// computeBlocked returns claimed items older than threshold, annotated with
// the items waiting on them (Depends back-pointers).
func computeBlocked(claimed []*workitem.Item, all []*workitem.Item, arrivedAt map[string]map[string]time.Time, now time.Time, threshold time.Duration) []BlockedItem {
	if len(claimed) == 0 {
		return nil
	}

	// Reverse-index: for each item ID, list items that depend on it.
	dependents := make(map[string][]string)
	for _, it := range all {
		for _, dep := range it.Depends {
			dependents[dep] = append(dependents[dep], it.ID)
		}
	}

	var out []BlockedItem
	for _, it := range claimed {
		ts, ok := arrivedAt[it.ID]["claimed"]
		if !ok {
			ts = it.Created
		}
		if ts.IsZero() {
			continue
		}
		age := now.Sub(ts)
		if age < threshold {
			continue
		}
		blocks := append([]string(nil), dependents[it.ID]...)
		sort.Strings(blocks)
		out = append(out, BlockedItem{
			ID:       it.ID,
			Status:   "claimed",
			Title:    it.Title,
			Age:      age,
			Blocking: blocks,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Age > out[j].Age })
	return out
}

// countRunningPolecats shells out to `pogo agent list --json` and counts
// type=polecat. Returns (0, false) if pogo isn't installed or the call fails;
// flow render falls back to omitting the polecat number rather than crashing.
func countRunningPolecats() (int, bool) {
	cmd := exec.Command("pogo", "agent", "list", "--json")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	var agents []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &agents); err != nil {
		return 0, false
	}
	n := 0
	for _, a := range agents {
		if a.Type == "polecat" && (a.Status == "" || a.Status == "running") {
			n++
		}
	}
	return n, true
}

func filterByRepo(items []*workitem.Item, repo string) []*workitem.Item {
	if repo == "" {
		return items
	}
	var out []*workitem.Item
	for _, it := range items {
		if strings.Contains(it.Repo, repo) {
			out = append(out, it)
		}
	}
	return out
}

func filterEventsByItemIDs(events []event.Entry, ids map[string]struct{}) []event.Entry {
	var out []event.Entry
	for _, e := range events {
		id := e.Extra["item_id"]
		if id == "" {
			continue
		}
		if _, ok := ids[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

func flatten(grouped map[string][]*workitem.Item) []*workitem.Item {
	var out []*workitem.Item
	for _, items := range grouped {
		out = append(out, items...)
	}
	return out
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	mid := len(d) / 2
	if len(d)%2 == 1 {
		return d[mid]
	}
	return (d[mid-1] + d[mid]) / 2
}

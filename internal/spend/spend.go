package spend

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// Totals is an aggregate over a slice of Records.
type Totals struct {
	Items       map[string]struct{} `json:"-"`
	ItemCount   int                 `json:"items"`
	Input       int                 `json:"input"`
	CacheRead   int                 `json:"cache_read"`
	CacheCreate int                 `json:"cache_create"`
	Output      int                 `json:"output"`
}

// TotalIn returns input + cache_read + cache_create.
func (t Totals) TotalIn() int { return t.Input + t.CacheRead + t.CacheCreate }

// TotalOut returns output tokens.
func (t Totals) TotalOut() int { return t.Output }

// Add accumulates a record into the totals.
func (t *Totals) Add(r Record) {
	if t.Items == nil {
		t.Items = make(map[string]struct{})
	}
	if r.ItemID != "" {
		t.Items[r.ItemID] = struct{}{}
		t.ItemCount = len(t.Items)
	}
	t.Input += r.Input
	t.CacheRead += r.CacheRead
	t.CacheCreate += r.CacheCreate
	t.Output += r.Output
}

// Group is one row of grouped output (`mg spend --by <axis>`).
type Group struct {
	Key    string `json:"key"`
	Totals Totals `json:"totals"`
}

// QueryOpts controls grouping/filtering for query callers.
type QueryOpts struct {
	// By is one of: "item", "tag", "tag:<value>", "repo", "agent",
	// "priority", "assignee". Empty defaults to "item".
	By string
	// Since filters records to those with Ts >= now-Since (a rolling
	// window). Zero value disables. Ignored when SinceTime is set.
	Since time.Duration
	// SinceTime filters records to those with Ts >= SinceTime — an absolute
	// cutoff used for calendar-anchored named windows (see WindowStart).
	// Takes precedence over Since when non-zero.
	SinceTime time.Time
	// Now overrides time.Now for testing.
	Now time.Time
}

// Query reads the spend store, optionally joins with mg item metadata, and
// returns rows grouped according to opts. Rows are sorted by total tokens
// descending.
func Query(root, mgRoot string, opts QueryOpts) ([]Group, error) {
	recs, err := ReadAll(root)
	if err != nil {
		return nil, err
	}

	if cutoff := opts.cutoff(); !cutoff.IsZero() {
		recs = filterSince(recs, cutoff)
	}

	by := opts.By
	if by == "" {
		by = "item"
	}

	var items map[string]*workitem.Item
	needsItems := by == "tag" || strings.HasPrefix(by, "tag:") ||
		by == "repo" || by == "priority" || by == "assignee"
	if needsItems && mgRoot != "" {
		items, err = loadItemMap(mgRoot)
		if err != nil {
			return nil, err
		}
	}

	groups := make(map[string]*Totals)
	add := func(key string, r Record) {
		if key == "" {
			return
		}
		if groups[key] == nil {
			groups[key] = &Totals{}
		}
		groups[key].Add(r)
	}

	switch {
	case by == "item":
		for _, r := range recs {
			if r.ItemID == "" {
				add("(overhead:"+r.Agent+")", r)
				continue
			}
			add(r.ItemID, r)
		}
	case by == "agent":
		for _, r := range recs {
			add(r.Agent, r)
		}
	case by == "tag":
		for _, r := range recs {
			if r.ItemID == "" {
				continue
			}
			it := items[r.ItemID]
			if it == nil || len(it.Tags) == 0 {
				add("(untagged)", r)
				continue
			}
			for _, t := range it.Tags {
				add(t, r)
			}
		}
	case strings.HasPrefix(by, "tag:"):
		want := strings.TrimPrefix(by, "tag:")
		for _, r := range recs {
			if r.ItemID == "" {
				continue
			}
			it := items[r.ItemID]
			if it == nil {
				continue
			}
			for _, t := range it.Tags {
				if t == want {
					add(r.ItemID, r)
					break
				}
			}
		}
	case by == "repo":
		for _, r := range recs {
			if r.ItemID == "" {
				continue
			}
			it := items[r.ItemID]
			if it == nil || it.Repo == "" {
				add("(no-repo)", r)
				continue
			}
			add(it.Repo, r)
		}
	case by == "priority":
		for _, r := range recs {
			if r.ItemID == "" {
				continue
			}
			it := items[r.ItemID]
			if it == nil || it.Priority == "" {
				add("(no-priority)", r)
				continue
			}
			add(it.Priority, r)
		}
	case by == "assignee":
		for _, r := range recs {
			if r.ItemID == "" {
				continue
			}
			it := items[r.ItemID]
			if it == nil || it.Assignee == "" {
				add("(unassigned)", r)
				continue
			}
			add(it.Assignee, r)
		}
	default:
		return nil, fmt.Errorf("unknown --by axis %q", by)
	}

	out := make([]Group, 0, len(groups))
	for k, t := range groups {
		out = append(out, Group{Key: k, Totals: *t})
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].Totals.TotalIn() + out[i].Totals.TotalOut()
		tj := out[j].Totals.TotalIn() + out[j].Totals.TotalOut()
		if ti != tj {
			return ti > tj
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// cutoff resolves the effective lower time bound for a query. An absolute
// SinceTime (calendar window) wins over a rolling Since duration. A zero
// return means "no time filter".
func (o QueryOpts) cutoff() time.Time {
	if !o.SinceTime.IsZero() {
		return o.SinceTime
	}
	if o.Since > 0 {
		now := o.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		return now.Add(-o.Since)
	}
	return time.Time{}
}

func filterSince(recs []Record, cutoff time.Time) []Record {
	out := recs[:0]
	for _, r := range recs {
		if !r.Ts.Before(cutoff) {
			out = append(out, r)
		}
	}
	return out
}

// WindowStart returns the calendar-anchored start instant for a named window,
// expressed in now's location. Unlike the rolling --since durations, these
// anchor to the local calendar:
//
//	"today" — local midnight at the start of now's day.
//	"week"  — local midnight on the most recent Monday (ISO-8601 week start).
//
// The caller compares record timestamps (stored in UTC) against the returned
// instant; comparison is location-independent, so a local-anchored cutoff and
// a UTC record timestamp compare correctly.
func WindowStart(name string, now time.Time) (time.Time, error) {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch name {
	case "today":
		return midnight, nil
	case "week":
		// time.Weekday: Sunday=0..Saturday=6. Shift so Monday=0 and
		// subtract that many days to reach the week's Monday.
		offset := (int(midnight.Weekday()) + 6) % 7
		return midnight.AddDate(0, 0, -offset), nil
	default:
		return time.Time{}, fmt.Errorf("unknown window %q (want: today, week)", name)
	}
}

// GrandTotal sums every record in the store into a single Totals, optionally
// filtered to records with Ts >= since (a zero since sums all time). It spans
// both attributed (by-item) and overhead (by-agent) records, so the result is
// a true grand total of measured token consumption.
func GrandTotal(root string, since time.Time) (Totals, error) {
	recs, err := ReadAll(root)
	if err != nil {
		return Totals{}, err
	}
	var t Totals
	for _, r := range recs {
		if !since.IsZero() && r.Ts.Before(since) {
			continue
		}
		t.Add(r)
	}
	return t, nil
}

// loadItemMap reads every work item in a macguffin tree (active, shelved, and
// archived) and indexes by ID. Used by Query for tag/repo/priority/assignee
// joins.
func loadItemMap(root string) (map[string]*workitem.Item, error) {
	idx := make(map[string]*workitem.Item)
	for _, status := range []string{"available", "claimed", "done", "pending", "shelved"} {
		items, err := workitem.ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, it := range items {
			idx[it.ID] = it
		}
	}
	archived, err := workitem.ListArchived(root)
	if err == nil {
		for _, it := range archived {
			idx[it.ID] = it
		}
	}
	return idx, nil
}

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
	// Since filters records to those with Ts >= now-Since. Zero value
	// disables.
	Since time.Duration
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

	if opts.Since > 0 {
		now := opts.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		cutoff := now.Add(-opts.Since)
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

func filterSince(recs []Record, cutoff time.Time) []Record {
	out := recs[:0]
	for _, r := range recs {
		if !r.Ts.Before(cutoff) {
			out = append(out, r)
		}
	}
	return out
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

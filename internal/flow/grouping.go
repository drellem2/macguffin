package flow

import (
	"fmt"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// GroupRec pairs a work item with the metadata grouping needs that isn't
// directly on the item: its lifecycle status (derived from where it lives),
// the completion-time proxy for done items (file mtime, used for throughput
// counting), and the age bucket precomputed against the same `now` Compute
// used so age-based groupings don't have to re-derive it.
type GroupRec struct {
	Item      *workitem.Item
	Status    string    // "available" | "claimed" | "pending" | "done"
	DoneAt    time.Time // mtime of the done file; zero for active items
	AgeBucket string    // "<24h" | "24h–7d" | "7d–30d" | ">30d"
}

// Active reports whether this record is in an in-flight (non-done) status.
func (r GroupRec) Active() bool { return r.Status != "done" }

// Grouping partitions GroupRecs into named groups for `mg flow --group-by`.
//
// Implementations may pre-filter the record set (Filter) — used by
// tag:<value> to drop items missing the named tag before partitioning.
type Grouping interface {
	// Name is the canonical grouping name ("status", "tag", "tag:ux", ...).
	Name() string
	// Filter narrows the input to records this grouping considers.
	Filter([]GroupRec) []GroupRec
	// Assign returns one or more group keys for the record. Most groupings
	// return exactly one key; tag returns multiple (multi-membership).
	Assign(GroupRec) []string
	// Label is a display string for the group key.
	Label(key string) string
	// Order is the preferred display order; keys not listed sort lexically.
	Order() []string
	// ExcludesFromBottleneck skips a key during bottleneck synthesis (e.g.
	// "done" in a status grouping — destination, not stuck queue).
	ExcludesFromBottleneck(key string) bool
}

// ParseGroupBy parses --group-by. Empty string is "status".
//
// Accepted: status, repo, tag, tag:<value>, assignee, priority, age.
func ParseGroupBy(s string) (Grouping, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "status"
	}
	if strings.HasPrefix(s, "tag:") {
		val := strings.TrimSpace(strings.TrimPrefix(s, "tag:"))
		if val == "" {
			return nil, fmt.Errorf("--group-by tag:<value> requires a non-empty value")
		}
		return tagFilterGrouping{value: val}, nil
	}
	switch s {
	case "status":
		return statusGrouping{}, nil
	case "repo":
		return repoGrouping{}, nil
	case "tag":
		return tagGrouping{}, nil
	case "assignee":
		return assigneeGrouping{}, nil
	case "priority":
		return priorityGrouping{}, nil
	case "age":
		return ageGrouping{}, nil
	}
	return nil, fmt.Errorf("unknown --group-by value %q (want: status, repo, tag, tag:<v>, assignee, priority, age)", s)
}

// --- status ---

type statusGrouping struct{}

func (statusGrouping) Name() string                           { return "status" }
func (statusGrouping) Filter(in []GroupRec) []GroupRec        { return in }
func (statusGrouping) Assign(r GroupRec) []string             { return []string{r.Status} }
func (statusGrouping) Label(key string) string                { return key }
func (statusGrouping) Order() []string                        { return []string{"available", "claimed", "pending", "done"} }
func (statusGrouping) ExcludesFromBottleneck(key string) bool { return key == "done" }

// --- repo ---

type repoGrouping struct{}

func (repoGrouping) Name() string                    { return "repo" }
func (repoGrouping) Filter(in []GroupRec) []GroupRec { return in }
func (repoGrouping) Assign(r GroupRec) []string {
	if r.Item.Repo == "" {
		return []string{"(no repo)"}
	}
	return []string{r.Item.Repo}
}
func (repoGrouping) Label(key string) string                { return key }
func (repoGrouping) Order() []string                        { return nil }
func (repoGrouping) ExcludesFromBottleneck(key string) bool { return false }

// --- tag (multi-membership) ---

type tagGrouping struct{}

func (tagGrouping) Name() string                    { return "tag" }
func (tagGrouping) Filter(in []GroupRec) []GroupRec { return in }
func (tagGrouping) Assign(r GroupRec) []string {
	if len(r.Item.Tags) == 0 {
		return []string{"(untagged)"}
	}
	return r.Item.Tags
}
func (tagGrouping) Label(key string) string {
	if key == "(untagged)" {
		return key
	}
	// Items with N tags appear in N rows; the asterisk warns that counts
	// across rows can sum to more than the underlying item count.
	return key + " *"
}
func (tagGrouping) Order() []string                        { return nil }
func (tagGrouping) ExcludesFromBottleneck(key string) bool { return false }

// --- tag:<value> (filter then sub-group by status) ---

type tagFilterGrouping struct {
	value string
}

func (g tagFilterGrouping) Name() string { return "tag:" + g.value }
func (g tagFilterGrouping) Filter(in []GroupRec) []GroupRec {
	out := make([]GroupRec, 0, len(in))
	needle := strings.ToLower(g.value)
	for _, r := range in {
		for _, t := range r.Item.Tags {
			if strings.Contains(strings.ToLower(t), needle) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}
func (g tagFilterGrouping) Assign(r GroupRec) []string { return []string{r.Status} }
func (g tagFilterGrouping) Label(key string) string    { return key }
func (g tagFilterGrouping) Order() []string {
	return []string{"available", "claimed", "pending", "done"}
}
func (g tagFilterGrouping) ExcludesFromBottleneck(key string) bool { return key == "done" }

// --- assignee ---

type assigneeGrouping struct{}

func (assigneeGrouping) Name() string                    { return "assignee" }
func (assigneeGrouping) Filter(in []GroupRec) []GroupRec { return in }
func (assigneeGrouping) Assign(r GroupRec) []string {
	if r.Item.Assignee == "" {
		return []string{"(unassigned)"}
	}
	return []string{r.Item.Assignee}
}
func (assigneeGrouping) Label(key string) string                { return key }
func (assigneeGrouping) Order() []string                        { return nil }
func (assigneeGrouping) ExcludesFromBottleneck(key string) bool { return false }

// --- priority ---

type priorityGrouping struct{}

func (priorityGrouping) Name() string                    { return "priority" }
func (priorityGrouping) Filter(in []GroupRec) []GroupRec { return in }
func (priorityGrouping) Assign(r GroupRec) []string {
	p := strings.ToLower(strings.TrimSpace(r.Item.Priority))
	if p == "" {
		p = "medium"
	}
	return []string{p}
}
func (priorityGrouping) Label(key string) string                { return key }
func (priorityGrouping) Order() []string                        { return []string{"high", "medium", "low"} }
func (priorityGrouping) ExcludesFromBottleneck(key string) bool { return false }

// --- age ---

type ageGrouping struct{}

func (ageGrouping) Name() string                           { return "age" }
func (ageGrouping) Filter(in []GroupRec) []GroupRec        { return in }
func (ageGrouping) Assign(r GroupRec) []string             { return []string{r.AgeBucket} }
func (ageGrouping) Label(key string) string                { return key }
func (ageGrouping) Order() []string                        { return AgeBuckets }
func (ageGrouping) ExcludesFromBottleneck(key string) bool { return false }

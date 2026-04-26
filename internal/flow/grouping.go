package flow

import (
	"fmt"
	"strings"
)

// Grouping partitions ItemRecs into named groups for flow reporting.
//
// Implementations may also pre-filter the record set (Filter) — used by
// tag:<value> to drop items missing the named tag before partitioning.
type Grouping interface {
	// Name returns the canonical grouping name (e.g. "status", "tag",
	// "tag:ux"). Used in result metadata.
	Name() string
	// Filter narrows the input to records this grouping considers; called
	// once before Assign. The default for most groupings is identity.
	Filter([]ItemRec) []ItemRec
	// Assign returns one or more group keys for the record. Most groupings
	// return exactly one key; tag groupings may return multiple (an item
	// with two tags appears in two groups).
	Assign(ItemRec) []string
	// Label returns a display string for the group key.
	Label(key string) string
	// Order returns a preferred display order for keys; keys not in this
	// list fall back to lexicographic ordering.
	Order() []string
	// ExcludesFromBottleneck reports whether the given key should be
	// skipped during bottleneck synthesis (e.g. "done" in a status group).
	ExcludesFromBottleneck(key string) bool
}

// ParseGroupBy parses the --group-by flag value. Empty string maps to
// "status" (the default).
//
// Accepted values:
//
//	status              partition by lifecycle state (default)
//	repo                partition by item.Repo
//	tag                 explode by tag (multi-membership)
//	tag:<value>         filter to items with <value> in their tags, sub-group by status
//	assignee            partition by item.Assignee (or "unassigned")
//	priority            partition by item.Priority (low/medium/high)
//	age                 partition by age bucket (<24h, 24h-7d, 7d-30d, >30d)
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

// identityFilter is the default Filter for groupings that don't drop records.
func identityFilter(in []ItemRec) []ItemRec { return in }

// --- status ---

type statusGrouping struct{}

func (statusGrouping) Name() string                           { return "status" }
func (statusGrouping) Filter(in []ItemRec) []ItemRec          { return in }
func (statusGrouping) Assign(r ItemRec) []string              { return []string{r.Status} }
func (statusGrouping) Label(key string) string                { return key }
func (statusGrouping) Order() []string                        { return []string{"available", "claimed", "pending", "done"} }
func (statusGrouping) ExcludesFromBottleneck(key string) bool { return key == "done" }

// --- repo ---

type repoGrouping struct{}

func (repoGrouping) Name() string                  { return "repo" }
func (repoGrouping) Filter(in []ItemRec) []ItemRec { return in }
func (repoGrouping) Assign(r ItemRec) []string {
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

func (tagGrouping) Name() string                  { return "tag" }
func (tagGrouping) Filter(in []ItemRec) []ItemRec { return in }
func (tagGrouping) Assign(r ItemRec) []string {
	if len(r.Item.Tags) == 0 {
		return []string{"(untagged)"}
	}
	return r.Item.Tags
}
func (tagGrouping) Label(key string) string {
	if key == "(untagged)" {
		return key
	}
	// Items with multiple tags appear in multiple rows; mark the row so
	// readers know counts can sum to more than the underlying item count.
	return key + " *"
}
func (tagGrouping) Order() []string                        { return nil }
func (tagGrouping) ExcludesFromBottleneck(key string) bool { return false }

// --- tag:<value> (filter then sub-group by status) ---

type tagFilterGrouping struct {
	value string
}

func (g tagFilterGrouping) Name() string { return "tag:" + g.value }
func (g tagFilterGrouping) Filter(in []ItemRec) []ItemRec {
	out := make([]ItemRec, 0, len(in))
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
func (g tagFilterGrouping) Assign(r ItemRec) []string { return []string{r.Status} }
func (g tagFilterGrouping) Label(key string) string   { return key }
func (g tagFilterGrouping) Order() []string {
	return []string{"available", "claimed", "pending", "done"}
}
func (g tagFilterGrouping) ExcludesFromBottleneck(key string) bool { return key == "done" }

// --- assignee ---

type assigneeGrouping struct{}

func (assigneeGrouping) Name() string                  { return "assignee" }
func (assigneeGrouping) Filter(in []ItemRec) []ItemRec { return in }
func (assigneeGrouping) Assign(r ItemRec) []string {
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

func (priorityGrouping) Name() string                  { return "priority" }
func (priorityGrouping) Filter(in []ItemRec) []ItemRec { return in }
func (priorityGrouping) Assign(r ItemRec) []string {
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

func (ageGrouping) Name() string                  { return "age" }
func (ageGrouping) Filter(in []ItemRec) []ItemRec { return in }
func (ageGrouping) Assign(r ItemRec) []string     { return []string{r.AgeBucket} }
func (ageGrouping) Label(key string) string       { return key }
func (ageGrouping) Order() []string               { return AgeBuckets }
func (ageGrouping) ExcludesFromBottleneck(key string) bool {
	// >30d is interesting on its own — don't exclude. Bottleneck synthesis
	// will naturally pick the bucket with the worst ratio.
	return false
}

// keep identityFilter referenced so it can be a future drop-in for groupings
// that today inline `func(in) []ItemRec { return in }`.
var _ = identityFilter

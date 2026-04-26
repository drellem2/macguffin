package flow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeItemTagged(t *testing.T, root, status, id, title string, repo string, tags []string, assignee, priority string, created time.Time) {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("id: " + id + "\n")
	b.WriteString("type: task\n")
	b.WriteString("created: " + created.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("creator: tester\n")
	b.WriteString("depends: []\n")
	if repo != "" {
		b.WriteString("repo: " + repo + "\n")
	}
	if assignee != "" {
		b.WriteString("assignee: " + assignee + "\n")
	}
	if priority != "" {
		b.WriteString("priority: " + priority + "\n")
	}
	if len(tags) > 0 {
		b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	}
	b.WriteString("---\n\n# " + title + "\n")
	path := filepath.Join(root, "work", status, id+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write item: %v", err)
	}
}

func TestComputeGrouped_ByRepo(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "claimed", "mg-a", "a", "/repo/one", nil, "", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-b", "b", "/repo/one", nil, "", "", now.Add(-3*time.Hour))
	writeItemTagged(t, root, "available", "mg-c", "c", "/repo/two", nil, "", "", now.Add(-1*time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "repo"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}

	if snap.GroupBy != "repo" {
		t.Errorf("GroupBy = %q, want %q", snap.GroupBy, "repo")
	}

	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["/repo/one"] != 2 {
		t.Errorf("/repo/one active = %d, want 2", got["/repo/one"])
	}
	if got["/repo/two"] != 1 {
		t.Errorf("/repo/two active = %d, want 1", got["/repo/two"])
	}
}

func TestComputeGrouped_ByTagMultiMembership(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "claimed", "mg-1", "one", "", []string{"ux", "infra"}, "", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-2", "two", "", []string{"ux"}, "", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "available", "mg-3", "three", "", nil, "", "", now)

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "tag"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}

	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["ux"] != 2 {
		t.Errorf("ux active = %d, want 2 (mg-1 + mg-2)", got["ux"])
	}
	if got["infra"] != 1 {
		t.Errorf("infra active = %d, want 1 (mg-1)", got["infra"])
	}
	if got["(untagged)"] != 1 {
		t.Errorf("(untagged) active = %d, want 1 (mg-3)", got["(untagged)"])
	}

	// The "ux *" suffix marks multi-membership rows.
	var foundLabel string
	for _, g := range snap.Groups {
		if g.Key == "ux" {
			foundLabel = g.Label
		}
	}
	if !strings.HasSuffix(foundLabel, " *") {
		t.Errorf("ux label = %q, want suffix %q", foundLabel, " *")
	}
}

func TestComputeGrouped_TagFilter(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "available", "mg-ux1", "a", "", []string{"ux"}, "", "", now)
	writeItemTagged(t, root, "claimed", "mg-ux2", "b", "", []string{"ux"}, "", "", now.Add(-time.Hour))
	writeItemTagged(t, root, "claimed", "mg-other", "c", "", []string{"infra"}, "", "", now.Add(-time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "tag:ux"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}
	if snap.GroupBy != "tag:ux" {
		t.Errorf("GroupBy = %q, want tag:ux", snap.GroupBy)
	}

	// tag:ux filters to ux-tagged items, then sub-groups by status.
	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["available"] != 1 || got["claimed"] != 1 {
		t.Errorf("tag:ux groups = %+v, want available=1 claimed=1 (infra item filtered out)", got)
	}
	if snap.TotalActive != 2 {
		t.Errorf("TotalActive = %d, want 2 (mg-other filtered out)", snap.TotalActive)
	}
}

func TestComputeGrouped_TagFilter_EmptyValueErrors(t *testing.T) {
	if _, err := ParseGroupBy("tag:"); err == nil {
		t.Error("ParseGroupBy(tag:) should error on empty value")
	}
	if _, err := ParseGroupBy("tag:   "); err == nil {
		t.Error("ParseGroupBy(tag:   ) should error on whitespace-only value")
	}
}

func TestComputeGrouped_ByAssignee(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "claimed", "mg-d", "d", "", nil, "daniel", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-h", "h", "", nil, "human", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "available", "mg-u", "u", "", nil, "", "", now)

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "assignee"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}

	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["daniel"] != 1 || got["human"] != 1 || got["(unassigned)"] != 1 {
		t.Errorf("assignee groups = %+v", got)
	}
}

func TestComputeGrouped_ByPriority(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "claimed", "mg-h1", "h1", "", nil, "", "high", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-l1", "l1", "", nil, "", "low", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-d1", "d1", "", nil, "", "", now.Add(-2*time.Hour)) // default = medium

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "priority"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}

	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["high"] != 1 || got["medium"] != 1 || got["low"] != 1 {
		t.Errorf("priority groups = %+v", got)
	}

	// high/medium/low display order.
	if len(snap.Groups) != 3 ||
		snap.Groups[0].Key != "high" ||
		snap.Groups[1].Key != "medium" ||
		snap.Groups[2].Key != "low" {
		var keys []string
		for _, g := range snap.Groups {
			keys = append(keys, g.Key)
		}
		t.Errorf("priority order = %v, want [high medium low]", keys)
	}
}

func TestComputeGrouped_ByAge(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "available", "mg-fresh", "fresh", "", nil, "", "", now.Add(-1*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-3d", "three days", "", nil, "", "", now.Add(-3*24*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-10d", "ten days", "", nil, "", "", now.Add(-10*24*time.Hour))
	writeItemTagged(t, root, "pending", "mg-60d", "old", "", nil, "", "", now.Add(-60*24*time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "age"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}

	got := map[string]int{}
	for _, g := range snap.Groups {
		got[g.Key] = g.Active
	}
	if got["<24h"] != 1 || got["24h–7d"] != 1 || got["7d–30d"] != 1 || got[">30d"] != 1 {
		t.Errorf("age groups = %+v", got)
	}
}

func TestComputeGrouped_RepoFilterComposes(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItemTagged(t, root, "claimed", "mg-keep", "keep", "/repo/keep", []string{"ux"}, "", "", now.Add(-2*time.Hour))
	writeItemTagged(t, root, "claimed", "mg-drop", "drop", "/repo/drop", []string{"ux"}, "", "", now.Add(-2*time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "tag:ux", RepoFilter: "/repo/keep"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}
	if snap.TotalActive != 1 {
		t.Errorf("TotalActive = %d, want 1 (drop filtered by --repo)", snap.TotalActive)
	}
}

func TestComputeGrouped_BottleneckSkipsDoneInStatus(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// A pile in pending with no done7d throughput.
	for i, id := range []string{"mg-p1", "mg-p2", "mg-p3"} {
		writeItemTagged(t, root, "pending", id, "p", "", nil, "", "", now.Add(-time.Duration(5+i)*24*time.Hour))
	}
	writeItemTagged(t, root, "claimed", "mg-c1", "c", "", nil, "", "", now.Add(-1*time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "status"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}
	if snap.Bottleneck != "pending" {
		t.Errorf("Bottleneck = %q, want pending", snap.Bottleneck)
	}
}

func TestComputeAgeDistribution(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	recs := []GroupRec{
		{AgeBucket: "<24h"},
		{AgeBucket: "<24h"},
		{AgeBucket: "24h–7d"},
		{AgeBucket: "7d–30d"},
		{AgeBucket: ">30d"},
		{AgeBucket: ">30d"},
	}
	d := ComputeAgeDistribution(recs)
	if d.Total != 6 {
		t.Errorf("Total = %d, want 6", d.Total)
	}
	if d.LessThan24h != 2 || d.OneDayToWeek != 1 || d.OneWeekToMonth != 1 || d.OverAMonth != 2 {
		t.Errorf("distribution = %+v", d)
	}

	// Counts by label.
	if d.Count("<24h") != 2 || d.Count(">30d") != 2 {
		t.Errorf("Count() returned wrong values: %+v", d)
	}
	if d.Count("nonsense") != 0 {
		t.Errorf("Count(nonsense) should be 0")
	}

	_ = now
}

func TestRenderAgeDistribution_FormatsBucketLines(t *testing.T) {
	dist := AgeDistribution{LessThan24h: 4, OneDayToWeek: 2, OneWeekToMonth: 1, OverAMonth: 0, Total: 7}
	var buf bytes.Buffer
	RenderAgeDistribution(&buf, dist)
	out := buf.String()
	for _, want := range []string{"age distribution:", "<24h", "24h–7d", "7d–30d", ">30d"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderAgeDistribution missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderAgeDistribution_EmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	RenderAgeDistribution(&buf, AgeDistribution{})
	if !strings.Contains(buf.String(), "(no items)") {
		t.Errorf("expected (no items) message, got: %s", buf.String())
	}
}

func TestRenderGrouped_IncludesHeaderAndBottleneckLine(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItemTagged(t, root, "claimed", "mg-r1", "r1", "/repo/x", nil, "", "", now.Add(-3*24*time.Hour))

	snap, err := ComputeGrouped(root, GroupedOptions{Now: now, GroupBy: "repo"})
	if err != nil {
		t.Fatalf("ComputeGrouped: %v", err)
	}
	var buf bytes.Buffer
	RenderGrouped(&buf, snap, true)
	out := buf.String()

	for _, want := range []string{
		"mg flow @",
		"group-by repo",
		"group",
		"active",
		"done7d",
		"med-age",
		"highest median-age-to-throughput ratio",
		"/repo/x",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderGrouped missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestParseGroupBy_DefaultsToStatus(t *testing.T) {
	g, err := ParseGroupBy("")
	if err != nil {
		t.Fatalf("ParseGroupBy(\"\"): %v", err)
	}
	if g.Name() != "status" {
		t.Errorf("default Name() = %q, want status", g.Name())
	}
}

func TestParseGroupBy_RejectsUnknown(t *testing.T) {
	if _, err := ParseGroupBy("nonsense"); err == nil {
		t.Error("ParseGroupBy(nonsense) should error")
	}
}

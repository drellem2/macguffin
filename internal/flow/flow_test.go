package flow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// setupDirs creates the standard work/* directory tree under root.
func setupDirs(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{
		filepath.Join(root, "work", "available"),
		filepath.Join(root, "work", "claimed"),
		filepath.Join(root, "work", "done"),
		filepath.Join(root, "work", "pending"),
		filepath.Join(root, "work", "shelved"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

// writeItem writes a work item directly into the given status directory at
// the given creation time. Returns the item.
func writeItem(t *testing.T, root, status, id, title string, created time.Time, opts ...itemOpt) *workitem.Item {
	t.Helper()
	it := &workitem.Item{
		ID:      id,
		Type:    "task",
		Created: created,
		Creator: "test",
		Title:   title,
	}
	for _, o := range opts {
		o(it)
	}
	path := filepath.Join(root, "work", status, id+".md")
	if status == "claimed" {
		path += ".42"
	}
	if err := os.WriteFile(path, []byte(workitem.Render(it)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return it
}

type itemOpt func(*workitem.Item)

func withRepo(r string) itemOpt     { return func(it *workitem.Item) { it.Repo = r } }
func withTags(ts ...string) itemOpt { return func(it *workitem.Item) { it.Tags = ts } }
func withAssignee(a string) itemOpt { return func(it *workitem.Item) { it.Assignee = a } }
func withPriority(p string) itemOpt { return func(it *workitem.Item) { it.Priority = p } }

// touchMtime sets the file's mtime to the given time. Used to control the
// completion-time proxy for done items in tests.
func touchMtime(t *testing.T, root, status, id string, when time.Time) {
	t.Helper()
	path := filepath.Join(root, "work", status, id+".md")
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// findGroup returns the metrics for the named group, or fails the test.
func findGroup(t *testing.T, res *Result, key string) GroupMetrics {
	t.Helper()
	for _, m := range res.Groups {
		if m.Key == key {
			return m
		}
	}
	t.Fatalf("group %q not found in result; got %d groups", key, len(res.Groups))
	return GroupMetrics{}
}

func TestComputeStatusGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// 2 available (one fresh, one old), 1 claimed, 1 pending (very old), 2 done (one recent, one old).
	writeItem(t, root, "available", "mg-a01", "fresh", now.Add(-2*time.Hour))
	writeItem(t, root, "available", "mg-a02", "stale", now.Add(-10*24*time.Hour))
	writeItem(t, root, "claimed", "mg-c01", "in progress", now.Add(-5*time.Hour))
	writeItem(t, root, "pending", "mg-p01", "blocked", now.Add(-40*24*time.Hour))
	writeItem(t, root, "done", "mg-d01", "shipped recent", now.Add(-3*24*time.Hour))
	touchMtime(t, root, "done", "mg-d01", now.Add(-2*24*time.Hour))
	writeItem(t, root, "done", "mg-d02", "shipped old", now.Add(-15*24*time.Hour))
	touchMtime(t, root, "done", "mg-d02", now.Add(-10*24*time.Hour))

	res, err := Compute(Options{Root: root, Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.GroupBy != "status" {
		t.Errorf("GroupBy = %q, want status", res.GroupBy)
	}

	avail := findGroup(t, res, "available")
	if avail.Active != 2 {
		t.Errorf("available.Active = %d, want 2", avail.Active)
	}
	claimed := findGroup(t, res, "claimed")
	if claimed.Active != 1 {
		t.Errorf("claimed.Active = %d, want 1", claimed.Active)
	}
	pending := findGroup(t, res, "pending")
	if pending.Active != 1 {
		t.Errorf("pending.Active = %d, want 1", pending.Active)
	}

	// Done group: only the recent done counts toward Done7d (the 10-day-old
	// one should be loaded — it's within 4*7d window — but its mtime is
	// outside the 7d window so Done7d=1).
	done := findGroup(t, res, "done")
	if done.Done7d != 1 {
		t.Errorf("done.Done7d = %d, want 1", done.Done7d)
	}

	// Bottleneck: pending has the oldest item by far and zero throughput,
	// so it should win the ratio.
	if res.Bottleneck != "pending" {
		t.Errorf("Bottleneck = %q, want pending (got groups: %+v)", res.Bottleneck, res.Groups)
	}

	// Status grouping should display in canonical order, even though
	// "claimed" and "pending" came in different file orders.
	want := []string{"available", "claimed", "pending", "done"}
	if len(res.Groups) != len(want) {
		t.Fatalf("got %d groups, want %d", len(res.Groups), len(want))
	}
	for i, m := range res.Groups {
		if m.Key != want[i] {
			t.Errorf("Groups[%d].Key = %q, want %q", i, m.Key, want[i])
		}
	}
}

func TestComputeNoFlagsRendersExpectedHeader(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour))

	res, err := Compute(Options{Root: root, Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var buf bytes.Buffer
	Render(&buf, res, nil)
	out := buf.String()

	// Spec: replace "🔥 BOTTLENECK" with the softer phrase.
	if strings.Contains(out, "BOTTLENECK") {
		t.Errorf("output still contains BOTTLENECK marker:\n%s", out)
	}
	if !strings.Contains(out, "highest median-age-to-throughput ratio") {
		t.Errorf("output missing softened bottleneck phrase:\n%s", out)
	}
}

func TestComputeRepoGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour), withRepo("/dev/foo"))
	writeItem(t, root, "available", "mg-a02", "y", now.Add(-2*time.Hour), withRepo("/dev/foo"))
	writeItem(t, root, "claimed", "mg-c01", "z", now.Add(-3*time.Hour), withRepo("/dev/bar"))
	writeItem(t, root, "available", "mg-a03", "u", now.Add(-time.Hour)) // no repo

	res, err := Compute(Options{Root: root, GroupBy: "repo", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	foo := findGroup(t, res, "/dev/foo")
	if foo.Active != 2 {
		t.Errorf("/dev/foo.Active = %d, want 2", foo.Active)
	}
	bar := findGroup(t, res, "/dev/bar")
	if bar.Active != 1 {
		t.Errorf("/dev/bar.Active = %d, want 1", bar.Active)
	}
	none := findGroup(t, res, "(no repo)")
	if none.Active != 1 {
		t.Errorf("(no repo).Active = %d, want 1", none.Active)
	}
}

func TestComputeTagGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour), withTags("ux", "infra"))
	writeItem(t, root, "available", "mg-a02", "y", now.Add(-2*time.Hour), withTags("ux"))
	writeItem(t, root, "claimed", "mg-c01", "z", now.Add(-3*time.Hour))

	res, err := Compute(Options{Root: root, GroupBy: "tag", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	ux := findGroup(t, res, "ux")
	if ux.Active != 2 {
		t.Errorf("ux.Active = %d, want 2", ux.Active)
	}
	infra := findGroup(t, res, "infra")
	if infra.Active != 1 {
		t.Errorf("infra.Active = %d, want 1", infra.Active)
	}
	untagged := findGroup(t, res, "(untagged)")
	if untagged.Active != 1 {
		t.Errorf("(untagged).Active = %d, want 1", untagged.Active)
	}

	// Multi-tag rows should be marked.
	if !strings.HasSuffix(ux.Label, "*") {
		t.Errorf("ux.Label = %q, want trailing * marker", ux.Label)
	}
}

func TestComputeTagFilterGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// Two items with ux tag, one without. Tag-filter should narrow to ux
	// items and sub-group by status.
	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour), withTags("ux"))
	writeItem(t, root, "claimed", "mg-c01", "y", now.Add(-2*time.Hour), withTags("ux", "other"))
	writeItem(t, root, "available", "mg-a02", "z", now.Add(-3*time.Hour), withTags("backend"))

	res, err := Compute(Options{Root: root, GroupBy: "tag:ux", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.GroupBy != "tag:ux" {
		t.Errorf("GroupBy = %q, want tag:ux", res.GroupBy)
	}
	if res.TotalActive != 2 {
		t.Errorf("TotalActive = %d, want 2 (tag-filter should drop non-ux items)", res.TotalActive)
	}

	avail := findGroup(t, res, "available")
	if avail.Active != 1 {
		t.Errorf("available.Active = %d, want 1", avail.Active)
	}
	claimed := findGroup(t, res, "claimed")
	if claimed.Active != 1 {
		t.Errorf("claimed.Active = %d, want 1", claimed.Active)
	}
}

func TestComputeTagFilterRequiresValue(t *testing.T) {
	_, err := ParseGroupBy("tag:")
	if err == nil {
		t.Error("expected error for tag: with empty value")
	}
}

func TestComputeAssigneeGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour), withAssignee("alice"))
	writeItem(t, root, "claimed", "mg-c01", "y", now.Add(-2*time.Hour), withAssignee("alice"))
	writeItem(t, root, "available", "mg-a02", "z", now.Add(-3*time.Hour), withAssignee("bob"))
	writeItem(t, root, "available", "mg-a03", "w", now.Add(-time.Hour))

	res, err := Compute(Options{Root: root, GroupBy: "assignee", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	alice := findGroup(t, res, "alice")
	if alice.Active != 2 {
		t.Errorf("alice.Active = %d, want 2", alice.Active)
	}
	bob := findGroup(t, res, "bob")
	if bob.Active != 1 {
		t.Errorf("bob.Active = %d, want 1", bob.Active)
	}
	un := findGroup(t, res, "(unassigned)")
	if un.Active != 1 {
		t.Errorf("(unassigned).Active = %d, want 1", un.Active)
	}
}

func TestComputePriorityGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour), withPriority("high"))
	writeItem(t, root, "available", "mg-a02", "y", now.Add(-time.Hour), withPriority("low"))
	writeItem(t, root, "available", "mg-a03", "z", now.Add(-time.Hour)) // empty -> medium

	res, err := Compute(Options{Root: root, GroupBy: "priority", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if findGroup(t, res, "high").Active != 1 {
		t.Errorf("high.Active mismatch")
	}
	if findGroup(t, res, "medium").Active != 1 {
		t.Errorf("medium.Active mismatch")
	}
	if findGroup(t, res, "low").Active != 1 {
		t.Errorf("low.Active mismatch")
	}

	// Display order: high, medium, low.
	want := []string{"high", "medium", "low"}
	for i, m := range res.Groups {
		if m.Key != want[i] {
			t.Errorf("priority order: Groups[%d] = %q, want %q", i, m.Key, want[i])
		}
	}
}

func TestComputeAgeGrouping(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "fresh", now.Add(-2*time.Hour))
	writeItem(t, root, "available", "mg-a02", "day", now.Add(-3*24*time.Hour))
	writeItem(t, root, "available", "mg-a03", "week", now.Add(-10*24*time.Hour))
	writeItem(t, root, "available", "mg-a04", "month", now.Add(-60*24*time.Hour))

	res, err := Compute(Options{Root: root, GroupBy: "age", Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if findGroup(t, res, "<24h").Active != 1 {
		t.Errorf("<24h mismatch")
	}
	if findGroup(t, res, "24h–7d").Active != 1 {
		t.Errorf("24h-7d mismatch")
	}
	if findGroup(t, res, "7d–30d").Active != 1 {
		t.Errorf("7d-30d mismatch")
	}
	if findGroup(t, res, ">30d").Active != 1 {
		t.Errorf(">30d mismatch")
	}
}

func TestComputeWithRepoFilterAndTagGroup(t *testing.T) {
	// The composition the spec calls out: --repo + --group-by tag:<v>.
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour),
		withRepo("/dev/pogo"), withTags("pogo", "infra"))
	writeItem(t, root, "claimed", "mg-c01", "y", now.Add(-2*time.Hour),
		withRepo("/dev/pogo"), withTags("pogo"))
	writeItem(t, root, "available", "mg-a02", "z", now.Add(-3*time.Hour),
		withRepo("/dev/other"), withTags("pogo"))

	res, err := Compute(Options{
		Root:    root,
		GroupBy: "tag:pogo",
		Repo:    "/dev/pogo",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.TotalActive != 2 {
		t.Errorf("TotalActive = %d, want 2 (repo-filter then tag-filter)", res.TotalActive)
	}
}

func TestAgeDistribution(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "fresh", now.Add(-2*time.Hour))
	writeItem(t, root, "available", "mg-a02", "fresh2", now.Add(-3*time.Hour))
	writeItem(t, root, "available", "mg-a03", "week", now.Add(-3*24*time.Hour))
	writeItem(t, root, "available", "mg-a04", "old", now.Add(-100*24*time.Hour))

	records, err := LoadAllRecords(root, now)
	if err != nil {
		t.Fatalf("LoadAllRecords: %v", err)
	}

	d := ComputeAgeDistribution(records)
	if d.LessThan24h != 2 {
		t.Errorf("LessThan24h = %d, want 2", d.LessThan24h)
	}
	if d.OneDayToWeek != 1 {
		t.Errorf("OneDayToWeek = %d, want 1", d.OneDayToWeek)
	}
	if d.OverAMonth != 1 {
		t.Errorf("OverAMonth = %d, want 1", d.OverAMonth)
	}
	if d.Total != 4 {
		t.Errorf("Total = %d, want 4", d.Total)
	}
}

func TestParseGroupByErrors(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", false}, // default to status
		{"status", false},
		{"repo", false},
		{"tag", false},
		{"tag:ux", false},
		{"tag:", true},
		{"assignee", false},
		{"priority", false},
		{"age", false},
		{"bogus", true},
	}
	for _, tc := range cases {
		_, err := ParseGroupBy(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseGroupBy(%q) err = %v, wantErr = %v", tc.in, err, tc.wantErr)
		}
	}
}

func TestRenderIncludesAgeDistributionWhenSet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "available", "mg-a01", "x", now.Add(-time.Hour))

	res, err := Compute(Options{Root: root, Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	records, _ := LoadAllRecords(root, now)
	d := ComputeAgeDistribution(records)

	var withDist bytes.Buffer
	Render(&withDist, res, &d)
	if !strings.Contains(withDist.String(), "Age distribution") {
		t.Errorf("expected histogram header in output:\n%s", withDist.String())
	}

	var withoutDist bytes.Buffer
	Render(&withoutDist, res, nil)
	if strings.Contains(withoutDist.String(), "Age distribution") {
		t.Errorf("histogram should be omitted when dist=nil:\n%s", withoutDist.String())
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		hours float64
		want  string
	}{
		{0, "—"},
		{0.25, "15m"},
		{5, "5h"},
		{24, "1d"},
		{30, "1d 6h"},
		{72, "3d"},
	}
	for _, tc := range cases {
		got := FormatDuration(tc.hours)
		if got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.hours, got, tc.want)
		}
	}
}

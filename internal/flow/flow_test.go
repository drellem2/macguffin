package flow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupRoot creates a fake macguffin workspace with the canonical work
// subdirectories. Tests then drop work-item .md files into the right subdir
// and append events.jsonl entries directly.
func setupRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"available", "claimed", "done", "pending", "shelved"} {
		if err := os.MkdirAll(filepath.Join(root, "work", d), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return root
}

func writeItem(t *testing.T, root, status, id, title string, depends []string, repo string, created time.Time) {
	t.Helper()
	depsLine := "[]"
	if len(depends) > 0 {
		depsLine = "[" + strings.Join(depends, ", ") + "]"
	}
	repoLine := ""
	if repo != "" {
		repoLine = "repo: " + repo + "\n"
	}
	content := fmt.Sprintf("---\nid: %s\ntype: task\ncreated: %s\ncreator: tester\ndepends: %s\n%s---\n\n# %s\n",
		id, created.UTC().Format(time.RFC3339), depsLine, repoLine, title)
	path := filepath.Join(root, "work", status, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write item: %v", err)
	}
}

func writeEvent(t *testing.T, root, eventType, itemID, from, to string, ts time.Time) {
	t.Helper()
	line := fmt.Sprintf(`{"ts":"%s","type":"%s","item_id":"%s","from_status":"%s","to_status":"%s","actor":"tester"}`+"\n",
		ts.UTC().Format(time.RFC3339), eventType, itemID, from, to)
	f, err := os.OpenFile(filepath.Join(root, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write event: %v", err)
	}
}

func fakePolecats(n int) func() (int, bool) {
	return func() (int, bool) { return n, true }
}

func TestCompute_EmptyWorkspace(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, m := range snap.Statuses {
		if m.Count != 0 {
			t.Errorf("status %s should be empty, got %d", m.Status, m.Count)
		}
	}
	if snap.Bottleneck != "" {
		t.Errorf("expected no bottleneck on empty workspace, got %q", snap.Bottleneck)
	}
	if len(snap.Blocked) != 0 {
		t.Errorf("expected no blocked items, got %v", snap.Blocked)
	}
	if snap.Spawn.Available != 0 {
		t.Errorf("expected available=0, got %d", snap.Spawn.Available)
	}
}

func TestCompute_PerStatusThroughput(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// Two items currently in claimed; entered claimed 2h ago.
	writeItem(t, root, "claimed", "mg-001", "first", nil, "", now.Add(-3*time.Hour))
	writeItem(t, root, "claimed", "mg-002", "second", nil, "", now.Add(-3*time.Hour))
	writeEvent(t, root, "work.claim", "mg-001", "available", "claimed", now.Add(-2*time.Hour))
	writeEvent(t, root, "work.claim", "mg-002", "available", "claimed", now.Add(-2*time.Hour))
	// One item completed 1h ago.
	writeItem(t, root, "done", "mg-003", "third", nil, "", now.Add(-5*time.Hour))
	writeEvent(t, root, "work.done", "mg-003", "claimed", "done", now.Add(-time.Hour))
	// One stale event 8 days ago — should land in neither 24h nor 7d window.
	writeEvent(t, root, "work.done", "mg-old", "claimed", "done", now.Add(-8*24*time.Hour))

	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(1)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	want := map[string]struct{ in24, out24 int }{
		"available": {0, 2},
		"claimed":   {2, 1},
		"pending":   {0, 0},
		"done":      {1, 0},
	}
	for _, m := range snap.Statuses {
		w := want[m.Status]
		if m.In24h != w.in24 || m.Out24h != w.out24 {
			t.Errorf("status %s: in24h=%d (want %d), out24h=%d (want %d)", m.Status, m.In24h, w.in24, m.Out24h, w.out24)
		}
	}
}

func TestCompute_BottleneckPicksHighestRatio(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// Pending item that's been sitting for 5 days. No throughput out.
	writeItem(t, root, "pending", "mg-stuck", "stuck", []string{"mg-other"}, "", now.Add(-6*24*time.Hour))
	writeEvent(t, root, "work.created", "mg-stuck", "", "pending", now.Add(-5*24*time.Hour))

	// Claimed item, recent.
	writeItem(t, root, "claimed", "mg-fresh", "fresh", nil, "", now.Add(-time.Hour))
	writeEvent(t, root, "work.claim", "mg-fresh", "available", "claimed", now.Add(-30*time.Minute))

	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(1)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if snap.Bottleneck != "pending" {
		t.Errorf("bottleneck should be pending, got %q", snap.Bottleneck)
	}
}

func TestCompute_BottleneckNoneWhenAllEmpty(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	// Only a done item — done is excluded from bottleneck candidates.
	writeItem(t, root, "done", "mg-d", "done one", nil, "", now.Add(-2*time.Hour))
	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if snap.Bottleneck != "" {
		t.Errorf("bottleneck should be empty, got %q", snap.Bottleneck)
	}
}

func TestCompute_BlockedChain(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// mg-A claimed 30h ago, two items depend on it.
	writeItem(t, root, "claimed", "mg-A", "blocker", nil, "", now.Add(-31*time.Hour))
	writeEvent(t, root, "work.claim", "mg-A", "available", "claimed", now.Add(-30*time.Hour))
	writeItem(t, root, "pending", "mg-B", "wait B", []string{"mg-A"}, "", now.Add(-30*time.Hour))
	writeItem(t, root, "pending", "mg-C", "wait C", []string{"mg-A"}, "", now.Add(-30*time.Hour))

	// mg-fresh claimed 1h ago, should not be flagged.
	writeItem(t, root, "claimed", "mg-fresh", "fresh", nil, "", now.Add(-time.Hour))
	writeEvent(t, root, "work.claim", "mg-fresh", "available", "claimed", now.Add(-time.Hour))

	snap, err := Compute(root, Options{Now: now, BlockedAfter: 24 * time.Hour, PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(snap.Blocked) != 1 {
		t.Fatalf("expected 1 blocked item, got %d: %+v", len(snap.Blocked), snap.Blocked)
	}
	b := snap.Blocked[0]
	if b.ID != "mg-A" {
		t.Errorf("blocked id should be mg-A, got %q", b.ID)
	}
	if len(b.Blocking) != 2 || b.Blocking[0] != "mg-B" || b.Blocking[1] != "mg-C" {
		t.Errorf("blocking list should be [mg-B mg-C] (sorted), got %v", b.Blocking)
	}
}

func TestCompute_BlockedChainCustomThreshold(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItem(t, root, "claimed", "mg-X", "two-hour", nil, "", now.Add(-3*time.Hour))
	writeEvent(t, root, "work.claim", "mg-X", "available", "claimed", now.Add(-2*time.Hour))

	// Default (24h) — not blocked.
	snap, _ := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})
	if len(snap.Blocked) != 0 {
		t.Errorf("default threshold should not flag 2h-old item, got %v", snap.Blocked)
	}

	// 1h threshold — blocked.
	snap, _ = Compute(root, Options{Now: now, BlockedAfter: time.Hour, PolecatFetcher: fakePolecats(0)})
	if len(snap.Blocked) != 1 {
		t.Errorf("1h threshold should flag 2h-old item, got %v", snap.Blocked)
	}
}

func TestCompute_RepoFilter(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	writeItem(t, root, "claimed", "mg-A", "repo a", nil, "/path/to/repo-a", now.Add(-2*time.Hour))
	writeItem(t, root, "claimed", "mg-B", "repo b", nil, "/path/to/repo-b", now.Add(-2*time.Hour))
	writeEvent(t, root, "work.claim", "mg-A", "available", "claimed", now.Add(-time.Hour))
	writeEvent(t, root, "work.claim", "mg-B", "available", "claimed", now.Add(-time.Hour))

	snap, err := Compute(root, Options{Now: now, RepoFilter: "repo-a", PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, m := range snap.Statuses {
		if m.Status == "claimed" {
			if m.Count != 1 {
				t.Errorf("repo filter should leave 1 claimed item, got %d", m.Count)
			}
			if m.In24h != 1 {
				t.Errorf("repo filter should restrict events to filtered items: in24h=%d, want 1", m.In24h)
			}
		}
	}
}

func TestCompute_SpawnPressure(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItem(t, root, "available", "mg-1", "a", nil, "", now)
	writeItem(t, root, "available", "mg-2", "b", nil, "", now)
	writeItem(t, root, "available", "mg-3", "c", nil, "", now)

	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(1)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if snap.Spawn.Available != 3 || snap.Spawn.Polecats != 1 || !snap.Spawn.PolecatsOK {
		t.Errorf("spawn pressure mismatch: %+v", snap.Spawn)
	}
}

func TestCompute_AgeFromEventThenCreated(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// First item has an event for arrival in claimed.
	writeItem(t, root, "claimed", "mg-evt", "with event", nil, "", now.Add(-10*time.Hour))
	writeEvent(t, root, "work.claim", "mg-evt", "available", "claimed", now.Add(-3*time.Hour))
	// Second item has no event — age must come from Created.
	writeItem(t, root, "claimed", "mg-noevt", "no event", nil, "", now.Add(-5*time.Hour))

	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, m := range snap.Statuses {
		if m.Status != "claimed" {
			continue
		}
		if m.OldestID != "mg-noevt" {
			t.Errorf("oldest claimed should be mg-noevt (5h via Created), got %q with age %s", m.OldestID, m.OldestAge)
		}
	}
}

func TestRender_IncludesAllSections(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItem(t, root, "claimed", "mg-x", "stuck task", nil, "", now.Add(-26*time.Hour))
	writeEvent(t, root, "work.claim", "mg-x", "available", "claimed", now.Add(-25*time.Hour))
	writeItem(t, root, "available", "mg-y", "queued", nil, "", now)

	snap, err := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	var buf bytes.Buffer
	Render(&buf, snap, true)
	out := buf.String()

	for _, want := range []string{
		"mg flow @",
		"status",
		"available",
		"claimed",
		"pending",
		"done",
		"bottleneck:",
		"blocked chains:",
		"mg-x",
		"spawn pressure:",
		"available:1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Render output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRender_BottleneckMarker(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItem(t, root, "pending", "mg-stale", "stale", []string{"mg-x"}, "", now.Add(-10*24*time.Hour))
	writeEvent(t, root, "work.created", "mg-stale", "", "pending", now.Add(-10*24*time.Hour))
	snap, _ := Compute(root, Options{Now: now, PolecatFetcher: fakePolecats(0)})

	var buf bytes.Buffer
	Render(&buf, snap, true)
	out := buf.String()
	if !strings.Contains(out, "▶ pending") {
		t.Errorf("expected bottleneck marker on pending row, got:\n%s", out)
	}
}

func TestRender_NoPolecatFetcherFallback(t *testing.T) {
	root := setupRoot(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeItem(t, root, "available", "mg-q", "queued", nil, "", now)
	snap, _ := Compute(root, Options{Now: now, PolecatFetcher: func() (int, bool) { return 0, false }})

	var buf bytes.Buffer
	Render(&buf, snap, true)
	if !strings.Contains(buf.String(), "polecats:?") {
		t.Errorf("expected polecats:? when fetcher returns false, got:\n%s", buf.String())
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []time.Duration
		want time.Duration
	}{
		{nil, 0},
		{[]time.Duration{time.Hour}, time.Hour},
		{[]time.Duration{time.Minute, time.Hour, 24 * time.Hour}, time.Hour},
		{[]time.Duration{time.Minute, time.Hour}, (time.Minute + time.Hour) / 2},
	}
	for i, c := range cases {
		got := median(append([]time.Duration(nil), c.in...))
		if got != c.want {
			t.Errorf("case %d: median(%v) = %v, want %v", i, c.in, got, c.want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "—"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := shortDuration(c.in); got != c.want {
			t.Errorf("shortDuration(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

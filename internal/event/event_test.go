package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndList(t *testing.T) {
	root := t.TempDir()

	// Append two events
	e1, err := Append(root, "agent.start", map[string]string{"agent": "cat-a3f", "type": "crew"})
	if err != nil {
		t.Fatal(err)
	}
	if e1.Type != "agent.start" {
		t.Errorf("got type %q, want agent.start", e1.Type)
	}
	if e1.Ts == "" {
		t.Error("timestamp should be set")
	}

	e2, err := Append(root, "work.claim", map[string]string{"agent": "cat-a3f", "item": "gt-a3f"})
	if err != nil {
		t.Fatal(err)
	}

	// List all
	entries, err := List(root, ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Type != "agent.start" {
		t.Errorf("first entry type = %q", entries[0].Type)
	}
	if entries[1].Type != "work.claim" {
		t.Errorf("second entry type = %q", entries[1].Type)
	}
	_ = e2
}

func TestListFilter(t *testing.T) {
	root := t.TempDir()

	Append(root, "agent.start", nil)
	Append(root, "work.claim", nil)
	Append(root, "agent.start", nil)

	// Filter by type
	entries, err := List(root, ListOpts{Type: "agent.start"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}

	// Tail
	entries, err = List(root, ListOpts{Tail: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if entries[0].Type != "agent.start" {
		t.Errorf("tail entry type = %q, want agent.start", entries[0].Type)
	}
}

func TestListEmpty(t *testing.T) {
	root := t.TempDir()
	entries, err := List(root, ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestEntryJSON(t *testing.T) {
	e := Entry{
		Ts:    "2026-01-01T00:00:00Z",
		Type:  "test.event",
		Extra: map[string]string{"key": "val"},
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]string
	json.Unmarshal(data, &m)

	if m["ts"] != e.Ts {
		t.Errorf("ts = %q", m["ts"])
	}
	if m["type"] != e.Type {
		t.Errorf("type = %q", m["type"])
	}
	if m["key"] != "val" {
		t.Errorf("key = %q", m["key"])
	}
}

func TestAppendCreatesFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, eventsFile)

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("events file should not exist yet")
	}

	Append(root, "test", nil)

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("events file should exist: %v", err)
	}
}

func TestLogPath(t *testing.T) {
	root := t.TempDir()
	got := LogPath(root)
	want := filepath.Join(root, eventsFile)
	if got != want {
		t.Errorf("LogPath(%q) = %q, want %q", root, got, want)
	}
}

// writeRaw seeds events.jsonl with exact lines, so a test controls ts.
func writeRaw(t *testing.T, root string, lines ...string) {
	t.Helper()
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(LogPath(root), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestListSince_NonUTCOffset is the positive control from drellem2/macguffin#48:
// 2026-11-21T00:00:00+05:00 is 2026-11-20T19:00:00Z, so an event at 22:14:15Z
// is inside the window. The old lexical compare ("2026-11-20..." <
// "2026-11-21...") dropped it.
func TestListSince_NonUTCOffset(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root,
		`{"ts":"2026-11-20T18:59:59Z","type":"work.done","item_id":"mg-before"}`,
		`{"ts":"2026-11-20T22:14:15Z","type":"work.done","item_id":"mg-after"}`,
	)
	entries, err := List(root, ListOpts{Since: mustTime(t, "2026-11-21T00:00:00+05:00")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Extra["item_id"] != "mg-after" {
		t.Fatalf("want only mg-after (22:14:15Z >= 19:00Z), got %+v", entries)
	}
}

// TestListUntil_Exclusive: --until keeps ts < until, so an event exactly at the
// bound is excluded and adjacent windows never double-count.
func TestListUntil_Exclusive(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root,
		`{"ts":"2026-11-19T23:59:59Z","type":"a"}`,
		`{"ts":"2026-11-20T00:00:00Z","type":"b"}`,
		`{"ts":"2026-11-20T00:00:01Z","type":"c"}`,
	)
	entries, err := List(root, ListOpts{
		Since: mustTime(t, "2026-11-19T00:00:00Z"),
		Until: mustTime(t, "2026-11-20T00:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "a" {
		t.Fatalf("want only a, got %+v", entries)
	}
	// Positive control: the bound itself is kept by --since (inclusive).
	entries, err = List(root, ListOpts{Since: mustTime(t, "2026-11-20T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "b" {
		t.Fatalf("want b and c, got %+v", entries)
	}
}

// TestListBounds_UnparsableTsExcluded: an event whose ts cannot be placed in
// time is excluded once a bound is set, and still listed when none is.
func TestListBounds_UnparsableTsExcluded(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root,
		`{"ts":"not-a-time","type":"x"}`,
		`{"ts":"2026-11-20T12:00:00Z","type":"y"}`,
	)
	entries, err := List(root, ListOpts{Since: mustTime(t, "2026-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "y" {
		t.Fatalf("want only y, got %+v", entries)
	}
	entries, err = List(root, ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("unbounded list should keep both, got %+v", entries)
	}
}

package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

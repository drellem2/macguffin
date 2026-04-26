package spend

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestAppendAndRead_RoundTrip(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	recs := []Record{
		{Ts: ts, Agent: "pc-x", Model: "m", Input: 5, Output: 10, Session: "s1", MessageUUID: "u1"},
		{Ts: ts.Add(time.Second), Agent: "pc-x", Model: "m", Input: 1, Output: 2, Session: "s1", MessageUUID: "u2"},
	}
	if err := AppendItem(root, "mg-aaaa", recs); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := ReadItem(root, "mg-aaaa")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].Session != "s1" || got[0].MessageUUID != "u1" || got[0].Input != 5 {
		t.Errorf("unexpected first record: %+v", got[0])
	}
	if got[0].ItemID != "mg-aaaa" {
		t.Errorf("ItemID not set on read: %q", got[0].ItemID)
	}
}

func TestAppendItem_EmptyNoOp(t *testing.T) {
	root := t.TempDir()
	if err := AppendItem(root, "mg-aaaa", nil); err != nil {
		t.Fatalf("append empty: %v", err)
	}
	// File should not be created for an empty append.
	if _, err := os.Stat(filepath.Join(ItemDir(root), "mg-aaaa.jsonl")); !os.IsNotExist(err) {
		t.Errorf("empty append created file: %v", err)
	}
}

func TestSeenSet(t *testing.T) {
	root := t.TempDir()
	if err := AppendItem(root, "mg-a", []Record{
		{Session: "s1", MessageUUID: "u1"},
		{Session: "s1", MessageUUID: "u2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendAgent(root, "architect", []Record{
		{Session: "s2", MessageUUID: "u3"},
	}); err != nil {
		t.Fatal(err)
	}

	seen, err := SeenSet(root)
	if err != nil {
		t.Fatalf("seen: %v", err)
	}
	want := []Key{
		{Session: "s1", MessageUUID: "u1"},
		{Session: "s1", MessageUUID: "u2"},
		{Session: "s2", MessageUUID: "u3"},
	}
	for _, k := range want {
		if _, ok := seen[k]; !ok {
			t.Errorf("missing key %v", k)
		}
	}
	if len(seen) != 3 {
		t.Errorf("seen size = %d, want 3", len(seen))
	}
}

func TestListItems_MissingDir(t *testing.T) {
	root := t.TempDir()
	ids, err := ListItems(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty list on missing dir, got %v", ids)
	}
}

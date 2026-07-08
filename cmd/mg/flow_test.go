package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/flow"
)

// The flow --json writers previously encoded straight to os.Stdout, which meant
// the command could not be exercised by a test capturing cobra's redirected
// output. These tests pin the fix (mg-b093): the writers honour the io.Writer
// threaded in from the caller (cmd.OutOrStdout()), so output is capturable, and
// the FROZEN additive-only wire shape (drellem2/pogo#55) is preserved.

func TestWriteFlowStatusJSON_WritesToProvidedWriter(t *testing.T) {
	snap := flow.Snapshot{
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Bottleneck:  "claimed",
		Statuses: []flow.StatusMetrics{{
			Status:    "claimed",
			Count:     2,
			In24h:     1,
			MedianAge: 3 * time.Hour,
			OldestAge: 30 * time.Hour,
			OldestID:  "mg-old1",
		}},
		Blocked: []flow.BlockedItem{{
			ID:       "mg-blk1",
			Status:   "claimed",
			Title:    "stuck",
			Age:      48 * time.Hour,
			Blocking: []string{"mg-dep1"},
		}},
		Spawn: flow.SpawnPressure{Available: 5, Polecats: 1, PolecatsOK: true},
	}

	var buf bytes.Buffer
	if err := writeFlowStatusJSON(&buf, snap); err != nil {
		t.Fatalf("writeFlowStatusJSON: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected JSON on the provided writer, got nothing")
	}

	var out flowJSON
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if out.GroupBy != "status" {
		t.Errorf("group_by = %q, want status", out.GroupBy)
	}
	if out.Bottleneck != "claimed" {
		t.Errorf("bottleneck = %q, want claimed", out.Bottleneck)
	}
	if len(out.Statuses) != 1 || out.Statuses[0].OldestID != "mg-old1" {
		t.Errorf("statuses = %+v, want one entry oldest_id=mg-old1", out.Statuses)
	}
	if out.Statuses[0].MedianAgeHours != 3 {
		t.Errorf("median_age_hours = %v, want 3", out.Statuses[0].MedianAgeHours)
	}
	if len(out.Groups) != 0 {
		t.Errorf("groups = %+v, want empty on the status path", out.Groups)
	}
	if out.Spawn == nil || out.Spawn.Available != 5 {
		t.Errorf("spawn = %+v, want available=5 on the status path", out.Spawn)
	}
}

func TestWriteFlowGroupedJSON_WritesToProvidedWriter(t *testing.T) {
	snap := &flow.GroupedSnapshot{
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		GroupBy:     "repo",
		Bottleneck:  "repoA",
		Groups: []flow.GroupedMetrics{{
			Key:            "repoA",
			Label:          "repoA",
			Active:         3,
			Done7d:         2,
			MedianAgeHours: 12,
			OldestID:       "mg-old2",
			OldestAgeHours: 40,
		}},
	}

	var buf bytes.Buffer
	if err := writeFlowGroupedJSON(&buf, snap); err != nil {
		t.Fatalf("writeFlowGroupedJSON: %v", err)
	}

	var out flowJSON
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if out.GroupBy != "repo" {
		t.Errorf("group_by = %q, want repo", out.GroupBy)
	}
	if len(out.Groups) != 1 || out.Groups[0].Key != "repoA" {
		t.Errorf("groups = %+v, want one entry key=repoA", out.Groups)
	}
	if len(out.Statuses) != 0 {
		t.Errorf("statuses = %+v, want empty on the grouped path", out.Statuses)
	}
	// Spawn is null on the grouped path — it's a status-only concept.
	if out.Spawn != nil {
		t.Errorf("spawn = %+v, want null on the grouped path", out.Spawn)
	}
}

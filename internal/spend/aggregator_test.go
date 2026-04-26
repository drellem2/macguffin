package spend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

func openAppend(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// makeTranscript writes a Claude Code-style JSONL with two assistant
// messages (a-1 at +1s, a-2 at +2s). Used to drive the aggregator.
func makeTranscript(t *testing.T, projects, projectDir, sessionID, agentTs string) string {
	t.Helper()
	path := filepath.Join(projects, projectDir, sessionID+".jsonl")
	body := `{"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","sessionId":"` + sessionID + `","timestamp":"` + agentTs + `"}
{"type":"assistant","uuid":"a-1","sessionId":"` + sessionID + `","timestamp":"` + agentTs + `","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}}
{"type":"assistant","uuid":"a-2","sessionId":"` + sessionID + `","timestamp":"` + agentTs + `","message":{"model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":2}}}
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	return path
}

func emitClaim(t *testing.T, mgRoot, actor, item, ts string) {
	t.Helper()
	if _, err := event.Append(mgRoot, "work.claim", map[string]string{
		"actor":   actor,
		"item_id": item,
		"ts":      ts,
	}); err != nil {
		t.Fatal(err)
	}
}

// emitDone emits a work.done event. Note: Append stamps ts=now() — for
// tests we want a controlled timestamp so we write directly to the file.
func emitDone(t *testing.T, mgRoot, actor, item, ts string) {
	t.Helper()
	writeEvent(t, mgRoot, "work.done", actor, item, ts)
}

func writeEvent(t *testing.T, mgRoot, typ, actor, item, ts string) {
	t.Helper()
	line := `{"actor":"` + actor + `","item_id":"` + item + `","ts":"` + ts + `","type":"` + typ + `"}` + "\n"
	p := event.LogPath(mgRoot)
	f, err := openAppend(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// TestAggregate_Idempotent_NoNewWritesOnRerun is the headline test: re-running
// the aggregator with no new transcript data must not duplicate records.
func TestAggregate_Idempotent_NoNewWritesOnRerun(t *testing.T) {
	mgRoot := t.TempDir()
	projects := t.TempDir()

	// Polecat pc-aaaa claims mg-aaaa at 12:00:00, all assistant messages at
	// 12:00:01 fall inside that interval (no work.done = still claimed).
	writeEvent(t, mgRoot, "work.claim", "pc-aaaa", "mg-aaaa", "2026-04-26T12:00:00Z")
	makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-1", "2026-04-26T12:00:01Z")

	run := &Run{Root: mgRoot, ProjectsDir: projects, Warn: func(string) {}}
	res1, err := run.Aggregate()
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if res1.NewItemRecords != 2 {
		t.Errorf("first pass NewItemRecords = %d, want 2", res1.NewItemRecords)
	}

	res2, err := run.Aggregate()
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res2.NewItemRecords != 0 || res2.NewAgentRecords != 0 {
		t.Errorf("second pass wrote new records: %+v", res2)
	}
	if res2.SkippedDup != 2 {
		t.Errorf("second pass SkippedDup = %d, want 2", res2.SkippedDup)
	}

	// Verify the on-disk file still has exactly two lines.
	got, err := ReadItem(mgRoot, "mg-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("after rerun: %d records on disk, want 2", len(got))
	}
}

func TestAggregate_DedupAcrossDistinctSessionsWithSameUUID(t *testing.T) {
	// Polecat ID reuse mitigation: dedup on (session, message-uuid). Two
	// sessions with the same message-uuid must both be stored.
	mgRoot := t.TempDir()
	projects := t.TempDir()
	writeEvent(t, mgRoot, "work.claim", "pc-aaaa", "mg-aaaa", "2026-04-26T12:00:00Z")
	makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-A", "2026-04-26T12:00:01Z")
	makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-B", "2026-04-26T12:00:01Z")

	run := &Run{Root: mgRoot, ProjectsDir: projects, Warn: func(string) {}}
	if _, err := run.Aggregate(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadItem(mgRoot, "mg-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	// Two sessions × two assistant messages each = 4 distinct records.
	if len(got) != 4 {
		t.Errorf("got %d records, want 4 (2 sessions × 2 msgs)", len(got))
	}
}

func TestAggregate_AttributionInterval(t *testing.T) {
	// Crew agent works on mg-aaa from 12:00→12:01, then nothing for 30s,
	// then mg-bbb from 12:01:30→13:00. Messages between 12:01 and 12:01:30
	// have no claim → overhead bucket.
	mgRoot := t.TempDir()
	projects := t.TempDir()

	writeEvent(t, mgRoot, "work.claim", "architect", "mg-aaa", "2026-04-26T12:00:00Z")
	writeEvent(t, mgRoot, "work.done", "architect", "mg-aaa", "2026-04-26T12:01:00Z")
	writeEvent(t, mgRoot, "work.claim", "architect", "mg-bbb", "2026-04-26T12:01:30Z")

	dir := "-Users-daniel--pogo-agents-architect"
	body := `{"type":"assistant","uuid":"a-during-aaa","sessionId":"sess-1","timestamp":"2026-04-26T12:00:30Z","message":{"model":"x","usage":{"input_tokens":1,"output_tokens":1}}}
{"type":"assistant","uuid":"a-gap","sessionId":"sess-1","timestamp":"2026-04-26T12:01:15Z","message":{"model":"x","usage":{"input_tokens":2,"output_tokens":2}}}
{"type":"assistant","uuid":"a-during-bbb","sessionId":"sess-1","timestamp":"2026-04-26T12:01:45Z","message":{"model":"x","usage":{"input_tokens":3,"output_tokens":3}}}
`
	if err := writeFile(filepath.Join(projects, dir, "sess-1.jsonl"), body); err != nil {
		t.Fatal(err)
	}

	run := &Run{Root: mgRoot, ProjectsDir: projects, Warn: func(string) {}}
	if _, err := run.Aggregate(); err != nil {
		t.Fatal(err)
	}
	aaa, _ := ReadItem(mgRoot, "mg-aaa")
	bbb, _ := ReadItem(mgRoot, "mg-bbb")
	overhead, _ := ReadAgent(mgRoot, "architect")

	if len(aaa) != 1 || aaa[0].MessageUUID != "a-during-aaa" {
		t.Errorf("mg-aaa: got %d records, want 1 with uuid a-during-aaa: %+v", len(aaa), aaa)
	}
	if len(bbb) != 1 || bbb[0].MessageUUID != "a-during-bbb" {
		t.Errorf("mg-bbb: got %d records, want 1 with uuid a-during-bbb: %+v", len(bbb), bbb)
	}
	if len(overhead) != 1 || overhead[0].MessageUUID != "a-gap" {
		t.Errorf("overhead: got %d records, want 1 with uuid a-gap: %+v", len(overhead), overhead)
	}
}

func TestAggregate_MissingProjectsDirIsNotAnError(t *testing.T) {
	mgRoot := t.TempDir()
	run := &Run{Root: mgRoot, ProjectsDir: filepath.Join(t.TempDir(), "does-not-exist"), Warn: func(string) {}}
	res, err := run.Aggregate()
	if err != nil {
		t.Fatalf("missing dir: %v", err)
	}
	if res.MessagesScanned != 0 {
		t.Errorf("scanned %d, want 0", res.MessagesScanned)
	}
}

func TestQuery_ByItem(t *testing.T) {
	mgRoot := t.TempDir()
	ts := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: ts, Agent: "pc-1", Input: 10, Output: 20, Session: "s", MessageUUID: "u1"},
		{Ts: ts, Agent: "pc-1", Input: 5, Output: 7, Session: "s", MessageUUID: "u2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendItem(mgRoot, "mg-bbb", []Record{
		{Ts: ts, Agent: "pc-2", Input: 1, Output: 2, Session: "s", MessageUUID: "u3"},
	}); err != nil {
		t.Fatal(err)
	}

	groups, err := Query(mgRoot, "", QueryOpts{By: "item"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	// Sorted by total tokens desc — mg-aaa (15+27=42) > mg-bbb (1+2=3).
	if groups[0].Key != "mg-aaa" {
		t.Errorf("groups[0].Key = %q, want mg-aaa", groups[0].Key)
	}
	if groups[0].Totals.Input != 15 || groups[0].Totals.Output != 27 {
		t.Errorf("mg-aaa totals: %+v", groups[0].Totals)
	}
	if groups[0].Totals.ItemCount != 1 {
		t.Errorf("ItemCount = %d, want 1", groups[0].Totals.ItemCount)
	}
}

func TestQuery_SinceFilters(t *testing.T) {
	mgRoot := t.TempDir()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour) // 30 days ago
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: old, Agent: "pc", Input: 100, Session: "s", MessageUUID: "old"},
		{Ts: now.Add(-time.Hour), Agent: "pc", Input: 5, Session: "s", MessageUUID: "recent"},
	}); err != nil {
		t.Fatal(err)
	}
	groups, err := Query(mgRoot, "", QueryOpts{By: "item", Since: 7 * 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Totals.Input != 5 {
		t.Errorf("since-filtered query: %+v", groups)
	}
}

func TestQuery_ByAgentIncludesOverhead(t *testing.T) {
	mgRoot := t.TempDir()
	ts := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	_ = AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: ts, Agent: "pc-x", Input: 10, Output: 5, Session: "s", MessageUUID: "u1"},
	})
	_ = AppendAgent(mgRoot, "architect", []Record{
		{Ts: ts, Agent: "architect", Input: 100, Output: 50, Session: "s", MessageUUID: "u2"},
	})
	groups, err := Query(mgRoot, "", QueryOpts{By: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(groups), groups)
	}
	if groups[0].Key != "architect" {
		t.Errorf("expected architect to lead, got %q", groups[0].Key)
	}
}

func TestBuildClaimIntervals_UnclosedClaimExtendsToFarFuture(t *testing.T) {
	mgRoot := t.TempDir()
	writeEvent(t, mgRoot, "work.claim", "pc-x", "mg-x", "2026-04-26T12:00:00Z")
	intervals, err := buildClaimIntervals(mgRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if !intervals[0].ReleaseTs.Equal(farFuture) {
		t.Errorf("unclosed claim release = %v, want farFuture", intervals[0].ReleaseTs)
	}
}

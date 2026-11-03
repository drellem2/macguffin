package spend

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// rawLines counts the non-empty NDJSON lines physically present under the
// store — the on-disk truth that ReadAll's dedup would otherwise hide.
func rawLines(t *testing.T, root string) int {
	t.Helper()
	n := 0
	for _, dir := range []string{ItemDir(root), AgentDir(root)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		for _, e := range entries {
			f, err := os.Open(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if strings.TrimSpace(sc.Text()) != "" {
					n++
				}
			}
			f.Close()
		}
	}
	return n
}

// A store already holding an exact doubled block (what the pre-lock race
// wrote) reads back with each message counted once — no rebuild needed.
func TestReadAll_DedupsDoubledBlock(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 8, 13, 9, 46, 0, 0, time.UTC)
	block := []Record{
		{Ts: ts, Agent: "pc-x", Input: 10, CacheRead: 30, CacheCreate: 5, Output: 20, Session: "s1", MessageUUID: "a-1"},
		{Ts: ts, Agent: "pc-x", Input: 1, Output: 2, Session: "s1", MessageUUID: "a-2"},
	}
	for i := 0; i < 2; i++ {
		if err := AppendItem(root, "mg-aaaa", block); err != nil {
			t.Fatal(err)
		}
		if err := AppendAgent(root, "pc-y", []Record{{Ts: ts, Agent: "pc-y", Input: 7, Session: "s2", MessageUUID: "b-1"}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := rawLines(t, root); got != 6 {
		t.Fatalf("fixture: %d raw lines, want 6", got)
	}

	all, err := ReadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ReadAll = %d records, want 3 unique", len(all))
	}

	tot, err := GrandTotal(root, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if tot.TotalIn() != 45+1+7 || tot.TotalOut() != 22 {
		t.Errorf("GrandTotal in/out = %d/%d, want 53/22", tot.TotalIn(), tot.TotalOut())
	}

	groups, err := Query(root, "", QueryOpts{By: "item"})
	if err != nil {
		t.Fatal(err)
	}
	var itemIn int
	for _, g := range groups {
		if g.Key == "mg-aaaa" {
			itemIn = g.Totals.TotalIn()
		}
	}
	if itemIn != 46 {
		t.Errorf("Query mg-aaaa TotalIn = %d, want 46", itemIn)
	}
}

// The stated tie-break: last in read order wins, and read order is
// by-agent/ then by-item/ (lexical), so an attributed copy beats an overhead
// copy and, between two items, the lexically greater id keeps the tokens.
func TestReadAll_TieBreakIsDeterministic(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 8, 9, 17, 22, 0, 0, time.UTC)
	dup := Record{Ts: ts, Agent: "mayor", Input: 3, Session: "s", MessageUUID: "m"}
	// Write the greater id first, so write order and read order disagree.
	if err := AppendItem(root, "mg-e314", []Record{dup}); err != nil {
		t.Fatal(err)
	}
	if err := AppendItem(root, "mg-4b01", []Record{dup}); err != nil {
		t.Fatal(err)
	}
	if err := AppendAgent(root, "mayor", []Record{dup}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		all, err := ReadAll(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 {
			t.Fatalf("ReadAll = %d records, want 1", len(all))
		}
		if all[0].ItemID != "mg-e314" {
			t.Fatalf("winner ItemID = %q, want mg-e314", all[0].ItemID)
		}
	}
}

// Overlapping harvests must not both append. Before the lock, two goroutines
// calling Aggregate on one root produced 4 raw lines for 2 messages 20/20
// (triage mg-cfbe, REPRO A). Each goroutine opens its own lock descriptor,
// so this exercises the same flock contention two processes would.
func TestAggregate_ConcurrentRunsDoNotDoubleAppend(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		mgRoot := t.TempDir()
		projects := t.TempDir()
		writeEvent(t, mgRoot, "work.claim", "pc-aaaa", "mg-aaaa", "2026-04-26T12:00:00Z")
		makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-1", "2026-04-26T12:00:01Z")

		const n = 4
		var wg sync.WaitGroup
		errs := make([]error, n)
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				run := &Run{Root: mgRoot, ProjectsDir: projects, Warn: func(string) {}, AgentRegistry: func() map[string]string { return nil }}
				_, errs[i] = run.Aggregate()
			}(i)
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := rawLines(t, mgRoot); got != 2 {
			t.Fatalf("trial %d: %d raw lines on disk, want 2", trial, got)
		}
	}
}

// A held lock makes a plain harvest skip (and say so via HarvestSkipped)
// rather than block; once released, the next harvest proceeds.
func TestAggregate_SkipsWhenLockHeld(t *testing.T) {
	mgRoot := t.TempDir()
	projects := t.TempDir()
	writeEvent(t, mgRoot, "work.claim", "pc-aaaa", "mg-aaaa", "2026-04-26T12:00:00Z")
	makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-1", "2026-04-26T12:00:01Z")

	release, err := acquireHarvestLock(mgRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{Root: mgRoot, ProjectsDir: projects, Warn: func(string) {}, AgentRegistry: func() map[string]string { return nil }}
	res, err := run.Aggregate()
	if err != nil {
		t.Fatal(err)
	}
	if !res.HarvestSkipped || res.TranscriptsScanned != 0 {
		t.Fatalf("with lock held: %+v, want HarvestSkipped and no scan", res)
	}
	if got := rawLines(t, mgRoot); got != 0 {
		t.Fatalf("skipped harvest wrote %d lines", got)
	}
	release()

	res, err = run.Aggregate()
	if err != nil {
		t.Fatal(err)
	}
	if res.HarvestSkipped || res.NewItemRecords != 2 {
		t.Fatalf("after release: %+v, want 2 new records", res)
	}
}

// --rebuild waits for the lock instead of skipping, then clears and rescans
// under it — so the removal can never interleave with another harvest.
func TestAggregate_RebuildWaitsForLockThenRewrites(t *testing.T) {
	mgRoot := t.TempDir()
	projects := t.TempDir()
	writeEvent(t, mgRoot, "work.claim", "pc-aaaa", "mg-aaaa", "2026-04-26T12:00:00Z")
	makeTranscript(t, projects, "-Users-daniel--pogo-polecats-pc-aaaa", "sess-1", "2026-04-26T12:00:01Z")
	// A doubled store, as the race left it.
	ts := time.Date(2026, 4, 26, 12, 0, 1, 0, time.UTC)
	stale := []Record{{Ts: ts, Agent: "pc-aaaa", Input: 10, Session: "sess-1", MessageUUID: "a-1"}}
	for i := 0; i < 2; i++ {
		if err := AppendItem(mgRoot, "mg-aaaa", stale); err != nil {
			t.Fatal(err)
		}
	}

	release, err := acquireHarvestLock(mgRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() {
		run := &Run{Root: mgRoot, ProjectsDir: projects, Rebuild: true, Warn: func(string) {}, AgentRegistry: func() map[string]string { return nil }}
		res, err := run.Aggregate()
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case <-done:
		t.Fatal("rebuild returned while the lock was held; it must wait")
	case <-time.After(200 * time.Millisecond):
	}
	if got := rawLines(t, mgRoot); got != 2 {
		t.Fatalf("store touched while lock held: %d lines, want the original 2", got)
	}
	release()
	res := <-done
	if res.HarvestSkipped || res.NewItemRecords != 2 {
		t.Fatalf("rebuild result %+v, want 2 new records", res)
	}
	if got := rawLines(t, mgRoot); got != 2 {
		t.Fatalf("after rebuild: %d raw lines, want 2 (a-1, a-2 once each)", got)
	}
}

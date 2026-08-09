package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// reclaimNow is the fixed clock every test in this file measures its window
// against. Reclaim's whole contract is about message age, so a real clock would
// make the assertions drift.
var reclaimNow = time.Date(2026, 8, 9, 19, 35, 0, 0, time.UTC)

// matchSenders is the sender predicate the CLI passes in, reduced to what
// these tests need: exact equality on the From field.
func matchSenders(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(from string) bool { return set[from] }
}

// plant writes a message straight into new/ so the test controls the Date
// header, which Send derives from the wall clock. date is written verbatim, so
// a test can plant an unparseable one.
func plant(t *testing.T, mailRoot, box, id, from, subject, date string) {
	t.Helper()
	if err := EnsureMaildir(mailRoot, box); err != nil {
		t.Fatalf("EnsureMaildir(%s): %v", box, err)
	}
	content := fmt.Sprintf("Message-Id: %s\nFrom: %s\nSubject: %s\nDate: %s\n\nbody of %s\n",
		id, from, subject, date, id)
	path := filepath.Join(mailRoot, box, "new", id)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("planting %s: %v", id, err)
	}
}

// plantFires plants n copies of one recurring schedule's fallback mail, the
// oldest `age` before the fixed clock and each subsequent one 10 minutes later.
func plantFires(t *testing.T, mailRoot, box, schedule string, n int, oldest time.Duration) []string {
	t.Helper()
	subject := "scheduler: " + schedule + " (cron */10 * * * *)"
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		at := reclaimNow.Add(-oldest).Add(time.Duration(i) * 10 * time.Minute)
		id := fmt.Sprintf("%s.%d", schedule, i)
		plant(t, mailRoot, box, id, "scheduler", subject, at.Format(time.RFC3339))
		ids = append(ids, id)
	}
	return ids
}

func liveIDs(t *testing.T, mailRoot, box string) []string {
	t.Helper()
	msgs, _, err := ListAll(mailRoot, box)
	if err != nil {
		t.Fatalf("ListAll(%s): %v", box, err)
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

func archivedIDs(t *testing.T, mailRoot, box string) []string {
	t.Helper()
	msgs, _, err := ListArchived(mailRoot, box)
	if err != nil {
		t.Fatalf("ListArchived(%s): %v", box, err)
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

func defaultOpts() ReclaimOpts {
	return ReclaimOpts{
		Match:     matchSenders("scheduler"),
		Keep:      1,
		OlderThan: 24 * time.Hour,
		Now:       reclaimNow,
	}
}

// TestReclaim_DrainsSupersededRunKeepsNewest reproduces the defect in miniature:
// one unbroken outage run of fallback copies, all but the newest of which have
// no remaining function.
func TestReclaim_DrainsSupersededRunKeepsNewest(t *testing.T) {
	_, mr := eventTestRoots(t)
	ids := plantFires(t, mr, "architect", "mail-check-architect", 20, 72*time.Hour)

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Scanned != 20 {
		t.Errorf("Scanned = %d, want 20", res.Scanned)
	}
	if res.Groups != 1 {
		t.Errorf("Groups = %d, want 1 (one schedule, one Subject)", res.Groups)
	}
	if res.Reclaimed != 19 || res.Retained != 1 {
		t.Errorf("Reclaimed/Retained = %d/%d, want 19/1", res.Reclaimed, res.Retained)
	}

	live := liveIDs(t, mr, "architect")
	if len(live) != 1 || live[0] != ids[len(ids)-1] {
		t.Errorf("active mailbox = %v, want only the newest copy %q", live, ids[len(ids)-1])
	}
	if got := len(archivedIDs(t, mr, "architect")); got != 19 {
		t.Errorf("archive/ holds %d, want 19 — reclaim MOVES, it never deletes", got)
	}
}

// TestReclaim_NeverTouchesAnotherSender is the property the whole command is
// built around: a real message cannot be selected, however much noise surrounds
// it and whatever its subject says.
func TestReclaim_NeverTouchesAnotherSender(t *testing.T) {
	_, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 40, 72*time.Hour)

	old := reclaimNow.Add(-60 * time.Hour).Format(time.RFC3339)
	// The two collisions a text filter gets wrong: a real sender writing ABOUT
	// the scheduler noise, and a distinct sender whose NAME contains the
	// reclaimed one. Both are older than the window and both are duplicated, so
	// nothing but the sender field keeps them.
	plant(t, mr, "architect", "real.1", "pm-pogo", "scheduler fallback mail is 86% of this box", old)
	plant(t, mr, "architect", "real.2", "pm-pogo", "scheduler fallback mail is 86% of this box", old)
	plant(t, mr, "architect", "real.3", "scheduler-v2", "second-generation fire", old)
	plant(t, mr, "architect", "real.4", "scheduler-v2", "second-generation fire", old)

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Scanned != 40 {
		t.Errorf("Scanned = %d, want 40 — only From:scheduler is examined", res.Scanned)
	}

	live := liveIDs(t, mr, "architect")
	for _, want := range []string{"real.1", "real.2", "real.3", "real.4"} {
		found := false
		for _, id := range live {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Errorf("reclaim took %s — mail from another sender must never be selected (live: %v)", want, live)
		}
	}
}

// TestReclaim_GroupsBySubjectSoEverySchedulesNewestSurvives: an agent with two
// schedules must keep the newest of EACH, not just the newest overall.
func TestReclaim_GroupsBySubjectSoEverySchedulesNewestSurvives(t *testing.T) {
	_, mr := eventTestRoots(t)
	mail := plantFires(t, mr, "architect", "mail-check-architect", 10, 72*time.Hour)
	// The daily sweep's newest fire is much older than the mail-check's, so a
	// per-mailbox "keep the newest" rule would discard it.
	sweep := plantFires(t, mr, "architect", "daily-sweep", 5, 60*time.Hour)

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Groups != 2 {
		t.Errorf("Groups = %d, want 2 (two distinct schedules)", res.Groups)
	}

	live := liveIDs(t, mr, "architect")
	want := []string{mail[len(mail)-1], sweep[len(sweep)-1]}
	sort.Strings(want)
	if strings.Join(live, ",") != strings.Join(want, ",") {
		t.Errorf("active mailbox = %v, want the newest of each schedule %v", live, want)
	}
}

// TestReclaim_RetentionWindowHoldsRecentCopies: superseded is not sufficient;
// the copy must also be older than the window.
func TestReclaim_RetentionWindowHoldsRecentCopies(t *testing.T) {
	_, mr := eventTestRoots(t)
	// 12 copies at 10-minute spacing ending at the clock: every one inside 24h.
	plantFires(t, mr, "architect", "mail-check-architect", 12, 2*time.Hour)

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Reclaimed != 0 || res.Retained != 12 {
		t.Errorf("Reclaimed/Retained = %d/%d, want 0/12 — all inside the window", res.Reclaimed, res.Retained)
	}

	// With the window disabled, every superseded copy goes.
	opts := defaultOpts()
	opts.OlderThan = 0
	res, err = Reclaim(mr, "architect", opts)
	if err != nil {
		t.Fatalf("Reclaim (no window): %v", err)
	}
	if res.Reclaimed != 11 || res.Retained != 1 {
		t.Errorf("Reclaimed/Retained = %d/%d with --older-than=0, want 11/1", res.Reclaimed, res.Retained)
	}
}

// TestReclaim_KeepRetainsNNewest checks Keep, and that a value below 1 is
// raised rather than emptying the group.
func TestReclaim_KeepRetainsNNewest(t *testing.T) {
	_, mr := eventTestRoots(t)
	ids := plantFires(t, mr, "architect", "mail-check-architect", 10, 72*time.Hour)

	opts := defaultOpts()
	opts.Keep = 3
	res, err := Reclaim(mr, "architect", opts)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Reclaimed != 7 || res.Retained != 3 {
		t.Errorf("Reclaimed/Retained = %d/%d with Keep=3, want 7/3", res.Reclaimed, res.Retained)
	}
	live := liveIDs(t, mr, "architect")
	want := append([]string(nil), ids[7:]...)
	sort.Strings(want)
	if strings.Join(live, ",") != strings.Join(want, ",") {
		t.Errorf("active mailbox = %v, want the newest 3 %v", live, want)
	}

	_, mr2 := eventTestRoots(t)
	plantFires(t, mr2, "architect", "mail-check-architect", 4, 72*time.Hour)
	opts.Keep = 0
	res, err = Reclaim(mr2, "architect", opts)
	if err != nil {
		t.Fatalf("Reclaim (Keep=0): %v", err)
	}
	if res.Retained != 1 {
		t.Errorf("Keep=0 retained %d, want 1 — a group is never emptied", res.Retained)
	}
}

// TestReclaim_UndatedCopyIsRetainedAndDoesNotDisplaceTheNewest covers the
// fail-safe ordering. An unparseable Date means unknown age, so the copy is
// retained; sorting it NEWEST would additionally push a genuinely-newest copy
// into the reclaim range and lose the live pointer.
func TestReclaim_UndatedCopyIsRetainedAndDoesNotDisplaceTheNewest(t *testing.T) {
	_, mr := eventTestRoots(t)
	subject := "scheduler: mail-check-architect (cron */10 * * * *)"
	ids := plantFires(t, mr, "architect", "mail-check-architect", 5, 72*time.Hour)
	// An id that sorts LAST alphabetically, so a tie-break on id alone would
	// also have put it in the Keep slot.
	plant(t, mr, "architect", "zz-undated", "scheduler", subject, "not-a-date")

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Undated != 1 {
		t.Errorf("Undated = %d, want 1", res.Undated)
	}
	if res.Reclaimed != 4 || res.Retained != 2 {
		t.Errorf("Reclaimed/Retained = %d/%d, want 4/2 (newest + the undated copy)", res.Reclaimed, res.Retained)
	}

	live := liveIDs(t, mr, "architect")
	want := []string{ids[len(ids)-1], "zz-undated"}
	sort.Strings(want)
	if strings.Join(live, ",") != strings.Join(want, ",") {
		t.Errorf("active mailbox = %v, want %v — the newest DATED copy must survive alongside the undated one", live, want)
	}
}

// TestReclaim_DryRunMovesNothing.
func TestReclaim_DryRunMovesNothing(t *testing.T) {
	workRoot, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 8, 72*time.Hour)

	opts := defaultOpts()
	opts.DryRun = true
	res, err := Reclaim(mr, "architect", opts)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Reclaimed != 7 {
		t.Errorf("dry run Reclaimed = %d, want 7 (the count it would move)", res.Reclaimed)
	}
	if got := len(liveIDs(t, mr, "architect")); got != 8 {
		t.Errorf("dry run left %d messages in the active mailbox, want 8 — it must move nothing", got)
	}
	if got := len(archivedIDs(t, mr, "architect")); got != 0 {
		t.Errorf("dry run archived %d messages, want 0", got)
	}
	if got := len(eventsOfType(t, workRoot, "mail.reclaimed")); got != 0 {
		t.Errorf("dry run emitted %d mail.reclaimed events, want 0", got)
	}
}

// TestReclaim_EmitsOneSummaryEventPerMailbox is the enumeration check on the
// remedy itself: reclaim drains an unbounded pile, so it must not write an
// unbounded pile of its own into events.jsonl on the way.
func TestReclaim_EmitsOneSummaryEventPerMailbox(t *testing.T) {
	workRoot, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 50, 72*time.Hour)

	if _, err := Reclaim(mr, "architect", defaultOpts()); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	if got := len(eventsOfType(t, workRoot, "mail.archived")); got != 0 {
		t.Errorf("reclaim emitted %d per-message mail.archived events; a 12k sweep would rebuild the pile in the event log", got)
	}
	entries := eventsOfType(t, workRoot, "mail.reclaimed")
	if len(entries) != 1 {
		t.Fatalf("mail.reclaimed events = %d, want exactly 1 summary per mailbox", len(entries))
	}
	if entries[0].Extra["reclaimed"] != "49" {
		t.Errorf("summary event reclaimed = %q, want %q", entries[0].Extra["reclaimed"], "49")
	}
}

// TestReclaim_AuditNamesEveryMove: the per-message record survives, in the log
// nothing reads to decide whether it has mail.
func TestReclaim_AuditNamesEveryMove(t *testing.T) {
	_, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 6, 72*time.Hour)

	if _, err := Reclaim(mr, "architect", defaultOpts()); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	data, err := os.ReadFile(AuditLogPath(mr))
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	n := strings.Count(string(data), " op=reclaim ")
	if n != 5 {
		t.Errorf("audit log has %d op=reclaim lines, want 5 (one per moved message)", n)
	}
	if !strings.Contains(string(data), "from_dir=new") {
		t.Errorf("audit line should record which directory the message left:\n%s", data)
	}
}

// TestReclaim_SecondRunIsANoOp: the `idempotent` hint in the command schema.
func TestReclaim_SecondRunIsANoOp(t *testing.T) {
	_, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 9, 72*time.Hour)

	if _, err := Reclaim(mr, "architect", defaultOpts()); err != nil {
		t.Fatalf("first Reclaim: %v", err)
	}
	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("second Reclaim: %v", err)
	}
	if res.Reclaimed != 0 {
		t.Errorf("second run reclaimed %d, want 0", res.Reclaimed)
	}
	if got := len(archivedIDs(t, mr, "architect")); got != 8 {
		t.Errorf("archive/ holds %d after two runs, want 8", got)
	}
}

// TestReclaim_ReadStateDoesNotExempt: the pile is a mix of unread (new/) and
// read (cur/) copies, and both are superseded the same way.
func TestReclaim_ReadStateDoesNotExempt(t *testing.T) {
	_, mr := eventTestRoots(t)
	ids := plantFires(t, mr, "architect", "mail-check-architect", 6, 72*time.Hour)
	if _, err := Read(mr, "architect", ids[0]); err != nil {
		t.Fatalf("Read: %v", err)
	}

	res, err := Reclaim(mr, "architect", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Reclaimed != 5 {
		t.Errorf("Reclaimed = %d, want 5 (read and unread copies alike)", res.Reclaimed)
	}
	data, err := os.ReadFile(AuditLogPath(mr))
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	if !strings.Contains(string(data), "from_dir=cur") {
		t.Errorf("expected a reclaim from cur/ in the audit log:\n%s", data)
	}
}

// TestReclaim_NilMatchIsRefused: defaulting to "match everything" would turn a
// caller's bug into a fleet-wide sweep of real correspondence.
func TestReclaim_NilMatchIsRefused(t *testing.T) {
	_, mr := eventTestRoots(t)
	plantFires(t, mr, "architect", "mail-check-architect", 3, 72*time.Hour)

	opts := defaultOpts()
	opts.Match = nil
	if _, err := Reclaim(mr, "architect", opts); err == nil {
		t.Fatal("Reclaim with a nil Match must refuse, not sweep everything")
	}
	if got := len(liveIDs(t, mr, "architect")); got != 3 {
		t.Errorf("refused Reclaim moved %d messages, want 0", 3-got)
	}
}

// TestReclaim_MissingMailboxIsQuiet: the fleet-wide form walks every box.
func TestReclaim_MissingMailboxIsQuiet(t *testing.T) {
	_, mr := eventTestRoots(t)
	res, err := Reclaim(mr, "nobody", defaultOpts())
	if err != nil {
		t.Fatalf("Reclaim on a missing mailbox: %v", err)
	}
	if res.Scanned != 0 || res.Reclaimed != 0 {
		t.Errorf("missing mailbox gave %+v, want zeros", res)
	}
}

// TestReclaim_ReclaimedMailIsStillReadable: "moved, not deleted" has to be true
// of the content, not just the file count.
func TestReclaim_ReclaimedMailIsStillReadable(t *testing.T) {
	_, mr := eventTestRoots(t)
	ids := plantFires(t, mr, "architect", "mail-check-architect", 4, 72*time.Hour)

	if _, err := Reclaim(mr, "architect", defaultOpts()); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	msgs, _, err := ListArchived(mr, "architect")
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("archived %d, want 3", len(msgs))
	}
	for _, m := range msgs {
		if m.From != "scheduler" || !strings.HasPrefix(m.Body, "body of ") {
			t.Errorf("archived message %s lost its content: %+v", m.ID, m)
		}
	}
	if msgs[0].ID != ids[0] {
		t.Errorf("archived oldest = %s, want %s", msgs[0].ID, ids[0])
	}
}

// TestReclaim_DoesNotOverwriteAnArchivedID: an id already in archive/ keeps its
// content, the same guarantee MergeMailbox gives.
func TestReclaim_DoesNotOverwriteAnArchivedID(t *testing.T) {
	_, mr := eventTestRoots(t)
	ids := plantFires(t, mr, "architect", "mail-check-architect", 3, 72*time.Hour)

	archiveDir := filepath.Join(mr, "architect", "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	squatter := filepath.Join(archiveDir, ids[0])
	if err := os.WriteFile(squatter, []byte("From: pa\nSubject: real\nDate: x\n\nkeep me\n"), 0o644); err != nil {
		t.Fatalf("planting squatter: %v", err)
	}

	if _, err := Reclaim(mr, "architect", defaultOpts()); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	data, err := os.ReadFile(squatter)
	if err != nil {
		t.Fatalf("reading squatter: %v", err)
	}
	if !strings.Contains(string(data), "keep me") {
		t.Errorf("reclaim overwrote an existing archived message:\n%s", data)
	}
}

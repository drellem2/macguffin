package spend

import (
	"testing"
	"time"
)

func TestWindowStart_Today(t *testing.T) {
	// A Wednesday at 15:04 local. "today" anchors to that day's midnight.
	now := time.Date(2026, 4, 22, 15, 4, 5, 0, time.UTC)
	got, err := WindowStart("today", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("today start = %v, want %v", got, want)
	}
}

func TestWindowStart_WeekAnchorsToMonday(t *testing.T) {
	// 2026-04-22 is a Wednesday; the week's Monday is 2026-04-20.
	now := time.Date(2026, 4, 22, 15, 4, 5, 0, time.UTC)
	got, err := WindowStart("week", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("week start = %v, want %v (Monday)", got, want)
	}
	if got.Weekday() != time.Monday {
		t.Errorf("week start weekday = %v, want Monday", got.Weekday())
	}
}

func TestWindowStart_WeekOnSundayLooksBackSixDays(t *testing.T) {
	// A Sunday must anchor to the *preceding* Monday, not roll forward.
	// 2026-04-26 is a Sunday; its week's Monday is 2026-04-20.
	now := time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC)
	got, err := WindowStart("week", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Sunday week start = %v, want %v", got, want)
	}
}

func TestWindowStart_WeekOnMondayIsSameDay(t *testing.T) {
	// 2026-04-20 is a Monday; the week start is that midnight.
	now := time.Date(2026, 4, 20, 23, 59, 0, 0, time.UTC)
	got, err := WindowStart("week", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Monday week start = %v, want %v", got, want)
	}
}

func TestWindowStart_Unknown(t *testing.T) {
	if _, err := WindowStart("month", time.Now().UTC()); err == nil {
		t.Error("expected error for unknown window name")
	}
}

func TestQuery_SinceTimeFiltersCalendarWindow(t *testing.T) {
	mgRoot := t.TempDir()
	// Two records: one before the cutoff, one after.
	before := time.Date(2026, 4, 26, 8, 0, 0, 0, time.UTC)
	after := time.Date(2026, 4, 26, 20, 0, 0, 0, time.UTC)
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: before, Agent: "pc", Input: 100, Session: "s", MessageUUID: "old"},
		{Ts: after, Agent: "pc", Input: 5, Session: "s", MessageUUID: "new"},
	}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	groups, err := Query(mgRoot, "", QueryOpts{By: "item", SinceTime: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Totals.Input != 5 {
		t.Errorf("SinceTime-filtered query: %+v", groups)
	}
}

func TestQuery_SinceTimeWinsOverSince(t *testing.T) {
	// When both are set, the absolute SinceTime cutoff must win over the
	// rolling Since duration.
	mgRoot := t.TempDir()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: now.Add(-2 * time.Hour), Agent: "pc", Input: 100, Session: "s", MessageUUID: "a"},
		{Ts: now.Add(-30 * time.Minute), Agent: "pc", Input: 5, Session: "s", MessageUUID: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	// Rolling Since of 24h would keep both records; SinceTime of now-1h keeps
	// only the 30-minutes-ago record.
	groups, err := Query(mgRoot, "", QueryOpts{
		By:        "item",
		Since:     24 * time.Hour,
		SinceTime: now.Add(-time.Hour),
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Totals.Input != 5 {
		t.Errorf("SinceTime should win over Since: %+v", groups)
	}
}

func TestGrandTotal_SumsItemAndOverhead(t *testing.T) {
	mgRoot := t.TempDir()
	ts := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: ts, Agent: "pc", Input: 10, CacheRead: 3, CacheCreate: 1, Output: 20, Session: "s", MessageUUID: "u1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendAgent(mgRoot, "architect", []Record{
		{Ts: ts, Agent: "architect", Input: 100, Output: 50, Session: "s", MessageUUID: "u2"},
	}); err != nil {
		t.Fatal(err)
	}
	tot, err := GrandTotal(mgRoot, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if tot.TotalIn() != 10+3+1+100 {
		t.Errorf("TotalIn = %d, want %d", tot.TotalIn(), 10+3+1+100)
	}
	if tot.TotalOut() != 70 {
		t.Errorf("TotalOut = %d, want 70", tot.TotalOut())
	}
	// Only the attributed record carries an ItemID, so ItemCount is 1.
	if tot.ItemCount != 1 {
		t.Errorf("ItemCount = %d, want 1", tot.ItemCount)
	}
}

func TestGrandTotal_SinceFilters(t *testing.T) {
	mgRoot := t.TempDir()
	old := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := AppendItem(mgRoot, "mg-aaa", []Record{
		{Ts: old, Agent: "pc", Input: 1000, Session: "s", MessageUUID: "old"},
		{Ts: recent, Agent: "pc", Input: 7, Session: "s", MessageUUID: "new"},
	}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	tot, err := GrandTotal(mgRoot, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if tot.TotalIn() != 7 {
		t.Errorf("since-filtered GrandTotal TotalIn = %d, want 7", tot.TotalIn())
	}
}

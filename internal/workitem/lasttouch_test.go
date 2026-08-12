package workitem

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// mg-43d0: the failure this file guards is not a lost byte, it is a false bug
// report. Two agents edited one item, neither could see the other, and a
// colleague's write was reported to the human as data corruption. Every
// assertion here is about whether the next writer is TOLD.

// touchLog writes an events.jsonl containing exactly the given lines, so a test
// asserts against a log it fully controls rather than one accumulated by
// side effects.
func touchLog(t *testing.T, root string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(event.LogPath(root), []byte(body), 0o644); err != nil {
		t.Fatalf("writing events.jsonl: %v", err)
	}
}

func editLine(ts, id, actor, mode string) string {
	return fmt.Sprintf(`{"ts":%q,"type":"work.edited","item_id":%q,"actor":%q,"mode":%q}`, ts, id, actor, mode)
}

func TestLastTouch_ReturnsTheMostRecentEditNotTheFirst(t *testing.T) {
	root := t.TempDir()
	touchLog(t, root,
		editLine("2026-08-11T09:00:00Z", "mg-1234", "pm-pogo", "replace"),
		editLine("2026-08-11T09:30:00Z", "mg-1234", "mayor", "append"),
	)

	got := LastTouch(root, "mg-1234")
	if got == nil {
		t.Fatal("LastTouch returned nil for an item with two recorded edits")
	}
	if got.Actor != "mayor" {
		t.Errorf("actor = %q, want mayor (the LAST writer, not the first)", got.Actor)
	}
	if got.Mode != "append" {
		t.Errorf("mode = %q, want append", got.Mode)
	}
	if want := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC); !got.When.Equal(want) {
		t.Errorf("when = %v, want %v", got.When, want)
	}
}

// The backward scan must not stop at another item's line, and must not answer
// with one either — the log is shared by every item in the workspace.
func TestLastTouch_IgnoresOtherItemsAndOtherEventTypes(t *testing.T) {
	root := t.TempDir()
	touchLog(t, root,
		editLine("2026-08-11T09:00:00Z", "mg-1234", "pm-pogo", "replace"),
		editLine("2026-08-11T09:10:00Z", "mg-9999", "mayor", "replace"),
		`{"ts":"2026-08-11T09:20:00Z","type":"work.claim","item_id":"mg-1234","actor":"pogod"}`,
	)

	got := LastTouch(root, "mg-1234")
	if got == nil {
		t.Fatal("LastTouch returned nil despite a work.edited for mg-1234")
	}
	if got.Actor != "pm-pogo" {
		t.Errorf("actor = %q, want pm-pogo — a later work.claim and another item's edit both leaked in", got.Actor)
	}
}

// An id that is a substring of a longer id must not match it. The prefilter is
// a substring scan, so this is exactly where it could lie; the unmarshal is
// what makes it honest.
func TestLastTouch_DoesNotMatchAnItemWhoseIDMerelyContainsThisOne(t *testing.T) {
	root := t.TempDir()
	touchLog(t, root, editLine("2026-08-11T09:00:00Z", "mg-1234ab", "mayor", "replace"))

	if got := LastTouch(root, "mg-1234"); got != nil {
		t.Fatalf("LastTouch(mg-1234) answered with %+v from mg-1234ab's line", got)
	}
}

func TestLastTouch_NoLogAndNoRecordAreBothNil(t *testing.T) {
	empty := t.TempDir()
	if got := LastTouch(empty, "mg-1234"); got != nil {
		t.Errorf("LastTouch with no events.jsonl = %+v, want nil", got)
	}

	root := t.TempDir()
	touchLog(t, root, editLine("2026-08-11T09:00:00Z", "mg-9999", "mayor", "append"))
	if got := LastTouch(root, "mg-1234"); got != nil {
		t.Errorf("LastTouch for an unedited item = %+v, want nil", got)
	}
}

// A truncated or half-written final line must not hide the record below it.
// events.jsonl is appended to by many processes and is not fsynced.
func TestLastTouch_SkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	touchLog(t, root,
		editLine("2026-08-11T09:00:00Z", "mg-1234", "mayor", "append"),
		`{"ts":"2026-08-11T09:05:00Z","type":"work.edited","item_id":"mg-1234"`, // truncated
	)

	got := LastTouch(root, "mg-1234")
	if got == nil || got.Actor != "mayor" {
		t.Fatalf("LastTouch = %+v, want the intact mayor line below the truncated one", got)
	}
}

// A log with no trailing newline is the shape a killed writer leaves. The last
// line is still a record.
func TestLastTouch_ReadsALogWithNoTrailingNewline(t *testing.T) {
	root := t.TempDir()
	line := editLine("2026-08-11T09:00:00Z", "mg-1234", "mayor", "append")
	if err := os.WriteFile(event.LogPath(root), []byte(line), 0o644); err != nil {
		t.Fatalf("writing events.jsonl: %v", err)
	}
	if got := LastTouch(root, "mg-1234"); got == nil || got.Actor != "mayor" {
		t.Fatalf("LastTouch = %+v, want mayor", got)
	}
}

// LastTouch is read through Update's own emission: the field it keys on
// (item_id) is written by edit.go, and a test that only ever read hand-written
// lines would pass if that key were renamed.
func TestLastTouch_ReadsWhatUpdateActuallyWrites(t *testing.T) {
	root := t.TempDir()
	item := seedMetaItem(t, root, "an item two agents will edit")

	t.Setenv("MG_ACTOR", "pm-pogo")
	if _, err := Update(root, item.ID, UpdateField{AppendBody: strPtr("## handing this back")}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := LastTouch(root, item.ID)
	if got == nil {
		t.Fatal("LastTouch found nothing after a real Update — item_id or type has drifted")
	}
	if got.Actor != "pm-pogo" || got.Mode != "append" {
		t.Errorf("LastTouch = %+v, want actor=pm-pogo mode=append", got)
	}
	if got.When.IsZero() {
		t.Error("When is zero — the emitted 'ts' did not parse as RFC3339")
	}
}

// --- EditNotice: what gets said, and what stays quiet ----------------------

// The incident, reproduced: mayor edits an item pm-pogo wrote to four minutes
// ago, and is told so.
func TestEditNotice_NamesTheOtherWriterAndHowLongAgo(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 34, 0, 0, time.UTC)
	touch := &Touch{Actor: "pm-pogo", Mode: "append", When: now.Add(-4 * time.Minute)}

	got := EditNotice(touch, "mayor", "mg-1234", now)
	for _, want := range []string{"mg-1234", "4m ago", "pm-pogo", "append"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q is missing %q", got, want)
		}
	}
	if !strings.Contains(got, "not corruption") {
		t.Errorf("notice %q does not say what the incident needed it to say", got)
	}
}

// Silence has exactly one meaning: no OTHER party has a recorded edit since you
// last wrote. An agent iterating on its own item is 71% of measured edits, and
// a note on every one of them is how a warning becomes wallpaper.
func TestEditNotice_SilentWhenTheLastWriterIsYou(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 34, 0, 0, time.UTC)
	touch := &Touch{Actor: "mayor", Mode: "append", When: now.Add(-30 * time.Second)}

	if got := EditNotice(touch, "mayor", "mg-1234", now); got != "" {
		t.Errorf("notice = %q, want silence — the caller IS the last writer", got)
	}
}

func TestEditNotice_SilentWithNoRecordAtAll(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 34, 0, 0, time.UTC)
	if got := EditNotice(nil, "mayor", "mg-1234", now); got != "" {
		t.Errorf("notice = %q, want silence for an item with no recorded edit", got)
	}
	if got := EditNotice(&Touch{Actor: "", Mode: "append"}, "mayor", "mg-1234", now); got != "" {
		t.Errorf("notice = %q, want silence — an unattributed record names nobody", got)
	}
}

// No recency threshold: a cutoff would be a number nothing justifies, and the
// age is printed so the reader can discount an old touch themselves.
func TestEditNotice_ReportsOldTouchesToo(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 34, 0, 0, time.UTC)
	touch := &Touch{Actor: "pm-pogo", Mode: "replace", When: now.Add(-19 * 24 * time.Hour)}

	got := EditNotice(touch, "mayor", "mg-1234", now)
	if !strings.Contains(got, "19d ago") {
		t.Errorf("notice %q should name the age (19d ago) rather than suppress the touch", got)
	}
}

func TestHumanAgo(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 34, 0, 0, time.UTC)
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{"seconds", now.Add(-20 * time.Second), "just now"},
		{"minutes", now.Add(-4 * time.Minute), "4m ago"},
		{"hours", now.Add(-2 * time.Hour), "2h ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		// A clock step is not information. It must not render as a negative age.
		{"future", now.Add(90 * time.Second), "just now"},
		// An unparseable ts must say so, not be computed from the epoch and
		// reported as fifty-six years.
		{"unreadable", time.Time{}, "at an unreadable time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanAgo(tc.when, now); got != tc.want {
				t.Errorf("humanAgo = %q, want %q", got, tc.want)
			}
		})
	}
}

// The note rides on the front of every `mg edit`, against a log that is 5.3 MB
// and 40,779 lines on the live workspace and only grows. A visibility aid that
// taxes the write path is one that gets removed, so the cost is measured rather
// than assumed.
func BenchmarkLastTouch(b *testing.B) {
	root := b.TempDir()
	var sb strings.Builder
	for i := 0; i < 40000; i++ {
		sb.WriteString(editLine("2026-08-01T00:00:00Z", fmt.Sprintf("mg-%04x", i), "mayor", "append"))
		sb.WriteString("\n")
	}
	if err := os.WriteFile(event.LogPath(root), []byte(sb.String()), 0o644); err != nil {
		b.Fatalf("writing events.jsonl: %v", err)
	}

	// The worst case: an item with no record, which cannot short-circuit and
	// must scan every line.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := LastTouch(root, "mg-ffffff"); got != nil {
			b.Fatalf("unexpected hit: %+v", got)
		}
	}
}

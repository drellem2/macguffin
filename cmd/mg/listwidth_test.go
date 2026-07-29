package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
)

// legacyListLine is the exact Printf `mg list` used before line-fitting landed.
// Every assertion about untruncated output compares against THIS, so a future
// change that starts truncating unconditionally — the failure mode that would
// quietly corrupt every script parsing `mg list` — fails here rather than in
// somebody's pipeline.
func legacyListLine(indent string, item *workitem.Item, currentUser string) string {
	return fmt.Sprintf("%s%-10s %-8s %s%s%s%s", indent, item.ID, item.Type, item.Title,
		formatTags(item.Tags), formatAssignee(item.Assignee, currentUser), formatSnooze(item))
}

// wideItem is the shape the whole feature exists for: a long title, a fat tag
// list, an assignee and a live snooze — the last two being the fields a naive
// "cut at column N" would delete first.
func wideItem() *workitem.Item {
	wake := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	return &workitem.Item{
		ID:        "mg-73ee",
		Type:      "bug",
		Title:     "mg list runs off the right-hand edge of the screen on any terminal narrower than the longest title in the store",
		Tags:      []string{"cli", "ergonomics", "listing", "needs-review"},
		Assignee:  "bob",
		Snooze:    wake,
		SnoozeRaw: wake.Format(time.RFC3339),
	}
}

func TestRenderListLine_ZeroWidthIsByteIdenticalToLegacy(t *testing.T) {
	items := []*workitem.Item{
		wideItem(),
		{ID: "mg-0001", Type: "task", Title: "short"},
		{ID: "mg-0002", Type: "design", Title: "tagged", Tags: []string{"a", "b"}},
		{ID: "mg-0003", Type: "task", Title: "assigned", Assignee: "alice"},
		{ID: "mg-a-very-long-id-that-overflows", Type: "maintenance", Title: "wide fixed fields"},
	}
	for _, indent := range []string{"", "  "} {
		for _, item := range items {
			got := renderListLine(indent, item, "alice", 0)
			want := legacyListLine(indent, item, "alice")
			if got != want {
				t.Errorf("renderListLine(%q, %s, width=0) =\n  %q\nwant\n  %q", indent, item.ID, got, want)
			}
		}
	}
}

func TestRenderListLine_NarrowWidthKeepsIDTypeAssigneeAndSnooze(t *testing.T) {
	item := wideItem()
	const width = 60
	got := renderListLine("  ", item, "alice", width)

	if w := visibleWidth(got); w > width {
		t.Errorf("line is %d columns wide, want <= %d:\n%q", w, width, got)
	}
	// The four fields that must survive the squeeze.
	for _, want := range []string{item.ID, item.Type, "bob", item.SnoozeRaw} {
		if !strings.Contains(got, want) {
			t.Errorf("narrow line dropped %q (the whole point of the feature):\n%q", want, got)
		}
	}
	// ...and the two that are supposed to absorb it.
	if strings.Contains(got, item.Title) {
		t.Errorf("title was not shortened at width %d:\n%q", width, got)
	}
	if strings.Contains(got, "needs-review") {
		t.Errorf("tags survived while the title was cut; tags are the cheaper field:\n%q", got)
	}
	if !strings.Contains(got, truncMarker) {
		t.Errorf("a cut line carries no %q marker, so a shortened title is indistinguishable from a short one:\n%q", truncMarker, got)
	}
}

// TestRenderListLine_SnoozeSurvivesEvenWhenTitleCannot pushes the width below
// what the fixed fields need. assignee and snooze are still never squeezed.
func TestRenderListLine_SnoozeSurvivesEvenWhenTitleCannot(t *testing.T) {
	item := wideItem()
	got := renderListLine("", item, "alice", 20)
	for _, want := range []string{item.ID, item.SnoozeRaw, "bob"} {
		if !strings.Contains(got, want) {
			t.Errorf("width=20 dropped %q:\n%q", want, got)
		}
	}
}

func TestRenderListLine_FittingLineIsUntouched(t *testing.T) {
	item := &workitem.Item{ID: "mg-0001", Type: "task", Title: "short title", Tags: []string{"a"}, Assignee: "bob"}
	got := renderListLine("", item, "alice", 200)
	if want := legacyListLine("", item, "alice"); got != want {
		t.Errorf("a line that fits was still rewritten:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, truncMarker) {
		t.Errorf("a line that fits carries a truncation marker: %q", got)
	}
}

// TestRenderListLine_TagsTakeTheLeftoverAfterTheTitle pins the priority order:
// the title is filled first, tags get what remains.
func TestRenderListLine_TagsTakeTheLeftoverAfterTheTitle(t *testing.T) {
	item := &workitem.Item{
		ID: "mg-0001", Type: "task",
		Title: "a title that fits",
		Tags:  []string{"alpha", "beta", "gamma", "delta"},
	}
	full := renderListLine("", item, "alice", 0)
	// Wide enough for the title plus part of the tag list.
	got := renderListLine("", item, "alice", visibleWidth(full)-10)
	if !strings.Contains(got, item.Title) {
		t.Errorf("title was cut while tags were still spendable:\n%q", got)
	}
	if strings.Contains(got, "delta") {
		t.Errorf("tag list was not shortened:\n%q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Errorf("tag list was dropped whole instead of shortened:\n%q", got)
	}
}

func TestVisibleWidth_IgnoresANSI(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"plain", 5},
		{"\033[2mdim\033[0m", 3},
		{" \033[2m[cli, ergonomics]\033[0m", 18},
		{" \033[34mhuman\033[0m", 6},
		{"héllo", 5},
	}
	for _, c := range cases {
		if got := visibleWidth(c.in); got != c.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateVisible(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"fits", "abc", 3, "abc"},
		{"empty", "", 5, ""},
		{"zero budget", "abc", 0, ""},
		{"negative budget", "abc", -3, ""},
		{"cut", "abcdef", 4, "abc" + truncMarker},
		{"cut to marker only", "abcdef", 1, truncMarker},
		{"styled fits", "\033[2mab\033[0m", 2, "\033[2mab\033[0m"},
		{"styled cut resets", "\033[2mabcdef\033[0m", 3, "\033[2mab" + truncMarker + "\033[0m"},
		{"multibyte", "héllo wörld", 4, "hél" + truncMarker},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateVisible(c.in, c.max)
			if got != c.want {
				t.Errorf("truncateVisible(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
			if c.max > 0 && visibleWidth(got) > c.max {
				t.Errorf("truncateVisible(%q, %d) = %q, which is %d columns", c.in, c.max, got, visibleWidth(got))
			}
		})
	}
}

// TestResolveListWidth_NonTerminalNeverTruncates is the positive control at the
// unit level: a width of 0 is what tells renderListLine to leave the line
// alone, and anything that is not a character device must get it.
func TestResolveListWidth_NonTerminalNeverTruncates(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := resolveListWidth(f, false); got != 0 {
		t.Errorf("resolveListWidth(regular file) = %d, want 0 (no truncation off a terminal)", got)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if got := resolveListWidth(w, false); got != 0 {
		t.Errorf("resolveListWidth(pipe) = %d, want 0 (no truncation into a pipe)", got)
	}

	// --wide opts out even when the caller would otherwise get a budget.
	if got := resolveListWidth(w, true); got != 0 {
		t.Errorf("resolveListWidth(--wide) = %d, want 0", got)
	}
}

func TestListWideFlag_HasNoTruncateAlias(t *testing.T) {
	f := listCmd.Flags().Lookup("no-truncate")
	if f == nil {
		t.Fatal("--no-truncate is not registered")
	}
	if f.NoOptDefVal != "true" {
		t.Errorf("--no-truncate NoOptDefVal = %q, want \"true\" (it must work as a bare flag)", f.NoOptDefVal)
	}
	// Setting the alias must move the same variable as --wide.
	if err := f.Value.Set("true"); err != nil {
		t.Fatal(err)
	}
	if !listWide {
		t.Error("--no-truncate does not share --wide's value")
	}
	if err := f.Value.Set("false"); err != nil {
		t.Fatal(err)
	}
}

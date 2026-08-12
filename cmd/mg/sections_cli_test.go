package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/workitem"
)

// The CLI half of mg-7e02: `mg show ID --sections` and the banner.

// sectionsFixture files an item whose body carries n dated sections plus a
// spec, and returns its id. The body is handed over with NO leading heading so
// mg writes the title heading itself — the shape every guard in edit.go is
// happy with, and the one a real appender ends up in.
func sectionsFixture(t *testing.T, bin string, env []string, title string, sections []string) string {
	t.Helper()
	body := "The live spec, such as it is.\n\nDo the thing.\n"
	for _, s := range sections {
		body += "\n" + s + "\n\nwhat was decided.\n"
	}
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := snzRun(t, bin, env, "new", title, "--body-file="+path)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	return strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")
}

// bodyOf reads the stored body back through the documented route — the same one
// `mg show --sections` names in its own output.
func bodyOf(t *testing.T, bin string, env []string, id string) string {
	t.Helper()
	out, code := snzRun(t, bin, env, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, out)
	}
	var item struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &item); err != nil {
		t.Fatalf("unmarshal show --json: %v\n%s", err, out)
	}
	return item.Body
}

// TestCLI_SectionsIndexesEveryDatedHeading is the feature: an index of the
// dated headings, and line numbers that land on them IN THE BODY those numbers
// claim to index. The line numbers are checked against the stored body rather
// than against a hand-written constant, because a jump table whose numbers are
// off by the height of a header block is worse than no jump table.
func TestCLI_SectionsIndexesEveryDatedHeading(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	headings := []string{
		"## 2026-08-10",
		"## STRUCK 2026-08-11: the premise above is gone",
		"### CORRECTION 2026-08-12 (mayor) — read this one",
	}
	id := sectionsFixture(t, bin, env, "A contested item", headings)

	out, code := snzRun(t, bin, env, "show", id, "--sections")
	if code != 0 {
		t.Fatalf("mg show --sections: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "3 dated sections") {
		t.Errorf("the index should say how many there are, got:\n%s", out)
	}

	bodyLines := strings.Split(bodyOf(t, bin, env, id), "\n")
	for _, h := range headings {
		var row string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimRight(line, " "), h) {
				row = line
				break
			}
		}
		if row == "" {
			t.Fatalf("no row for %q in:\n%s", h, out)
		}
		n, err := strconv.Atoi(strings.Fields(row)[0])
		if err != nil {
			t.Fatalf("row %q does not start with a line number: %v", row, err)
		}
		if n < 1 || n > len(bodyLines) {
			t.Fatalf("line %d is outside a %d-line body", n, len(bodyLines))
		}
		if got := bodyLines[n-1]; got != h {
			t.Errorf("index sends a reader to body line %d, which is %q, not %q", n, got, h)
		}
	}
}

// TestCLI_SectionsIsInDocumentOrder: the index never re-sorts by date. A
// section dated before the one above it is the anomaly a reader most needs to
// see, and sorting would hide exactly it.
func TestCLI_SectionsIsInDocumentOrder(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := sectionsFixture(t, bin, env, "Appended out of order", []string{
		"## 2026-08-12 written first",
		"## 2026-07-01 backdated append",
	})
	out, code := snzRun(t, bin, env, "show", id, "--sections")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	late, early := strings.Index(out, "written first"), strings.Index(out, "backdated append")
	if late < 0 || early < 0 {
		t.Fatalf("both sections should be listed, got:\n%s", out)
	}
	if late > early {
		t.Errorf("the index re-sorted by date; it must stay in document order:\n%s", out)
	}
}

// TestCLI_SectionsEmptyIsNotAnError: a body with no dated headings answers the
// question rather than failing it. A non-zero exit here would make `--sections`
// unusable in the `if mg show ... --sections` shape an agent would reach for.
func TestCLI_SectionsEmptyIsNotAnError(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Nothing dated in here")
	out, code := snzRun(t, bin, env, "show", id, "--sections")
	if code != 0 {
		t.Fatalf("--sections on an undated body must exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "no dated sections") {
		t.Errorf("expected an explicit empty answer, got:\n%s", out)
	}
}

// TestCLI_SectionsBannerThreshold is the half that matters. The banner is what
// tells a reader the body is a log BEFORE they are deep in it, so it is checked
// at the boundary in both directions and for its position relative to the body.
func TestCLI_SectionsBannerThreshold(t *testing.T) {
	bin, env, _ := snoozeEnv(t)

	two := sectionsFixture(t, bin, env, "Two appends", []string{
		"## 2026-08-10", "## 2026-08-11",
	})
	out, code := snzRun(t, bin, env, "show", two)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "dated sections") {
		t.Errorf("two dated sections must not raise the banner:\n%s", out)
	}

	three := sectionsFixture(t, bin, env, "Three appends", []string{
		"## 2026-08-10", "## 2026-08-11", "## 2026-08-12",
	})
	out, code = snzRun(t, bin, env, "show", three)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "this body carries 3 dated sections") {
		t.Errorf("three dated sections must raise the banner:\n%s", out)
	}
	if !strings.Contains(out, "--sections") {
		t.Errorf("the banner must name the command that indexes them:\n%s", out)
	}

	// Above the body: a reader who learns the body is a log after reading it
	// has learned it too late.
	banner := strings.Index(out, "this body carries")
	firstSection := strings.Index(out, "## 2026-08-10")
	if banner < 0 || firstSection < 0 || banner > firstSection {
		t.Errorf("the banner must print above the body, banner=%d body=%d:\n%s", banner, firstSection, out)
	}
}

// TestCLI_SectionsIgnoresTheTitleHeading: 13 open items carry a date in their
// TITLE, and the title is not a section of the body — it is already printed in
// the header block. It must neither appear in the index nor push an item over
// the banner threshold.
func TestCLI_SectionsIgnoresTheTitleHeading(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := sectionsFixture(t, bin, env, "RETIRED PREMISE 2026-08-07 — do not act on this", []string{
		"## 2026-08-10", "## 2026-08-11",
	})

	out, code := snzRun(t, bin, env, "show", id, "--sections")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "2 dated sections") {
		t.Errorf("the dated title must not be counted as a section, got:\n%s", out)
	}
	if strings.Contains(out, "RETIRED PREMISE") {
		t.Errorf("the title heading must not appear in the index:\n%s", out)
	}

	out, code = snzRun(t, bin, env, "show", id)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "dated sections") {
		t.Errorf("a dated title must not push a 2-section body over the threshold:\n%s", out)
	}
}

// TestCLI_SectionsJSON: --json is honoured rather than ignored, because a
// caller who passed it will parse whatever comes back. `sections` is an array
// even when empty, so a consumer can iterate without a nil check.
func TestCLI_SectionsJSON(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := sectionsFixture(t, bin, env, "Machine readable", []string{
		"## 2026-08-10 first", "## CORRECTION 2026-08-12 — the above is wrong",
	})

	out, code := snzRun(t, bin, env, "show", id, "--sections", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var got struct {
		ID       string `json:"id"`
		Count    int    `json:"count"`
		Sections []struct {
			Line    int    `json:"line"`
			Date    string `json:"date"`
			Level   int    `json:"level"`
			Heading string `json:"heading"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--sections --json must emit JSON: %v\n%s", err, out)
	}
	if got.ID != id || got.Count != 2 || len(got.Sections) != 2 {
		t.Fatalf("got %+v, want id=%s count=2", got, id)
	}
	if got.Sections[1].Date != "2026-08-12" || got.Sections[1].Level != 2 {
		t.Errorf("second section: got %+v", got.Sections[1])
	}
	if !strings.HasPrefix(got.Sections[1].Heading, "CORRECTION") {
		t.Errorf("heading should be the text without its marker, got %q", got.Sections[1].Heading)
	}

	bare := snzNewItem(t, bin, env, "No sections at all")
	out, code = snzRun(t, bin, env, "show", bare, "--sections", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, `"sections": []`) {
		t.Errorf("an empty index must be [] and not null:\n%s", out)
	}
}

// TestCLI_SectionsRefusesBodyHash: --body-hash prints one line and nothing
// else, so the pair has no meaning. It is refused rather than silently
// resolved, the same way --json and --body-hash already are.
func TestCLI_SectionsRefusesBodyHash(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Flag conflict")
	out, code := snzRun(t, bin, env, "show", id, "--sections", "--body-hash")
	if code == 0 {
		t.Fatalf("--sections with --body-hash must fail, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "--sections") || !strings.Contains(out, "--body-hash") {
		t.Errorf("the refusal should name both flags, got:\n%s", out)
	}
}

// TestSectionsTruncatesToTheColumnBudget exercises the TTY branch that a
// piped CLI test cannot reach: renderSections is called with a width directly,
// the way resolveListWidth would supply one on a terminal.
//
// What is pinned is that the LINE and DATE columns survive and only the heading
// is cut — a jump table whose line number was truncated is not a jump table.
func TestSectionsTruncatesToTheColumnBudget(t *testing.T) {
	item := &workitem.Item{ID: "mg-1234"}
	secs := []workitem.DatedSection{{
		Line: 42, Date: "2026-08-12", Level: 2,
		Heading: strings.Repeat("very long heading ", 20),
	}}

	f, err := os.CreateTemp(t.TempDir(), "sections")
	if err != nil {
		t.Fatal(err)
	}
	renderSections(f, item, secs, 60)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	var row string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "2026-08-12") && strings.Contains(line, "42") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no index row in:\n%s", raw)
	}
	if visibleWidth(row) > 60 {
		t.Errorf("row is %d columns, budget was 60: %q", visibleWidth(row), row)
	}
	if !strings.HasPrefix(strings.TrimSpace(row), "42  2026-08-12") {
		t.Errorf("line and date columns must survive the cut, got %q", row)
	}
	if !strings.HasSuffix(row, truncMarker) {
		t.Errorf("a cut heading must be marked as cut, got %q", row)
	}
}

// TestSectionsKeepsTheHeadingWhenTheBudgetIsGone: on a terminal too narrow to
// hold a heading at all, the row wraps rather than losing the heading. A jump
// table row with a line number and no heading is one a reader cannot use, and
// cannot tell from a section that had no heading.
func TestSectionsKeepsTheHeadingWhenTheBudgetIsGone(t *testing.T) {
	item := &workitem.Item{ID: "mg-1234"}
	secs := []workitem.DatedSection{{Line: 42, Date: "2026-08-12", Level: 2, Heading: "RETRACTED"}}

	f, err := os.CreateTemp(t.TempDir(), "sections")
	if err != nil {
		t.Fatal(err)
	}
	renderSections(f, item, secs, 22) // the columns alone are 20 wide
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "## RETRACTED") {
		t.Errorf("the heading must survive a budget too small to cut it to:\n%s", raw)
	}
}

// TestCLI_SectionsPipedOutputIsUntouched: truncation is a TTY affordance. Down
// a pipe the consumer is grep, and a heading cut at the terminal's width would
// hide the half of a retraction that says what was retracted.
func TestCLI_SectionsPipedOutputIsUntouched(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	long := "## ⚠ RETRACTION 2026-08-06 — MY EVIDENCE SECTION ABOVE IS WRONG, the box was off the whole time and every number in it was measured against a machine that was not running"
	id := sectionsFixture(t, bin, env, "Long heading", []string{long})

	out, code := snzRun(t, bin, env, "show", id, "--sections")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, long) {
		t.Errorf("a piped index must carry the heading verbatim, got:\n%s", out)
	}
}

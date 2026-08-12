package workitem

import (
	"strings"
	"testing"
)

// TestDatedSections_Predicate pins each clause of the predicate stated in
// datedsections.go, one case per clause, in the shape a real stored body has:
// the leading blank line and the "# " title heading that composeBody writes.
func TestDatedSections_Predicate(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []DatedSection
	}{
		{
			name: "empty body has no sections",
			body: "",
		},
		{
			name: "a body with no dated heading has none",
			body: "\n# Title\n\n## Background\n\nsome prose\n",
		},
		{
			name: "the plain append convention",
			body: "\n# Title\n\nspec\n\n## 2026-08-10\n\nfirst\n\n## 2026-08-12\n\nsecond\n",
			want: []DatedSection{
				{Line: 6, Date: "2026-08-10", Level: 2, Heading: "2026-08-10"},
				{Line: 10, Date: "2026-08-12", Level: 2, Heading: "2026-08-12"},
			},
		},
		{
			// Condition 4, and the reason it is "contains": this is the shape
			// every retraction in the store is written in.
			name: "a date later in the heading still counts",
			body: "\n# Title\n\n## STRUCK 2026-07-30: the gate no longer exists\n",
			want: []DatedSection{
				{Line: 4, Date: "2026-07-30", Level: 2, Heading: "STRUCK 2026-07-30: the gate no longer exists"},
			},
		},
		{
			// Condition 3. The title is printed in the header block already,
			// and it is not a section of the body.
			name: "the title heading is never a section, even dated",
			body: "\n# RETIRED PREMISE 2026-08-07 — do not act on this\n\nprose\n",
		},
		{
			// ...but a level-1 heading that is not the FIRST one is a section:
			// bodies in the store do escalate a correction to H1.
			name: "a second H1 is a section",
			body: "\n# RETIRED PREMISE 2026-08-07\n\nprose\n\n# CORRECTION 2026-08-09 — the above is wrong\n",
			want: []DatedSection{
				{Line: 6, Date: "2026-08-09", Level: 1, Heading: "CORRECTION 2026-08-09 — the above is wrong"},
			},
		},
		{
			// Condition 1. A body documenting the convention must not be
			// indexed as though it practised it.
			name: "a fenced example is not a section",
			body: "\n# Title\n\n```\n## 2026-08-12\n```\n\n## 2026-08-11 real\n",
			want: []DatedSection{
				{Line: 8, Date: "2026-08-11", Level: 2, Heading: "2026-08-11 real"},
			},
		},
		{
			name: "a tilde fence counts too",
			body: "\n# Title\n\n~~~\n## 2026-08-12\n~~~\n",
		},
		{
			name: "an indented fence still opens a fence",
			body: "\n# Title\n\n  ```\n  ## 2026-08-12\n  ```\n",
		},
		{
			// Condition 2. Indented and quoted lines are not headings, which is
			// also how a caller quotes one without indexing it.
			name: "indented and blockquoted headings are not headings",
			body: "\n# Title\n\n  ## 2026-08-12 indented\n> ## 2026-08-12 quoted\n",
		},
		{
			name: "seven hashes is not a heading",
			body: "\n# Title\n\n####### 2026-08-12\n",
		},
		{
			name: "a hash with no space is not a heading",
			body: "\n# Title\n\n##2026-08-12\n",
		},
		{
			name: "deep levels are indexed with their level",
			body: "\n# Title\n\n### CORRECTION 2026-08-09 (mayor)\n",
			want: []DatedSection{
				{Line: 4, Date: "2026-08-09", Level: 3, Heading: "CORRECTION 2026-08-09 (mayor)"},
			},
		},
		{
			// The year is pinned to 20xx precisely so this does not fire.
			name: "a version string is not a date",
			body: "\n# Title\n\n## v1.2.3-11-22 and 1999-01-01\n",
		},
		{
			// Document order, never date order: an append dated earlier than
			// the one above it is an anomaly a reader must be able to see.
			name: "out-of-order dates stay in document order",
			body: "\n# Title\n\n## 2026-08-12 late\n\n## 2026-07-01 early\n",
			want: []DatedSection{
				{Line: 4, Date: "2026-08-12", Level: 2, Heading: "2026-08-12 late"},
				{Line: 6, Date: "2026-07-01", Level: 2, Heading: "2026-07-01 early"},
			},
		},
		{
			name: "trailing whitespace is trimmed from the heading",
			body: "\n# Title\n\n## 2026-08-12 note   \n",
			want: []DatedSection{
				{Line: 4, Date: "2026-08-12", Level: 2, Heading: "2026-08-12 note"},
			},
		},
		{
			// No "# " line at all: nothing is the title, so nothing is skipped.
			// DatedSections is total — it does not require a composed body.
			name: "a body with no title heading skips nothing",
			body: "## 2026-08-12 first line of the file\n",
			want: []DatedSection{
				{Line: 1, Date: "2026-08-12", Level: 2, Heading: "2026-08-12 first line of the file"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DatedSections(tc.body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sections, want %d\ngot:  %+v\nwant: %+v", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("section %d:\n got  %+v\n want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestDatedSections_LineNumbersAreBodyLines is the contract `mg show
// --sections` prints in its own header, checked rather than asserted: the Line
// field indexes strings.Split(body, "\n") 1-based, so a reader can cut the
// section out of the body those numbers came from.
func TestDatedSections_LineNumbersAreBodyLines(t *testing.T) {
	body := "\n# Title\n\nspec paragraph\n\n## 2026-08-10 first\n\nbody of first\n\n## 2026-08-12 second\n"
	lines := strings.Split(body, "\n")
	for _, s := range DatedSections(body) {
		if s.Line < 1 || s.Line > len(lines) {
			t.Fatalf("line %d out of range for a %d-line body", s.Line, len(lines))
		}
		want := strings.Repeat("#", s.Level) + " " + s.Heading
		if got := lines[s.Line-1]; got != want {
			t.Errorf("body line %d is %q, want %q", s.Line, got, want)
		}
	}
}

// TestDatedSections_UnclosedFenceSwallowsTheRest pins the behaviour of a body
// whose fence is never closed. Everything after it is treated as fenced and so
// is not indexed.
//
// That is the conservative direction and it is deliberate: an unclosed fence is
// a malformed body, and the choice is between an index that stops early and an
// index that claims a code listing is a section. A reader who sees fewer
// entries than they expected goes and reads the body; one who is handed a
// wrong jump table does not.
func TestDatedSections_UnclosedFenceSwallowsTheRest(t *testing.T) {
	body := "\n# Title\n\n```\nnot closed\n\n## 2026-08-12 invisible\n"
	if got := DatedSections(body); len(got) != 0 {
		t.Fatalf("an unclosed fence should swallow the rest, got %+v", got)
	}
}

package workitem

import (
	"regexp"
	"strings"
)

// Dated sections: reading a body that has become a log (mg-7e02).
//
// A long-lived contested item accumulates dated sections, because the ticket is
// the only artifact the next actor reads — a polecat reads the body, not
// anyone's outbox. The convention works. What fails is READING it: the live
// spec and the superseded history sit in the same undifferentiated body, and a
// reader who starts at the top has no signal that line 124 overturns line 58.
// A worker was nearly dispatched on a position pm-pogo had retracted twice.
//
// This file answers one question and no others: WHERE ARE THE DATED HEADINGS.
// It is not a changelog model — nothing here orders entries, stores them, or
// claims one of them is current.
//
// # THE PREDICATE, STATED (it will be audited)
//
// A body line is a DATED SECTION HEADING when ALL of:
//
//  1. it is not inside a fenced code block (``` or ~~~, either fence character,
//     toggled on its own line) — a body that DOCUMENTS this convention in an
//     example must not be indexed as if it practised it;
//  2. it begins at COLUMN 0 with one to six '#' followed by a space. Column 0 is
//     Parse's own rule for the title heading, and it is what keeps an indented
//     code block from reading as headings;
//  3. it is not the body's FIRST "# " line. That line is not a section — it is
//     the item's TITLE, which Parse derives from it and which `mg show` has
//     already printed in the header block. 13 open items carry a date in their
//     title; indexing it would put a phantom entry at the top of every one of
//     their jump tables;
//  4. the heading text CONTAINS an ISO date, 20xx-xx-xx, anywhere in it.
//
// # WHY CONDITION 4 IS "CONTAINS" AND NOT "STARTS WITH"
//
// The ticket's own phrasing is `## 20xx-xx-xx`, and the narrow reading — the
// date must LEAD the heading — is the one to reach for first. It was measured
// against the real store and it is wrong, in the direction that costs the most.
//
// Measured on the open store (115 items, 2026-08-12): 8 carry 3+ leading-date
// headings; 18 carry 3+ under the rule above. Every one of the 10 items that
// separate those counts is a 200-400 line body of exactly the shape this exists
// for, and these are headings the narrow rule drops:
//
//	## STRUCK 2026-07-30: THE REFINERY PROTECTED-PATHS GATE NO LONGER EXISTS
//	## ⚠ RETRACTION (doctor, 2026-08-06 06:05Z) — MY 22:00Z EVIDENCE SECTION ABOVE IS WRONG.
//	## CURRENT DISPATCH QUEUE — 2026-07-30 04:06Z. SUPERSEDES every earlier queue list…
//	## PROMPT SEQUENCING — CORRECTED 2026-07-30 04:37Z. My earlier … was WRONG.
//
// Those are not incidental mentions of a date. They are the retractions — the
// precise sections a reader must not miss, written by an author who put the
// verdict before the date because the verdict is the point. A rule that indexes
// the tidy appends and drops every retraction indexes the half that was never
// dangerous.
//
// The costs are not symmetric. A false positive is one extra row in a jump
// table, which the reader dismisses in the time it takes to read it. A false
// negative is the superseding section missing from the index of the body it
// supersedes, which is the original failure with a tool's endorsement on it.
//
// # WHAT THIS DOES NOT DO
//
// It does not say which section is CURRENT. Nothing mechanical can: that is a
// judgement someone made, and it is recorded by striking the superseded section
// inline where it sits, not by being the bottom entry here. Sections are
// returned in DOCUMENT order and never sorted by date, so a section dated
// earlier than the one above it stays visible as the anomaly it is.

// datedSectionDate matches an ISO calendar date inside a heading. The year is
// pinned to 20xx: a bare \d{4} matches version strings, port ranges and
// measured counts, and every dated section in the store is this century.
var datedSectionDate = regexp.MustCompile(`20[0-9]{2}-[0-9]{2}-[0-9]{2}`)

// datedSectionHeading matches a column-0 ATX heading of level 1-6 and captures
// its marker and its text.
var datedSectionHeading = regexp.MustCompile(`^(#{1,6}) (.*)$`)

// DatedSection is one dated heading, located.
type DatedSection struct {
	// Line is 1-based and counts lines of the STORED BODY — the bytes
	// `mg show ID --json | jq -r .body` returns, heading and all. It is
	// deliberately not an offset into `mg show ID`'s own output: that output's
	// header block grows and shrinks with which optional fields are set, so a
	// number keyed to it would mean something different for each item.
	Line int `json:"line"`
	// Date is the first ISO date in the heading — what the section is dated,
	// not when it was written; nothing here can know the latter.
	Date string `json:"date"`
	// Level is the heading depth: 1 for "# ", 2 for "## ".
	Level int `json:"level"`
	// Heading is the heading text with its '#' marker and one space removed,
	// verbatim otherwise.
	Heading string `json:"heading"`
}

// DatedSections returns every dated section heading in body, in document order.
// It is total: any string has an answer, and the empty body has none.
func DatedSections(body string) []DatedSection {
	if body == "" {
		return nil
	}
	lines := strings.Split(body, "\n")

	// The title heading, by Parse's rule — the FIRST line beginning "# ".
	// Positional, exactly as the read side is (see titleheading.go), so the
	// line this skips is the same line Parse turned into the title.
	titleLine := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			titleLine = i
			break
		}
	}

	var out []DatedSection
	inFence := false
	for i, line := range lines {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence || i == titleLine {
			continue
		}
		m := datedSectionHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimRight(m[2], " \t")
		date := datedSectionDate.FindString(text)
		if date == "" {
			continue
		}
		out = append(out, DatedSection{
			Line:    i + 1,
			Date:    date,
			Level:   len(m[1]),
			Heading: text,
		})
	}
	return out
}

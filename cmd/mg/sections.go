package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
)

// The reading half of mg-7e02.
//
// `mg show ID --sections` prints a jump table of the body's dated headings, and
// `mg show ID` puts a one-line banner above the body once there are enough of
// them. The banner is the half that matters: the failure is not knowing the
// body is a log until you are already deep in it, and a reader who knows can go
// and get the index. A reader who does not know never asks for one.

// datedSectionBanner is the number of dated sections at which `mg show` says so
// before printing the body.
//
// 3 is the ticket's number and it is a threshold, not a taste: one dated
// section is an append, two is a pair of appends, and three is the point at
// which a body has a history a reader has to date-sort by eye. Measured on the
// open store (115 items, 2026-08-12), 3 puts the banner on 18 of them — the
// long-lived contested ones — and leaves the other 97 rendering exactly as they
// do today. A threshold of 1 would put it on 92 of 115, and a notice on four
// items in five is chrome that gets read past.
const datedSectionBanner = 3

// minHeadingWidth is the smallest column budget worth cutting a heading to.
// Below it the row would be all marker and no heading — the counterpart of
// minTagsWidth in the listing.
const minHeadingWidth = 8

// sectionsJSON is the shape of `mg show ID --sections --json`. Field names are
// additive-only, like every other mg JSON contract.
type sectionsJSON struct {
	ID       string                  `json:"id"`
	Count    int                     `json:"count"`
	Sections []workitem.DatedSection `json:"sections"`
}

// writeSectionsJSON emits the index as one JSON object. `sections` is always an
// array — never null — so a consumer can iterate it without a nil check.
func writeSectionsJSON(item *workitem.Item, secs []workitem.DatedSection) error {
	if secs == nil {
		secs = []workitem.DatedSection{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sectionsJSON{ID: item.ID, Count: len(secs), Sections: secs})
}

// renderSections writes the human jump table. width is a column budget (0 means
// render in full), resolved by the same TTY-only rule as `mg list`: a piped
// index keeps every byte of every heading, because the consumer there is grep.
func renderSections(out *os.File, item *workitem.Item, secs []workitem.DatedSection, width int) {
	if len(secs) == 0 {
		fmt.Fprintf(out, "%s: no dated sections in this body.\n", item.ID)
		return
	}

	noun := "dated sections"
	if len(secs) == 1 {
		noun = "dated section"
	}
	fmt.Fprintf(out, "%s: %d %s.\n", item.ID, len(secs), noun)
	// Both facts a reader needs before using the table, and neither is
	// guessable from it. The line numbers are the body's, which is a different
	// artifact from this command's output; and the bottom row is not a verdict
	// — see datedsections.go on why nothing here can be.
	fmt.Fprintf(out, "Lines are lines of the BODY: mg show %s --json | jq -r .body\n", item.ID)
	fmt.Fprintf(out, "Order is document order. Which section is CURRENT is a judgement, not a field — this does not say.\n\n")

	for _, s := range secs {
		head := fmt.Sprintf("%6d  %-10s  ", s.Line, s.Date)
		heading := strings.Repeat("#", s.Level) + " " + s.Heading
		// A budget too small to say anything is spent on nothing: render the
		// heading whole and let the terminal wrap, the same call `mg list`
		// makes when its fixed fields alone overflow. The alternative is a
		// jump-table row carrying a line number and no heading, which is a row
		// a reader cannot use and cannot tell from one that had no heading.
		if avail := width - visibleWidth(head); width > 0 && avail >= minHeadingWidth {
			heading = truncateVisible(heading, avail)
		}
		fmt.Fprintf(out, "%s%s\n", head, heading)
	}
}

// sectionsBannerLine returns the one-line notice for the header block, or ""
// when the body has not accumulated enough dated sections to need one.
//
// It is rendered as a header FIELD rather than as free-floating text so it sits
// with the item's other facts and above the body, where it is read before the
// body rather than after.
func sectionsBannerLine(item *workitem.Item, secs []workitem.DatedSection) string {
	if len(secs) < datedSectionBanner {
		return ""
	}
	return fmt.Sprintf("%-10s this body carries %d dated sections — see `mg show %s --sections`",
		"Sections:", len(secs), item.ID)
}

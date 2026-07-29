package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/pflag"
)

// listWidthDefault is the column budget used when stdout IS a terminal but its
// real width cannot be read. 100 is wide enough for an id, a type, a useful
// slice of title and the trailing fields, and narrow enough to still bound a
// runaway line.
const listWidthDefault = 100

// truncMarker is appended to any field mg cut. A silently shortened title is a
// wrong title — the reader has to be able to tell "short" from "shortened".
const truncMarker = "…"

// minTagsWidth is the smallest budget worth spending on a tag list. Cut below
// this a tag list is all marker and no content, and the dangling ellipsis
// reads as part of the title rather than as a separate elided field.
const minTagsWidth = 4

// boolVarAlias registers alias as a second name for an already-registered bool
// flag, sharing the SAME underlying value — the bool counterpart of
// stringSliceVarWithAlias. NoOptDefVal has to be copied across by hand or the
// alias would demand an explicit `--alias=true`, which no bool flag does.
func boolVarAlias(fs *pflag.FlagSet, name, alias string) {
	f := fs.Lookup(name)
	fs.Var(f.Value, alias, "alias for --"+name)
	fs.Lookup(alias).NoOptDefVal = f.NoOptDefVal
}

// resolveListWidth returns the column budget for one human-formatted listing
// line, or 0 meaning "render in full, do not truncate".
//
// Truncation is a TTY-only affordance. When stdout is a pipe or a file the
// consumer is `grep`, `awk`, or a script, and dropping columns there would
// silently corrupt every caller that parses `mg list` — so non-terminal output
// stays byte-for-byte what it has always been. `--wide` opts a terminal out
// the same way.
func resolveListWidth(out *os.File, wide bool) int {
	if wide {
		return 0
	}
	if !isTerminal(out) {
		return 0
	}
	if w := terminalWidth(out.Fd()); w > 0 {
		return w
	}
	// A terminal that will not report its size: honour COLUMNS if the shell
	// exported one, else fall back to a fixed budget.
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
		return w
	}
	return listWidthDefault
}

// isTerminal reports whether f is attached to a character device.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// renderListLine renders one item's listing line (without the trailing
// newline). A width of 0 or less renders every field in full.
//
// The field order is id, type, title, tags, assignee, snooze — assignee and
// snooze are LAST, which is exactly why the budget is spent back-to-front:
// their width is reserved before anything else, and the remainder goes to the
// title and then the tags. Cutting the line at column N instead would delete
// the two fields a reader most needs (who owns this, and is it asleep) and
// keep the two that are cheapest to lose.
func renderListLine(indent string, item *workitem.Item, currentUser string, width int) string {
	head := fmt.Sprintf("%s%-10s %-8s ", indent, item.ID, item.Type)
	title := item.Title
	tags := formatTags(item.Tags)
	assignee := formatAssignee(item.Assignee, currentUser)
	snooze := formatSnooze(item)

	if width <= 0 {
		return head + title + tags + assignee + snooze
	}

	avail := width - visibleWidth(head) - visibleWidth(assignee) - visibleWidth(snooze)
	if avail < 0 {
		// The fixed fields alone overflow the terminal. Keep them whole and
		// let the line wrap rather than mangling an id or an assignee.
		avail = 0
	}
	title = truncateVisible(title, avail)
	tagsBudget := avail - visibleWidth(title)
	if tagsBudget < minTagsWidth {
		tagsBudget = 0
	}
	tags = truncateVisible(tags, tagsBudget)

	return head + title + tags + assignee + snooze
}

// escState tracks how far into an ANSI escape sequence a scan has walked.
type escState int

const (
	escNone  escState = iota // ordinary text
	escIntro                 // consumed ESC; the next rune is the introducer
	escCSI                   // inside a CSI body, which ends at a final byte
)

// step advances the state for r and reports whether r belongs to an escape
// sequence, and so occupies no columns. The introducer is handled as its own
// state because in "\033[2m" the '[' falls inside the @-~ final-byte range and
// would otherwise be mistaken for the end of the sequence.
func (s escState) step(r rune) (escState, bool) {
	switch s {
	case escIntro:
		if r == '[' {
			return escCSI, true
		}
		return escNone, true
	case escCSI:
		if r >= '@' && r <= '~' {
			return escNone, true
		}
		return escCSI, true
	default:
		if r == '\033' {
			return escIntro, true
		}
		return escNone, false
	}
}

// visibleWidth returns the number of columns s occupies on screen: runes, not
// bytes, and ANSI escape sequences (which mg uses for dim/blue styling) count
// as zero. Combining marks and double-width CJK are counted as one column
// each, which can under-estimate; the consequence is a line that wraps, not
// one that loses a field.
func visibleWidth(s string) int {
	n := 0
	st := escNone
	for _, r := range s {
		var esc bool
		if st, esc = st.step(r); !esc {
			n++
		}
	}
	return n
}

// truncateVisible returns s cut to at most max visible columns, with
// truncMarker occupying the last column when anything was removed. Escape
// sequences are copied through without consuming budget, and a reset is
// appended if the cut landed inside styled text so the marker cannot leak a
// colour into the rest of the line.
func truncateVisible(s string, max int) string {
	if s == "" || max <= 0 {
		return ""
	}
	if visibleWidth(s) <= max {
		return s
	}

	var b strings.Builder
	n := 0
	styled := false
	st := escNone
	for _, r := range s {
		var esc bool
		st, esc = st.step(r)
		if esc {
			b.WriteRune(r)
			styled = true
			continue
		}
		if n >= max-1 {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString(truncMarker)
	if styled {
		b.WriteString("\033[0m")
	}
	return b.String()
}

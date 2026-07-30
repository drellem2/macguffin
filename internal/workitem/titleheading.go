package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// The title/body coupling, in one place.
//
// A work item's title is NOT a stored field. There is no `title:` in the
// frontmatter: Parse derives it, on every read, from the body's FIRST line
// beginning with "# " (see Parse). The body is the only place the title lives.
//
// That single fact produced two mutually contradictory bug reports nine days
// apart (mg-bac6), both correctly measured, because the WRITE side used to
// disagree with the read side about what "the body already carries the title"
// means:
//
//   - read  (Parse):       the FIRST "# " line, positionally.
//   - write (composeBody): strings.Contains(body, "# "+title) — ANYWHERE in the
//     body, as a substring, at any depth, inside any block.
//
// A position-insensitive write guard in front of a position-sensitive read is
// the whole defect, and it fails in both directions depending only on the shape
// of the incoming body:
//
//   - The body carries "# <title>" somewhere BELOW a different first heading:
//     Contains was satisfied, nothing was prepended, and the read then took the
//     first heading. The item was silently RETITLED by a body edit. Worse, the
//     defensive move disarmed the defence — passing --title "<the title I am
//     trying to keep>" made Contains match all the more surely, so the flag
//     that looked protective was the one guaranteeing the clobber. This ate the
//     titles of mg-2ce4 and mg-0418.
//   - The body's heading was REWORDED, so "# "+title no longer appeared as a
//     substring: the stored title was prepended above the caller's heading and
//     the body ended up with two near-identical H1s, the title unchanged. A
//     one-character edit to a heading — a comma — was enough. 196 of 2009
//     stored bodies carry that signature.
//
// Both outcomes exit 0 and print a success line.
//
// The fix is to give the write side the read side's rule. reconcileTitleHeading
// is now the ONLY place that decides how a title and a body heading combine,
// and it keys off the first heading positionally, exactly as Parse does. The
// duplicate-H1 outcome is not warned about, it is unrepresentable: there is no
// input for which this function emits a second heading for the title.
//
// What it deliberately does NOT do is rewrite headings deeper in the caller's
// prose. A body may legitimately have several H1 sections (207 stored bodies
// do), and mg inventing edits to a caller's prose to satisfy a count would be a
// worse defect than the one being fixed. The invariant enforced here is
// precise: THE TITLE IS THE BODY'S FIRST HEADING, AND MG NEVER SYNTHESISES A
// SECOND ONE.

// firstHeadingLine reports the index of the body's first "# " line and the title
// text on it. It applies exactly Parse's rule — HasPrefix on the raw line — so
// the two can never drift. In particular a blockquoted "> # heading" is NOT a
// heading to either of them, which is why blockquoting a prepended heading was
// observed to leave titles intact: Parse cannot see it, so it cannot become the
// title.
func firstHeadingLine(body string) (idx int, text string, found bool) {
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return i, strings.TrimPrefix(line, "# "), true
		}
	}
	return -1, "", false
}

// replaceHeadingLine rewrites the "# " line at idx to carry text, leaving every
// other byte of the body untouched.
func replaceHeadingLine(body string, idx int, text string) string {
	lines := strings.Split(body, "\n")
	if idx < 0 || idx >= len(lines) {
		return body
	}
	lines[idx] = "# " + text
	return strings.Join(lines, "\n")
}

// countHeadings returns how many lines of body are H1 headings, by Parse's rule.
func countHeadings(body string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			n++
		}
	}
	return n
}

// reconcileTitleHeading returns the body EXACTLY as it will be persisted, such
// that Parse of the result derives title back. It is total: every input has an
// answer, and none of them stack a second title heading.
//
// Three cases, and they are the whole coupling:
//
//  1. The body has no heading at all — synthesise one above it. This is the
//     historical behaviour for this shape and the only one that was ever
//     correct, which is why "hand --body-file a body with no leading heading"
//     is the procedure that always worked.
//  2. The body's first heading already says the title — nothing to do. Every
//     re-render of an already-stored item lands here, so archiving, done,
//     unarchive and friends stay byte-identical. That includes the bodies
//     already corrupted by the old rule: they are not silently repaired, both
//     because rewriting stored prose behind a caller's back is the defect and
//     because mg-c8d5 is evidence.
//  3. The body's first heading says something else — the title wins, IN PLACE.
//     The heading is rewritten rather than pushed down, which is what makes the
//     duplicate unrepresentable. Reaching this case means the caller named a
//     title explicitly (Update refuses the unnamed case before it gets here, so
//     that a title is never changed as an unrequested side effect), or an item
//     is being created, where the title is always named.
func reconcileTitleHeading(body, title string) string {
	idx, heading, found := firstHeadingLine(body)
	if !found {
		// Same bytes the old composeBody produced for this shape: a leading
		// blank line, the heading, a newline, then the body. Preserved
		// deliberately — changing it would rewrite the leading whitespace of
		// every item in the store on its next touch.
		out := fmt.Sprintf("\n# %s\n", title)
		if body != "" {
			out += body
		}
		return out
	}
	if heading == title {
		return body
	}
	return replaceHeadingLine(body, idx, title)
}

// countTitleHeadings returns how many of body's headings say exactly title.
// More than one is the corrupted state itself: two headings claiming to be the
// item's name, only the first of which any reader will ever see.
func countTitleHeadings(body, title string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") && strings.TrimPrefix(line, "# ") == title {
			n++
		}
	}
	return n
}

// errDuplicateTitleHeading refuses a write that would ADD a second heading
// saying the title.
//
// reconcileTitleHeading rewrites a differing first heading in place rather than
// pushing it down, which is what makes stacking unrepresentable — but "in place"
// can still land on a title the body already carries further down, and then the
// rewrite is the thing that authored the duplicate. That is the same defect
// arriving by the other door, so it is refused rather than written.
//
// The condition is that this write INCREASES the count, never that the count is
// high. 196 stored bodies already carry a stacked title from before the fix, and
// an --assignee edit on one of them must not start failing: mg does not get to
// refuse work because of damage it did earlier. Their state is preserved, both
// as evidence and because they are not this write's fault.
func errDuplicateTitleHeading(id, title string, was, now int) *mgerr.Error {
	msg := fmt.Sprintf(
		"%s: this edit would leave the body with %d headings saying %q (it has %d), "+
			"and a reader only ever sees the first.\n"+
			"The title is read back from the body's first '# ' heading, so a second copy of "+
			"it is invisible to every command and diverges the moment either is edited.",
		id, now, title, was)

	hint := fmt.Sprintf(
		"The body you supplied already carries %q as a heading below its first one. "+
			"Either drop that duplicate from the body, or drop the leading heading and let "+
			"mg write it from --title — it writes exactly one.", title)

	return mgerr.Conflict("duplicate_title_heading", msg, hint)
}

// titleSideEffect describes an edit that would move the item's title without
// the caller having asked for it. It is the thing that was silent.
type titleSideEffect struct {
	From string
	To   string
}

// detectTitleSideEffect reports whether applying newBody to an item titled
// oldTitle would change the title, i.e. whether the new body's first heading
// says something other than the current title.
//
// A body with NO heading is not a side effect: the title is preserved by
// synthesis (case 1 above), which is why that shape is safe. A body whose first
// heading matches is not a side effect either. Only a body that renames the
// heading renames the item.
func detectTitleSideEffect(oldTitle, newBody string) (titleSideEffect, bool) {
	_, heading, found := firstHeadingLine(newBody)
	if !found || heading == oldTitle {
		return titleSideEffect{}, false
	}
	return titleSideEffect{From: oldTitle, To: heading}, true
}

// errSilentRetitle is the refusal that replaces the exit 0.
//
// Conflict (exit 4), not usage: the command is well-formed, and the caller may
// legitimately want either outcome. It cannot be retried unchanged — both
// remedies name a field — which is exactly the --if-unchanged contract, so it
// carries the same category.
//
// The hint names BOTH remedies because the two directions of this coupling were
// each reported as the bug by an agent who had measured only their own
// direction (mg-bac6). A caller who is here wants one of exactly two things,
// and the error should not presume which.
func errSilentRetitle(id string, se titleSideEffect) *mgerr.Error {
	msg := fmt.Sprintf(
		"%s: this body edit would also change the item's TITLE, which you did not ask to change.\n"+
			"  title now      %q\n"+
			"  body's heading %q\n"+
			"The title is not a stored field — it is read back from the body's first '# ' "+
			"heading — so replacing that heading renames the item.",
		id, se.From, se.To)

	hint := fmt.Sprintf(
		"Say which you meant. To RENAME the item to the body's heading, pass it: "+
			"'mg edit %s --title=%q --body-file ...'. To KEEP the current title, give "+
			"--body-file a body with no leading '# ' heading — mg writes the heading for "+
			"you from the title, and exactly one of them. Either way both fields are named, "+
			"so neither can move without you.",
		id, se.To)

	return mgerr.Conflict("title_side_effect", msg, hint)
}

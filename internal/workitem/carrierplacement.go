package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// Write-time refusal for a carrier declaration the DISPATCH PARSER CANNOT REACH
// (mg-f928, the authorship half of mg-27d4).
//
// The state carrier block — `workflow:` / `stage:` — is load-bearing: pogo reads
// `stage: gated` as a dispatch gate and `workflow: gh-issue` as the routing key.
// It reads them from ONE place: the run of `key: value` lines that begins the
// body, immediately after the title heading. Anywhere else the lines are prose
// to the parser and a declaration to every human who reads `mg show`.
//
// Two placements produce exactly that split, and both were measured on the real
// store rather than imagined:
//
//   - BELOW PROSE. The block scan stops at the first line that is not carrier
//     shaped, so a `stage:` line under a paragraph is never reached.
//   - ABOVE THE TITLE HEADING. The parser searches for the `# ` heading first
//     and only then starts the block scan, so everything above the heading is
//     consumed by the title search and never scanned at all. mg-779b and mg-9863
//     sit in available/ in exactly this state; what has held them is
//     `assignee: parked` — luck, not a mechanism.
//
// The ruling (pm-pogo, mg-27d4) was REFUSE, NOT WARN, AT WRITE TIME. A warning
// is what the filer is already not reading, and write time is the one moment
// where the person who can fix it is present. The gate side now refuses to
// dispatch such an item; without this half the mistake is still authored
// silently and surfaces later, to a different actor, as a parse detail.
//
// # THE PREDICATE, STATED (it will be audited, and it is not the ticket's)
//
// mg-27d4 records two counts of the same store that differ only by predicate —
// "18" (including prose and fenced examples) and 12 (unquoted, column-0,
// gate-bearing declarations in the leading region). Neither was wrong; they
// answered different questions. This is a THIRD predicate, and it is written
// down here rather than left implied by the code.
//
// A body line is an UNREACHABLE CARRIER DECLARATION when ALL of:
//
//  1. it is not inside a fenced code block (``` or ~~~, either fence character,
//     toggled on its own line);
//  2. it begins at COLUMN 0 with an identifier-shaped key — no leading space,
//     no leading backtick, no `>` quote;
//  3. that key is exactly `workflow` or `stage` — the two lines dispatch
//     actually routes and gates on. `gh:` is deliberately NOT included: a
//     misplaced `gh:` line changes no dispatch decision, and every refusal that
//     buys nothing is a refusal that gets turned off;
//  4. the colon is followed by a space or ends the line, and the value is a
//     single whitespace-free token — so `Reported: the daemon wedged overnight`
//     is a sentence, not a declaration;
//  5. and the line lies OUTSIDE the region the dispatch parser reads: above the
//     `# ` title heading, or after the block scan has already stopped, or past
//     the scan horizon.
//
// Conditions 2–4 are pogo's own parseCarrierLine rule, restated so that mg
// refuses on exactly what pogo would have read had the line been placed where
// it could be read. Condition 1 is ADDITIONAL and is the safety case: pogo's
// scanner has no notion of fences (it never needs one — it stops before it gets
// there), but a write-time check that scanned the whole body without one would
// refuse every ticket that documents the carrier convention, mg-69b1's own body
// among them. mg is the tool the whole fleet files with; a refusal that is too
// strict does not degrade, it stops ticket creation everywhere at once.
//
// # THE HORIZON
//
// The block scan is bounded: a declaration far enough below the heading is
// invisible even with no prose above it. This check mirrors that bound exactly
// (carrierReachHorizon below) rather than choosing a stricter one. Mirroring is
// what makes the refusal mean "the parser cannot reach this"; a stricter bound
// would refuse lines that are in fact read, which is a false refusal on the
// fleet's ticketing tool, and a looser one would let through the very shape this
// exists to catch. The deepest real declaration measured on the store is at
// offset 3.
//
// # WHAT IT DOES NOT DO
//
// It refuses only declarations THIS WRITE INTRODUCES. A body that already
// carries an unreachable declaration keeps it, and an edit that leaves it alone
// is not refused — for the same reason mg-d878 grandfathers the missing-carrier
// case: an append cannot author the leading block, so refusing it would leave
// the item with no append-only route to a correction at all. mg does not get to
// refuse work because of damage that predates the guard. It also never MOVES the
// line: relocating a caller's declaration would change what the item means to
// dispatch, silently, which is the defect and not the fix.

// carrierReachHorizon is how many lines after the title heading the dispatch
// block scan will read before giving up. It mirrors pogo's
// workitem.carrierBlockScanLimit (16 as of 2026-08-11) — deliberately the same
// number and not a rounder or safer one, because the refusal below claims the
// parser CANNOT REACH the line, and that claim is only true if this constant is
// the parser's. If pogo's limit ever moves, this one moves with it; the failure
// mode of drift is a false refusal in one direction and a missed catch in the
// other, and both are visible in the tests that pin the boundary.
const carrierReachHorizon = 16

// carrierDeclKeys are the body keys whose placement is refused: the two the
// dispatcher routes and gates on. See predicate condition 3.
var carrierDeclKeys = map[string]bool{"workflow": true, "stage": true}

// carrierPlacement names WHERE an unreachable declaration sits. The refusal
// message is keyed off it, because a filer told "carrier block unreachable"
// learns nothing, and one told "your stage: line is below prose" fixes it in
// ten seconds.
type carrierPlacement int

const (
	placementAboveTitle carrierPlacement = iota
	placementBelowProse
	placementPastHorizon
)

// unreachableDecl is one refusable line, with the context needed to say what is
// wrong with WHERE it is.
type unreachableDecl struct {
	line      int    // 1-based line number within the stored body
	text      string // the declaration line, verbatim
	key       string // "workflow" or "stage"
	placement carrierPlacement
	blocker   string // the prose line that ended the scan (placementBelowProse only)
	blockerLn int    // its 1-based line number
}

// parseCarrierDecl applies predicate conditions 2–4 to one raw line: column-0
// identifier key, colon then space-or-end, single whitespace-free value. It is
// intentionally a restatement of pogo's parseCarrierLine and not a loosening of
// it — mg refuses on what pogo would have read.
func parseCarrierDecl(line string) (key, val string, ok bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = line[:idx]
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '_' || c == '-':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return "", "", false
		}
	}
	rest := line[idx+1:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", "", false
	}
	val = strings.TrimSpace(rest)
	if strings.ContainsAny(val, " \t") {
		return "", "", false
	}
	return strings.ToLower(key), val, true
}

// isFenceLine reports whether a line opens or closes a fenced code block. Both
// fence characters count, and an indented fence counts too — a fence inside a
// list item is still a fence, and everything it contains is an example.
func isFenceLine(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// findUnreachableDecls returns every carrier declaration in body that the
// dispatch parser cannot reach, in line order.
//
// It models the READER, not mg's own leadingWorkflow: mg's reader skips heading
// lines and has no horizon, so it sees strictly more than dispatch does, and it
// is the gap between the two that this whole file exists to close. The model is
// three regions:
//
//	[ 1 .. titleLine )        consumed by the title search — NEVER scanned
//	( titleLine .. blockEnd ] the carrier block — the only readable region
//	( blockEnd .. EOF ]       after the scan stopped — NEVER scanned
//
// where blockEnd is the first line after the heading (leading blanks skipped)
// that is not carrier shaped, or the horizon, whichever comes first.
func findUnreachableDecls(body string) []unreachableDecl {
	lines := strings.Split(body, "\n")

	// The title search, by Parse's rule: the FIRST line beginning "# ".
	// Positional, exactly as the read side is — see titleheading.go.
	titleLine := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			titleLine = i
			break
		}
	}

	// Walk the readable region to find where the block scan stops, and what
	// stopped it. With no heading there is no readable region at all: the
	// dispatch parser only starts scanning once it has found the title.
	blockEnd := titleLine // exclusive upper bound, in line indices
	blocker, blockerLn := "", 0
	if titleLine >= 0 {
		started := false
		seen := 0
		i := titleLine + 1
		for ; i < len(lines) && seen < carrierReachHorizon; i++ {
			seen++
			if !started && strings.TrimSpace(lines[i]) == "" {
				continue
			}
			if _, _, ok := parseCarrierDecl(lines[i]); !ok {
				blocker, blockerLn = strings.TrimSpace(lines[i]), i+1
				break
			}
			started = true
		}
		blockEnd = i
	}

	var out []unreachableDecl
	inFence := false
	for i, line := range lines {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if titleLine >= 0 && i > titleLine && i < blockEnd {
			continue // inside the block the parser actually reads
		}
		key, _, ok := parseCarrierDecl(line)
		if !ok || !carrierDeclKeys[key] {
			continue
		}
		d := unreachableDecl{line: i + 1, text: strings.TrimRight(line, " \t"), key: key}
		switch {
		case titleLine < 0 || i < titleLine:
			d.placement = placementAboveTitle
		case blocker == "" && blockerLn == 0:
			// The scan ran out of horizon rather than hitting prose.
			d.placement = placementPastHorizon
		default:
			d.placement = placementBelowProse
			d.blocker, d.blockerLn = blocker, blockerLn
		}
		out = append(out, d)
	}
	return out
}

// checkCarrierPlacement refuses a write that INTRODUCES a carrier declaration
// the dispatch parser cannot reach. newBody and oldBody are stored bodies
// (composeBody output) — the bytes that hit disk, including the synthesised
// title heading, because a check run on the caller's string is checking
// something no parser will ever see. oldBody is "" for a filing, where every
// line is introduced by definition.
//
// Inheritance is matched on the declaration TEXT, not its position: an edit that
// merely carries a pre-existing bad line along is not this write's fault, and
// the conservative direction here is the one that does not block the fleet.
func checkCarrierPlacement(newBody, oldBody string) error {
	found := findUnreachableDecls(newBody)
	if len(found) == 0 {
		return nil
	}
	inherited := map[string]bool{}
	for _, d := range findUnreachableDecls(oldBody) {
		inherited[strings.TrimSpace(d.text)] = true
	}
	for _, d := range found {
		if inherited[strings.TrimSpace(d.text)] {
			continue
		}
		return errCarrierUnreachable(d)
	}
	return nil
}

// errCarrierUnreachable states the PLACEMENT and the move that fixes it. It
// names the offending line and, where one exists, the line that ended the scan,
// because the two together are the whole explanation and neither is guessable
// from a body that reads correctly to a human.
func errCarrierUnreachable(d unreachableDecl) *mgerr.Error {
	var msg, hint string
	switch d.placement {
	case placementAboveTitle:
		msg = fmt.Sprintf(
			"body line %d declares %q, but it sits ABOVE the body's '# ' title heading.\n"+
				"Dispatch searches for the title heading FIRST and only then reads the carrier "+
				"block, so every line above the heading is consumed by that search and never "+
				"scanned. The item would read as declared to a human and as undeclared to the "+
				"router.\n"+
				"YOUR BODY MAY LOOK CORRECT: a body whose carrier block leads it and which "+
				"carries its OWN '# ' heading further down lands in exactly this state, because "+
				"that heading becomes the item's title and the block is then above it.",
			d.line, d.text)
		hint = "Drop the '# ' heading from your body and let mg write it from --title — it puts " +
			"the heading above the block for you, which is the shape dispatch reads. If you " +
			"want to keep your own heading, move it ABOVE the carrier block."
	case placementPastHorizon:
		msg = fmt.Sprintf(
			"body line %d declares %q, but it is more than %d lines below the '# ' title "+
				"heading.\nThe carrier block scan is bounded at %d lines, so this declaration "+
				"is never read.", d.line, d.text, carrierReachHorizon, carrierReachHorizon)
		hint = "Move the carrier block to directly BELOW the '# ' title heading — it is the " +
			"first few lines after the heading that are read, and nothing else."
	default:
		msg = fmt.Sprintf(
			"body line %d declares %q, but it sits BELOW a line of prose (line %d: %q).\n"+
				"The carrier block is only the run of 'key: value' lines that STARTS the body "+
				"after the title heading; the scan stops at that prose line and never reaches "+
				"your declaration. Dispatch would route this item as if the line were not "+
				"there.", d.line, d.text, d.blockerLn, truncateForMsg(d.blocker))
		hint = fmt.Sprintf(
			"Move the '%s:' line — and the rest of its block — to directly under the '# ' "+
				"title heading, above all prose.", d.key)
	}
	hint += "\n\nA carrier line that is only being QUOTED rather than declared is not refused: " +
		"put it in a fenced code block (```), indent it, or wrap it in backticks, and it " +
		"reads as prose to this check exactly as it does to dispatch."
	return mgerr.Usage("carrier_declaration_unreachable", msg, hint)
}

// truncateForMsg keeps a quoted prose line short enough that the refusal stays
// readable when the blocker is a paragraph.
func truncateForMsg(s string) string {
	const max = 60
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

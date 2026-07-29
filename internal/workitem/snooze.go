package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// Snooze is "not now, come back at <time>" — and it is an ATTRIBUTE on
// pending/, not a sixth status.
//
// The core model is: an item is a file, and its status IS the directory it
// sits in. Parse accepts no `status` key (see workitem.go) and Resolve sets
// Status from the directory it scanned (see resolve.go) — status is observed,
// never stored. A sixth directory would be adding to that model; an attribute
// an EXISTING gate consults is not. So `depends:` waits on an ITEM, `snooze:`
// waits on a CLOCK, and both AND into one predicate (gateOpen) evaluated in
// one place, rather than mg growing a second gating mechanism that does not
// know about the first.
//
// # Why the driver is part of this file's contract, not a follow-up
//
// An attribute on its own would be worse than no snooze at all. A snoozed item
// is not in available/, so stall-watch and priority-wake cannot see it, and
// `pending` is exactly what a correctly-waiting item looks like. That is the
// invisibility that left mg-344a and mg-b8f9 fifteen days behind a gate whose
// date had already passed: it looked scheduled, nothing nagged, and nobody
// goes looking for a ticket that is behaving.
//
// Three properties make it impossible to set a snooze that nothing will open:
//
//  1. The gate is LEVEL-triggered, never edge-triggered. The sweep asks "is
//     the wake time in the past?", not "did the wake time just arrive". A
//     driver that is down through the wake instant therefore DELAYS an item;
//     it can never lose one. This is what makes a missed fire survivable, and
//     it is the property the acceptance case exercises.
//  2. `mg snooze` refuses a malformed or already-past time, loudly, at snooze
//     time — and a `snooze:` value that reaches disk unparseable anyway (a
//     hand-edit) holds the item AND is named by `mg schedule`'s stranded
//     report, rather than being silently ignored in either direction.
//  3. `mg snooze` refuses at all when nothing has driven the sweep recently.
//     The driver is not a nice-to-have that can be filed as a successor: it is
//     a precondition mg itself checks. See CheckDriver.
//
// # Shape, for mg-8970
//
// This attribute is NOT stage-shaped. It is a single frontmatter timestamp,
// `snooze: <RFC3339 UTC>`, and it carries no workflow stage, no successor
// obligation, and no remainder. An item that is snoozed owes nothing; it is a
// declared PAUSE. A successor-guard predicate therefore does not need to
// distinguish "paused" from "owes a remainder" by inspecting a stage value —
// a snoozed item declares nothing for such a guard to read.

// SnoozeKey is the frontmatter key. Named once so the parser, the renderer,
// and every operator-facing message spell it the same way.
const SnoozeKey = "snooze"

// dateOnlyHour is the local hour a date-only snooze (`--until 2026-08-03`)
// resolves to. Midnight is the wrong default for "come back on Monday", and
// leaving it unstated is how a DST boundary turns into an undebuggable
// off-by-one-hour. 09:00 local matches the convention the fleet's own gates
// already used. `mg snooze` echoes the resolved absolute instant either way,
// so the choice is never invisible.
const dateOnlyHour = 9

// DriverStaleAfter is how long `mg schedule` may go unrun before `mg snooze`
// treats the driver as absent and refuses. With the recommended */15 cron this
// is eight consecutive missed fires — comfortably past scheduling jitter, a
// pogod restart, or a short host sleep, and far short of the fifteen days that
// made this feature necessary.
const DriverStaleAfter = 2 * time.Hour

// DriverHint is the exact command that wires the driver. It is a single string
// because a refusal that says "register a driver" without saying how is a
// refusal the reader has to go research.
const DriverHint = `pogo schedule mayor --cron "*/15 * * * *" --id mg-schedule-sweep \
    --message "Run: mg schedule"`

// snoozeNow is the clock every snooze gate is evaluated against. It is
// deliberately separate from nowFunc (which pins ID minting in collision
// tests) so a test that freezes one does not silently move the other.
var snoozeNow = func() time.Time { return time.Now().UTC() }

// snoozeHolds reports whether this item's snooze gate is still closed at now.
//
// A `snooze:` value that is present but unparseable holds the item. The
// alternative — ignoring it and promoting — turns a typo into a silent early
// wake with nothing anywhere recording that a gate was discarded. Holding is
// visible: Stranded names it and `mg schedule` prints it.
func (i *Item) snoozeHolds(now time.Time) bool {
	if i.SnoozeRaw == "" {
		return false
	}
	if i.Snooze.IsZero() {
		return true // malformed: a gate nothing can open, reported by Stranded
	}
	return now.Before(i.Snooze)
}

// SnoozeMalformed reports whether the item carries a `snooze:` value that is
// not a valid RFC3339 timestamp.
func (i *Item) SnoozeMalformed() bool {
	return i.SnoozeRaw != "" && i.Snooze.IsZero()
}

// Snoozed reports whether the item carries a snooze gate that has not yet
// opened, as of now.
func (i *Item) Snoozed(now time.Time) bool { return i.snoozeHolds(now) }

// gateOpen is THE predicate for "may this item be released to available/".
//
// Every gate ANDs here. Before snooze, four call sites each asked only about
// dependencies (schedule.go, edit.go twice, shelve.go), and adding a clock to
// each of them independently is how one rule acquires four implementations
// that drift. Adding a gate now means editing this function.
func gateOpen(item *Item, doneIDs map[string]bool, now time.Time) bool {
	return allDepsMet(item.Depends, doneIDs) && !item.snoozeHolds(now)
}

// SetSnooze stamps the gate. The stored value is always RFC3339 in UTC: the
// input may be local, the record never is, so a store read on another machine
// or after a DST shift means the same instant it meant when it was written.
func (i *Item) SetSnooze(t time.Time) {
	i.Snooze = t.UTC()
	i.SnoozeRaw = i.Snooze.Format(time.RFC3339)
}

// ClearSnooze removes the gate, verbatim value included.
func (i *Item) ClearSnooze() {
	i.Snooze = time.Time{}
	i.SnoozeRaw = ""
}

// --- operator input -------------------------------------------------------

// snoozeLayout is one accepted spelling of an absolute wake time. local says
// whether a layout carries no zone of its own and is therefore read in the
// host's location.
type snoozeLayout struct {
	layout string
	local  bool
	// dateOnly marks the layout that names a day but no time, which resolves
	// to dateOnlyHour local rather than to midnight.
	dateOnly bool
}

var snoozeLayouts = []snoozeLayout{
	{layout: time.RFC3339},
	{layout: "2006-01-02T15:04:05", local: true},
	{layout: "2006-01-02T15:04", local: true},
	{layout: "2006-01-02 15:04:05", local: true},
	{layout: "2006-01-02 15:04", local: true},
	{layout: "2006-01-02", local: true, dateOnly: true},
}

// SnoozeFormats renders the accepted --until spellings for help and error
// text, so the parser and the documentation cannot disagree about them.
func SnoozeFormats() string {
	out := make([]string, 0, len(snoozeLayouts))
	for _, l := range snoozeLayouts {
		out = append(out, l.layout)
	}
	return strings.Join(out, ", ")
}

// ParseSnoozeUntil resolves an absolute wake time given by an operator into an
// instant. Layouts carrying no zone are read in loc (the host's location);
// RFC3339 input keeps the zone it names. A date with no time resolves to
// dateOnlyHour in loc.
func ParseSnoozeUntil(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if loc == nil {
		loc = time.Local
	}
	for _, l := range snoozeLayouts {
		var (
			t   time.Time
			err error
		)
		if l.local {
			t, err = time.ParseInLocation(l.layout, s, loc)
		} else {
			t, err = time.Parse(l.layout, s)
		}
		if err != nil {
			continue
		}
		if l.dateOnly {
			t = time.Date(t.Year(), t.Month(), t.Day(), dateOnlyHour, 0, 0, 0, loc)
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time I recognise", s)
}

// ParseSnoozeFor resolves a relative wake time ("2h", "3d", "1w") against now.
//
// Go's ParseDuration stops at hours, but the unit this feature exists for is
// days: "come back Monday" is the case that overloaded `assignee` in the first
// place. `d` and `w` are therefore accepted on a bare <number><unit> form; any
// other spelling falls through to ParseDuration unchanged.
func ParseSnoozeFor(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty duration")
	}
	if n, unit, ok := splitCount(s); ok && (unit == "d" || unit == "w") {
		days := n
		if unit == "w" {
			days = n * 7
		}
		// AddDate rather than a multiple of 24h: adding days across a DST
		// boundary should land on the same wall-clock time, which is what an
		// operator writing "3d" means.
		return now.Local().AddDate(0, 0, days).UTC(), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a duration", s)
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("%q is not a positive duration", s)
	}
	return now.Add(d).UTC(), nil
}

// splitCount splits a bare "<positive integer><letters>" string. It reports
// false for anything else, so ParseSnoozeFor can fall through untouched.
func splitCount(s string) (n int, unit string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i == len(s) {
		return 0, "", false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, s[i:], true
}

// HumanUntil renders a duration the way a wake time reads best: coarse, and
// never more precise than the sweep cadence can honour.
func HumanUntil(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) - days*24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}

// --- transitions ----------------------------------------------------------

// snoozableStatuses mirrors Shelve's validation exactly: an item can be set
// aside from any live, non-shelved status. done/ and archive/ are historical
// records — scheduling a record to reappear is not a thing snooze means.
var snoozableStatuses = map[string]bool{"available": true, "claimed": true, "pending": true}

// SnoozeItem stamps a wake time on an item and files it in pending/, which is
// where an item waiting on a gate already lives. It returns the updated item
// and the status it came from.
//
// until must already be validated as future by the caller; the CLI does that
// so the refusal can name the flag the operator used.
func SnoozeItem(root, id string, until time.Time) (*Item, string, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, "", err
	}
	if !snoozableStatuses[status] {
		return nil, "", explainSnoozeFailure(id, status)
	}

	item, err := readFile(path)
	if err != nil {
		return nil, "", err
	}
	item.SetSnooze(until)

	// Write the attribute where the file already is, then move it. A crash
	// between the two leaves an item carrying a future snooze in the directory
	// it started in — inert but visible — rather than an item in pending/ with
	// no gate on it, which is the shape that waits forever.
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		return nil, "", ioErr(fmt.Sprintf("%s: could not be snoozed: %s", id, fsErrText(err)))
	}

	// Always <id>.md at the destination: a claimed item carries a PID suffix,
	// and snoozing releases the claim the same way shelving does.
	dst := filepath.Join(root, "work", "pending", id+".md")
	if path != dst {
		if err := os.Rename(path, dst); err != nil {
			return nil, "", ioErr(fmt.Sprintf("%s: could not be moved to pending: %s", id, fsErrText(err)))
		}
		if err := moveResultSidecar(filepath.Dir(path), filepath.Dir(dst), id); err != nil {
			return nil, "", ioErr(fmt.Sprintf("%s: snoozed, but result sidecar could not follow: %s", id, fsErrText(err)))
		}
	}

	event.Emit(root, "work.snooze", map[string]string{
		"item_id":     id,
		"from_status": status,
		"to_status":   "pending",
		"until":       item.SnoozeRaw,
		"actor":       actor(),
	})

	return item, status, nil
}

// UnsnoozeItem removes the gate and re-files the item wherever its remaining
// gates put it. It is the escape hatch: a snooze that can only be undone by
// hand-editing YAML is a snooze people work around instead of correcting.
func UnsnoozeItem(root, id string) (*Item, string, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, "", err
	}

	item, err := readFile(path)
	if err != nil {
		return nil, "", err
	}
	if item.SnoozeRaw == "" {
		return nil, "", mgerr.Conflict("not_snoozed",
			fmt.Sprintf("%s: is not snoozed, so there is nothing to lift.", id),
			fmt.Sprintf("Run 'mg show %s' to see its gates.", id))
	}

	item.ClearSnooze()
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		return nil, "", ioErr(fmt.Sprintf("%s: could not be unsnoozed: %s", id, fsErrText(err)))
	}

	// Only pending/ is re-placed. An inert snooze on an available, claimed or
	// shelved item (only a hand-edit can produce one) is cleared where it lies:
	// lifting a gate is not licence to move an item nobody asked to move.
	dest := status
	if status == "pending" {
		doneIDs, err := doneIDSet(root)
		if err != nil {
			return nil, "", err
		}
		if gateOpen(item, doneIDs, snoozeNow()) {
			dst := filepath.Join(root, "work", "available", id+".md")
			if err := os.Rename(path, dst); err != nil {
				return nil, "", ioErr(fmt.Sprintf("%s: unsnoozed, but could not be promoted: %s", id, fsErrText(err)))
			}
			if err := moveResultSidecar(filepath.Dir(path), filepath.Dir(dst), id); err != nil {
				return nil, "", ioErr(fmt.Sprintf("%s: promoted, but result sidecar could not follow: %s", id, fsErrText(err)))
			}
			dest = "available"
		}
	}

	event.Emit(root, "work.unsnooze", map[string]string{
		"item_id":     id,
		"from_status": status,
		"to_status":   dest,
		"actor":       actor(),
	})

	return item, dest, nil
}

// explainSnoozeFailure names why an item in this status cannot be snoozed.
func explainSnoozeFailure(id, status string) *mgerr.Error {
	switch status {
	case "":
		return errNoSuchItem(id)
	case "shelved":
		return mgerr.Conflict("item_shelved",
			fmt.Sprintf("%s: is shelved, which already means 'not now'.", id),
			remediation("shelved", id))
	case "done":
		return mgerr.Conflict("already_done",
			fmt.Sprintf("%s: is done — snooze schedules work, not records.", id),
			remediation("done", id))
	case "archived":
		return mgerr.Conflict("item_archived",
			fmt.Sprintf("%s: is archived — snooze schedules work, not records.", id),
			remediation("archived", id))
	default:
		return mgerr.Conflict("cannot_snooze",
			fmt.Sprintf("%s: cannot be snoozed from status %s.", id, status), "")
	}
}

// SnoozedPending returns the pending items whose snooze gate is still closed,
// soonest wake first. It is a reader for the attribute: a gate that cannot be
// listed is a gate nobody can audit, which is the whole defect.
func SnoozedPending(root string) ([]*Item, error) {
	pending, err := ListByStatus(root, "pending")
	if err != nil {
		return nil, err
	}
	now := snoozeNow()
	var out []*Item
	for _, item := range pending {
		if item.SnoozeMalformed() {
			continue // reported by Stranded, which is the louder channel
		}
		if item.snoozeHolds(now) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Snooze.Before(out[j].Snooze) })
	return out, nil
}

// --- the driver's pulse ---------------------------------------------------

// sweepStampName is the file `mg schedule` stamps on every run. It lives in
// work/ beside the directories it reports on, is plain RFC3339 text, and is
// therefore answerable with `cat` — Design Principle 1 applies to the driver's
// own liveness as much as to an item's status.
const sweepStampName = ".last-sweep"

func sweepStampPath(root string) string {
	return filepath.Join(root, "work", sweepStampName)
}

// RecordSweep stamps the moment the sweep ran. It is called ONLY by the
// `mg schedule` command, never by Done's internal sweep: the question this
// stamp answers is "is something driving the sweep on a clock", and a sweep
// that happened because somebody finished a ticket is not an answer to it.
func RecordSweep(root string) error {
	return os.WriteFile(sweepStampPath(root), []byte(snoozeNow().Format(time.RFC3339)+"\n"), 0o644)
}

// DriverStatus is what mg knows about whatever is driving `mg schedule`.
type DriverStatus struct {
	// Last is the previous recorded sweep; zero when Ever is false.
	Last time.Time
	// Ever is false when no sweep has ever been recorded in this store.
	Ever bool
	// Stale is true when nothing has driven the sweep inside DriverStaleAfter
	// — including the never-run case.
	Stale bool
	// Since is how long ago Last was; zero when Ever is false.
	Since time.Duration
}

// CheckDriver reports whether anything is currently driving the sweep.
func CheckDriver(root string, now time.Time) DriverStatus {
	data, err := os.ReadFile(sweepStampPath(root))
	if err != nil {
		return DriverStatus{Stale: true}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return DriverStatus{Stale: true}
	}
	since := now.Sub(t)
	return DriverStatus{Last: t, Ever: true, Since: since, Stale: since > DriverStaleAfter}
}

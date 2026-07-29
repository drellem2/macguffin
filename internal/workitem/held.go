package workitem

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Held answers one question the sweep could not previously answer out loud:
// which pending items did this run NOT promote, and what is holding each one?
//
// The snooze gate already had a reader (SnoozedPending) and `mg schedule`
// printed it; the dependency gate had none, so a sweep over a pending set held
// entirely by dependencies printed "No items promoted." and nothing else. That
// sentence is true and incomplete, and the reading it invites — nothing is
// waiting — is wrong exactly when a dependency gate is closed and nobody is
// watching, which is the case the cron exists for.
//
// So the population this reports on is the whole held population, not one
// gate's worth of it. Both gates are listed per item, because they are
// independent: an item can be past its wake time and still waiting on a
// parent, and either one can be the gate that is still closed.

// Hold is a pending item the sweep could not promote, carried with every gate
// of its that is still closed. Deps lists ONLY the unsatisfied dependencies —
// a met parent is not holding anything and naming it would bury the one that
// is.
type Hold struct {
	Item *Item

	// Deps are the dependencies that are not yet satisfied, each with the
	// parent's resolved state and status.
	Deps []Dep

	// Snoozed is true when a parseable wake time is still in the future.
	Snoozed bool

	// BadSnooze is the `snooze:` value verbatim when it is not parseable. Such
	// a gate holds the item and can never open (see snoozeHolds); Stranded is
	// the louder channel for it, but it is still a gate and this list is the
	// gate list.
	BadSnooze string
}

// wake is the instant this item's snooze gate opens, or the zero time when it
// has no parseable future wake time.
func (h Hold) wake() time.Time {
	if h.Snoozed {
		return h.Item.Snooze
	}
	return time.Time{}
}

// Gates renders every closed gate on one line, in the order they are most
// likely to be acted on: the parent to chase first, then the clock.
func (h Hold) Gates(now time.Time) string {
	parts := make([]string, 0, 2)
	if len(h.Deps) > 0 {
		ids := make([]string, 0, len(h.Deps))
		for _, d := range h.Deps {
			ids = append(ids, d.describe())
		}
		parts = append(parts, "depends: "+strings.Join(ids, ", "))
	}
	switch {
	case h.BadSnooze != "":
		parts = append(parts, fmt.Sprintf("snoozed: %q is not an RFC3339 timestamp, so this gate can never open", h.BadSnooze))
	case h.Snoozed:
		parts = append(parts, fmt.Sprintf("snoozed: wakes %s (in %s)",
			h.Item.SnoozeRaw, HumanUntil(h.Item.Snooze.Sub(now))))
	}
	return strings.Join(parts, "; ")
}

// describe names a dependency and what it is doing, e.g. "mg-2c34 (claimed)".
// The status, not the coarse DepState: "waiting" is true of an available item
// nobody has picked up and of one that is being worked right now, and those
// call for different reactions from whoever reads the sweep.
func (d Dep) describe() string {
	if d.State == DepUnknown || d.Status == "" {
		return d.ID + " (does not exist)"
	}
	return fmt.Sprintf("%s (%s)", d.ID, d.Status)
}

// Held returns every pending item with at least one closed gate, soonest-wake
// first and dependency-only holds last, ties broken by ID so the report is
// stable across runs.
//
// Called after Schedule, this is exactly the set the sweep stepped over: an
// item with no closed gate has already been promoted out of pending/ by the
// time this runs. An un-gated item still sitting in pending/ is therefore not
// reported here — it is not held, it is a promotion that is about to happen.
func Held(root string) ([]Hold, error) {
	pending, err := ListByStatus(root, "pending")
	if err != nil {
		return nil, err
	}

	now := snoozeNow()
	var out []Hold
	for _, item := range pending {
		deps, err := classifyDeps(root, item.Depends)
		if err != nil {
			return nil, err
		}
		var unmet []Dep
		for _, d := range deps {
			if d.State != DepSatisfied {
				unmet = append(unmet, d)
			}
		}

		h := Hold{Item: item, Deps: unmet}
		if item.SnoozeMalformed() {
			h.BadSnooze = item.SnoozeRaw
		} else if item.snoozeHolds(now) {
			h.Snoozed = true
		}

		if len(h.Deps) == 0 && !h.Snoozed && h.BadSnooze == "" {
			continue
		}
		out = append(out, h)
	}

	sort.Slice(out, func(i, j int) bool {
		wi, wj := out[i].wake(), out[j].wake()
		switch {
		case !wi.IsZero() && !wj.IsZero() && !wi.Equal(wj):
			return wi.Before(wj)
		case wi.IsZero() != wj.IsZero():
			// A known wake time is a date somebody can plan around; a
			// dependency hold has no date at all, so it sorts after.
			return !wi.IsZero()
		}
		return out[i].Item.ID < out[j].Item.ID
	})
	return out, nil
}

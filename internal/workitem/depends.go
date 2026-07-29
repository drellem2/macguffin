package workitem

import (
	"fmt"
	"strings"
)

// This file answers one question in one place: what does it mean for a
// dependency to be met, and where does a dependent belong right now?
//
// Before it existed, that rule lived only in Schedule's sweep — and the sweep
// runs on exactly one trigger, `Done`. Anything that put an item into pending/
// without a subsequent completion left it there. Two of the three strands the
// fleet measured were of that shape: a dependent filed onto a parent that had
// ALREADY finished sat in pending/ until some unrelated item happened to be
// completed and swept it up, and a dependent filed onto a SHELVED parent sat
// there forever, because nothing moves a shelved item to done.
//
// The failure is invisible in both directions: an unreleased child is not
// available/, so stall-watch and priority-wake cannot see it, and `pending` is
// exactly what a correctly-waiting item looks like. So placement is decided
// here, at the moment of filing, against resolved facts — not deferred to a
// sweep that may never run.

// DepState is the resolved condition of one dependency.
type DepState string

const (
	// DepSatisfied — the parent reached done. done/ and archive/ BOTH count:
	// archiving is a filing decision about completed work, not a repudiation of
	// the completion. A parent that has passed through done stays satisfied.
	DepSatisfied DepState = "satisfied"

	// DepWaiting — the parent is live (available, claimed, or pending). It will
	// release this dependent when it completes, via Done -> Schedule. This is
	// the only state in which `pending` is the honest placement.
	DepWaiting DepState = "waiting"

	// DepShelved — the parent is shelved. Nothing in the lifecycle moves a
	// shelved item to done, so a dependent left `pending` on one waits forever
	// while looking exactly like a dependent that is waiting correctly.
	DepShelved DepState = "shelved"

	// DepUnknown — no item with this ID exists anywhere in the store: a typo, or
	// a parent removed out from under the dependent. Equally unreachable, and
	// equally invisible. Placement does not refuse it (forward-references and
	// hand-written fixtures rely on it), but Stranded reports it.
	DepUnknown DepState = "unknown"
)

// Dep is one dependency ID paired with its resolved state.
type Dep struct {
	ID    string
	State DepState
}

// Reachable reports whether some future event could still satisfy this
// dependency. A shelved parent needs an explicit `mg unshelve` and an unknown
// parent needs to be created or the dependency corrected; neither happens on
// its own, so neither is reachable by waiting.
func (d Dep) Reachable() bool {
	return d.State == DepSatisfied || d.State == DepWaiting
}

// classifyDeps resolves every dependency ID to its current state. It reads the
// store and moves nothing.
func classifyDeps(root string, deps []string) ([]Dep, error) {
	if len(deps) == 0 {
		return nil, nil
	}

	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}

	out := make([]Dep, 0, len(deps))
	for _, id := range deps {
		out = append(out, Dep{ID: id, State: depStateFor(root, id, doneIDs)})
	}
	return out, nil
}

// depStateFor resolves a single dependency. doneIDs is consulted first because
// it is the authority on "passed through done" — it spans done/ and every
// archive partition, which is the whole point of dependency satisfaction
// surviving an archive.
func depStateFor(root, id string, doneIDs map[string]bool) DepState {
	if doneIDs[id] {
		return DepSatisfied
	}

	matches, err := Resolve(root, id)
	if err != nil || len(matches) == 0 {
		// A resolution error here is ambiguity, not absence, and an ambiguous
		// parent is not something placement should act on. Treat it as unknown:
		// Stranded will name it, and naming it is the useful outcome either way.
		return DepUnknown
	}

	// Prefer the live match. An ID that shadows an archived twin is answered by
	// the live item everywhere else in mg (see ResolveUnique); dependency
	// resolution must not be the one place that disagrees.
	for _, m := range matches {
		if m.Live() {
			if m.Status == "shelved" {
				return DepShelved
			}
			return DepWaiting
		}
	}

	// Only terminal matches remain — done or archived, both satisfied.
	return DepSatisfied
}

// placeForDeps returns the work/ subdirectory an item with these dependencies
// belongs in, together with the dependencies that forced the choice.
//
// Shelved wins over everything: a dependent of a parked parent is itself
// parked, which is what Shelve already does to dependents that exist when the
// parent is shelved (see findDependents). Filing is the only door that was
// still open, and it now closes the same way — so "shelved" means the same
// thing whichever side of the parent's shelving the dependent was created on.
func placeForDeps(deps []Dep) (subdir string, blocking []Dep) {
	var shelved []Dep
	allSatisfied := true
	for _, d := range deps {
		if d.State == DepShelved {
			shelved = append(shelved, d)
		}
		if d.State != DepSatisfied {
			allSatisfied = false
		}
	}

	switch {
	case len(shelved) > 0:
		return "shelved", shelved
	case len(deps) == 0 || allSatisfied:
		return "available", nil
	default:
		return "pending", nil
	}
}

// depIDs renders dependency IDs for an operator-facing message.
func depIDs(deps []Dep) string {
	ids := make([]string, 0, len(deps))
	for _, d := range deps {
		ids = append(ids, d.ID)
	}
	return strings.Join(ids, ", ")
}

// ShelvedDeps returns the IDs of the given dependencies that are currently
// shelved. It exists so the CLI can name the parent responsible for a shelved
// placement without reimplementing the classification rule.
func ShelvedDeps(root string, depends []string) ([]string, error) {
	deps, err := classifyDeps(root, depends)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range deps {
		if d.State == DepShelved {
			out = append(out, d.ID)
		}
	}
	return out, nil
}

// Strand is a pending item that no future completion can release, carried with
// the dependencies responsible. A bare list of IDs would leave the operator to
// work out which parent stalled which child, which is the guesswork the strand
// caused in the first place.
type Strand struct {
	Item     *Item
	Blocking []Dep
}

// Reason renders why this item can never be released, naming each unreachable
// parent and its state.
func (s Strand) Reason() string {
	parts := make([]string, 0, len(s.Blocking))
	for _, d := range s.Blocking {
		switch d.State {
		case DepShelved:
			parts = append(parts, fmt.Sprintf("%s is shelved", d.ID))
		case DepUnknown:
			parts = append(parts, fmt.Sprintf("%s does not exist", d.ID))
		default:
			parts = append(parts, fmt.Sprintf("%s is %s", d.ID, d.State))
		}
	}
	return strings.Join(parts, "; ")
}

// Stranded scans pending/ for items that no completion can ever release: those
// waiting on a shelved or nonexistent parent.
//
// This is a detector, and a detector is worth shipping only with a reader —
// `mg schedule` reports what this returns, right where it reports what it
// promoted. Placement (see placeForDeps) is what stops NEW strands; Stranded
// exists for the ones already on disk, and for the paths that can still create
// one after filing, such as `mg edit --add-depends` naming a shelved parent.
func Stranded(root string) ([]Strand, error) {
	pending, err := ListByStatus(root, "pending")
	if err != nil {
		return nil, err
	}

	var out []Strand
	for _, item := range pending {
		deps, err := classifyDeps(root, item.Depends)
		if err != nil {
			return nil, err
		}
		var blocking []Dep
		for _, d := range deps {
			if !d.Reachable() {
				blocking = append(blocking, d)
			}
		}
		if len(blocking) > 0 {
			out = append(out, Strand{Item: item, Blocking: blocking})
		}
	}
	return out, nil
}

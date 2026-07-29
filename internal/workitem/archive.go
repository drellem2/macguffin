package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// archiveCandidate is one done item eligible for archiving, carried with the
// facts the move needs so that scanning and moving stay separable (see
// archiveCandidates / ArchiveDryRun).
type archiveCandidate struct {
	path     string
	item     *Item
	doneTime time.Time
}

// archiveCandidates scans done/ and returns the items older than maxAge. It is
// pure: it reads the store and moves nothing. Archive and ArchiveDryRun share
// it so that a preview cannot drift from the mutation it previews.
func archiveCandidates(root string, maxAge time.Duration) ([]archiveCandidate, error) {
	doneDir := filepath.Join(root, "work", "done")
	entries, err := os.ReadDir(doneDir)
	if err != nil {
		return nil, fmt.Errorf("reading done/: %w", err)
	}

	now := time.Now()
	var cands []archiveCandidate

	for _, e := range entries {
		// Skip non-markdown files (e.g., .result.json sidecars)
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		path := filepath.Join(doneDir, e.Name())
		item, err := readFile(path)
		if err != nil {
			continue // skip malformed files
		}

		// Use file modification time as the "done" time
		info, err := e.Info()
		if err != nil {
			continue
		}
		doneTime := info.ModTime()

		if now.Sub(doneTime) < maxAge {
			continue
		}

		cands = append(cands, archiveCandidate{path: path, item: item, doneTime: doneTime})
	}

	return cands, nil
}

// archiveFile moves a single done item — its .md and any result sidecar — into
// the archive partition for doneTime (e.g. archive/2026-03/), and emits the
// archive event. It handles exactly one item and has no notion of a sweep;
// both Archive and ArchiveItem route their moves through it so the two forms
// cannot drift apart on partitioning, sidecar handling, or eventing.
func archiveFile(root string, c archiveCandidate) error {
	name := filepath.Base(c.path)

	// Determine archive partition (YYYY-MM based on done time)
	partition := c.doneTime.Format("2006-01")
	archiveDir := filepath.Join(root, "work", "archive", partition)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return fmt.Errorf("creating archive dir %s: %w", archiveDir, err)
	}

	// Move the .md file
	dstPath := filepath.Join(archiveDir, name)
	if err := os.Rename(c.path, dstPath); err != nil {
		return fmt.Errorf("archiving %s: %w", c.item.ID, err)
	}

	// Move result sidecar if it exists, so it travels with the .md.
	id := strings.TrimSuffix(name, ".md")
	if err := moveResultSidecar(filepath.Dir(c.path), archiveDir, id); err != nil {
		return fmt.Errorf("archiving sidecar for %s: %w", id, err)
	}

	event.Emit(root, "work.archive", map[string]string{
		"item_id":     c.item.ID,
		"from_status": "done",
		"to_status":   "archived",
		"actor":       actorFor(c.item),
	})

	return nil
}

// checkArchiveGuards runs every guard that can refuse an archive and returns
// the first refusal. It exists so the three archive paths — targeted, preview,
// sweep — cannot disagree about which guards apply: a guard wired into two of
// the three is a guard with a documented bypass.
//
// The blocked-on tag is checked BEFORE the design successor. Either refusal is
// correct when both fire, so the order is a choice about which one an operator
// should hear first, and "a named person still owes something on this item" is
// the fact no other archive-time query surfaces. It is also the cheaper check —
// it reads no store state and gates on no type.
func checkArchiveGuards(root string, item *Item) error {
	if err := requireNotBlocked(item); err != nil {
		return err
	}
	return requireSuccessor(root, item)
}

// SkippedItem is one done item a guard refused, carried with the refusal that
// stopped it. The sweep reports these, and reporting a bare list of ids would
// leave the operator to guess which guard fired on which item — the two live
// guards have different remedies (remove a tag vs. file a tracker), so the
// reason travels with the item rather than being reconstructed downstream.
type SkippedItem struct {
	Item   *Item
	Reason error
}

// splitBlocked partitions candidates into those the archive guards permit and
// those they block (see checkArchiveGuards). The sweep SKIPS blocked items
// rather than failing outright — one guarded item must not stop the cleanup of
// everything else — but it must not archive them either, or
// `mg archive --days=0` becomes a bulk bypass of the targeted form's refusal
// and the guards are decorative.
func splitBlocked(root string, cands []archiveCandidate) (ok []archiveCandidate, blocked []SkippedItem, err error) {
	for _, c := range cands {
		if blockErr := checkArchiveGuards(root, c.item); blockErr != nil {
			blocked = append(blocked, SkippedItem{Item: c.item, Reason: blockErr})
			continue
		}
		ok = append(ok, c)
	}
	return ok, blocked, nil
}

// Archive moves done items older than maxAge to the archive directory,
// partitioned by year-month (e.g., archive/2026-03/).
//
// It returns the items it archived AND the done items a guard refused, each
// with the refusal. Skipped items stay in done/, where they remain visible;
// callers are expected to report them, reason included.
//
// This is the age sweep: it is otherwise unfiltered over done/ and archives
// every item past the threshold. maxAge=0 therefore archives ALL eligible done
// items. Callers wanting to act on one named item must use ArchiveItem, which
// shares none of this function's selection logic.
func Archive(root string, maxAge time.Duration) (archived []*Item, skipped []SkippedItem, err error) {
	cands, err := archiveCandidates(root, maxAge)
	if err != nil {
		return nil, nil, err
	}

	movable, blocked, err := splitBlocked(root, cands)
	if err != nil {
		return nil, nil, err
	}

	for _, c := range movable {
		if err := archiveFile(root, c); err != nil {
			return nil, nil, err
		}
		archived = append(archived, c.item)
	}

	return archived, blocked, nil
}

// ArchiveDryRun returns the items Archive(root, maxAge) would move and the ones
// it would skip, without moving anything. The sweep is an unfiltered mass
// mutation, so it is worth being able to look before leaping — and the preview
// applies the same guards as the mutation, because a dry run that promises to
// archive something the real run will refuse is a preview that has drifted.
func ArchiveDryRun(root string, maxAge time.Duration) (would []*Item, skipped []SkippedItem, err error) {
	cands, err := archiveCandidates(root, maxAge)
	if err != nil {
		return nil, nil, err
	}

	movable, blocked, err := splitBlocked(root, cands)
	if err != nil {
		return nil, nil, err
	}

	for _, c := range movable {
		would = append(would, c.item)
	}
	return would, blocked, nil
}

// ArchiveOpts carries the caller's answers to the archive guards.
type ArchiveOpts struct {
	// Successor is the id of the item that tracks what a design recommends. It
	// is recorded as a successor: tag on the design BEFORE the move, so the
	// archived record names its own tracker. It answers the successor guard and
	// NOTHING ELSE — it is not a general archive override.
	Successor string
	// Force archives an item a guard refused: a design that nothing tracks
	// (legitimate when the design was abandoned rather than implemented), or an
	// item still tagged blocked-on-* (legitimate when the obligation was
	// discharged out of band and nobody removed the tag).
	//
	// It applies to the blocked-on guard for the same reason it applies to the
	// successor guard: without a recorded escape hatch, an operator who knows
	// the block is stale strips the tag by hand to get past the refusal, which
	// is the same bypass with none of the audit trail. It is never named by the
	// refusal it bypasses (see errNoSuccessor, errBlockedOn) and its use is
	// recorded as a work.archive_forced event naming the guard, so "rule
	// forced" does not look like "rule satisfied" to anyone reading the archive
	// later.
	Force bool
}

// CheckArchivable resolves the named item and reports whether archiving it is
// permitted, WITHOUT moving anything or writing anything. It is what --dry-run
// consults, so the preview cannot promise an archive the real run will refuse.
func CheckArchivable(root, id string, opts ArchiveOpts) (*Item, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}
	if status != "done" {
		return nil, explainArchiveFailure(root, id)
	}

	item, err := readFile(path)
	if err != nil {
		return nil, err
	}

	// A --successor the caller intends to pass is honoured by the preview as
	// if it had been written, but is still validated: a dry run that reports
	// "would archive" for a successor id that does not exist is a false pass
	// one step removed.
	if opts.Successor != "" {
		if err := checkSuccessorTarget(root, item, opts.Successor); err != nil {
			return nil, err
		}
		// --successor answers the successor guard and no other. Returning here
		// on the strength of it would make it a bypass for a guard it never
		// addressed — naming a tracker says nothing about whether a person
		// still owes something on this item.
		if err := requireNotBlocked(item); err != nil && !opts.Force {
			return nil, err
		}
		return item, nil
	}

	if err := checkArchiveGuards(root, item); err != nil && !opts.Force {
		return nil, err
	}
	return item, nil
}

// ArchiveItem archives exactly the one done item named by id, and returns it.
//
// It is the targeted counterpart to Archive and deliberately shares none of
// that function's selection logic: it resolves a single path by ID and moves
// that path or nothing. There is no age threshold, no scan of done/, and no
// fallback to sweep semantics on a bad or ambiguous id — an id that does not
// resolve to a done item is an error, never a broader archive. Widening this
// to touch more than the named item would recreate the defect it was written
// to fix (mg-322f), in which routine cleanup silently archived items that
// callers were relying on to still be there.
//
// A done `type: design` item is refused unless something tracks what it
// recommends — see successor.go for why that check is structural rather than
// textual, and why its failure message does not mention opts.Force. An item of
// ANY type carrying a blocked-on-* tag is refused too — see blockedon.go.
func ArchiveItem(root, id string, opts ArchiveOpts) (*Item, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}
	if status != "done" {
		return nil, explainArchiveFailure(root, id)
	}

	item, err := readFile(path)
	if err != nil {
		return nil, err
	}

	// Record the link first, so the guard below reads the same store any later
	// reader will: the successor: tag is part of the archived record, not a
	// fact that lived only in this process's argv.
	if opts.Successor != "" {
		if err := linkSuccessor(root, path, item, opts.Successor); err != nil {
			return nil, err
		}
	}

	if guardErr := checkArchiveGuards(root, item); guardErr != nil {
		if !opts.Force {
			return nil, guardErr
		}
		event.Emit(root, "work.archive_forced", map[string]string{
			"item_id": item.ID,
			"reason":  mgerrCode(guardErr),
			"actor":   actorFor(item),
		})
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not be archived: %s", id, fsErrText(err)))
	}

	if err := archiveFile(root, archiveCandidate{path: path, item: item, doneTime: info.ModTime()}); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not be archived: %s", id, fsErrText(err)))
	}

	return item, nil
}

// ListArchived returns all work items in the archive directory
// (across all date partitions).
func ListArchived(root string) ([]*Item, error) {
	archiveRoot := filepath.Join(root, "work", "archive")

	// Archive may not exist yet
	if _, err := os.Stat(archiveRoot); os.IsNotExist(err) {
		return nil, nil
	}

	partitions, err := os.ReadDir(archiveRoot)
	if err != nil {
		return nil, fmt.Errorf("reading archive/: %w", err)
	}

	var items []*Item
	for _, p := range partitions {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(archiveRoot, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			item, err := readFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			items = append(items, item)
		}
	}

	return items, nil
}

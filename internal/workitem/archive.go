package workitem

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
)

// archiveCandidate is one done item eligible for archiving, carried with the
// facts the move needs so that scanning and moving stay separable (see
// archiveCandidates / ArchiveDryRun).
type archiveCandidate struct {
	m        Match
	item     *Item
	doneTime time.Time
}

// archiveCandidates lists done/ and returns the items older than maxAge. It is
// pure: it reads the store and moves nothing. Archive and ArchiveDryRun share
// it so that a preview cannot drift from the mutation it previews.
func archiveCandidates(root string, maxAge time.Duration) ([]archiveCandidate, error) {
	docs, err := listDocs(root, store.Done)
	if err != nil {
		return nil, fmt.Errorf("reading done/: %w", err)
	}

	now := time.Now()
	var cands []archiveCandidate

	for i := range docs {
		m := matchOf(docs[i])
		item, err := readMatch(m)
		if err != nil {
			continue // skip malformed files
		}

		// The stored record's modification time is the "done" time.
		doneTime := docs[i].Mtime

		if now.Sub(doneTime) < maxAge {
			continue
		}

		cands = append(cands, archiveCandidate{m: m, item: item, doneTime: doneTime})
	}

	return cands, nil
}

// archiveFile moves a single done item — its record and any result sidecar —
// into the archive partition for doneTime (e.g. archive/2026-03/), and emits
// the archive event. It handles exactly one item and has no notion of a sweep;
// both Archive and ArchiveItem route their moves through it so the two forms
// cannot drift apart on partitioning, sidecar handling, or eventing.
func archiveFile(root string, c archiveCandidate) error {
	id := c.m.ID
	b, err := be(root)
	if err != nil {
		return err
	}

	// Determine archive partition (YYYY-MM based on done time)
	partition := c.doneTime.Format("2006-01")
	archiveDir := ""
	if l, ok := localOf(b); ok {
		archiveDir = filepath.Join(l.StatusDir(store.Archived), partition)
	}

	// Backend.Archive refuses, rather than replaces, a legacy same-id twin
	// already in this partition (mg-1096), and carries the result sidecar.
	if err := b.Archive(id, partition, writer(0)); err != nil {
		if errors.Is(err, store.ErrExists) {
			dst := filepath.Join("archived", partition, id)
			if archiveDir != "" {
				dst = filepath.Join(archiveDir, filepath.Base(c.m.Path))
			}
			return errDestinationExists(id, c.m.Path, dst)
		}
		return fmt.Errorf("archiving %s: %w", id, err)
	}

	// Saved prior bodies travel too, into archive/<partition>/.bodybak/<id>/.
	// Archiving must not orphan them: leaving them in work/.bodybak/ would
	// accumulate a directory per item forever with nothing to reap it, and
	// deleting them would destroy the only copy of a body at the exact moment
	// nobody is watching. Moving them keeps the archive self-contained, keeps
	// `mg restore-body` working on an archived item, and gives Unarchive
	// something to bring back. See bodyBackupDirFor. (Companion files: only on
	// a local backend.)
	if archiveDir != "" {
		if err := moveBodyBackups(root, liveBodyBackupParent(root), filepath.Join(archiveDir, bodyBackupDirName), id); err != nil {
			return fmt.Errorf("archiving saved bodies for %s: %w", id, err)
		}
	}

	event.Emit(root, "work.archive", map[string]string{
		"item_id":     c.item.ID,
		"from_status": "done",
		"to_status":   "archived",
		"actor":       actor(),
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
//
// The declared-remainder check (remainder.go) sits between them as a BACKSTOP,
// not the primary: `mg done` refuses first, loudly, to the agent standing
// there, so anything reaching archive still declaring an undischarged remainder
// got here by a route that predates the guard or by a successor deleted
// afterwards. It is checked ahead of requireSuccessor because a declaration
// beats an inference — when both fire, the operator should hear the one the
// item states about itself.
func checkArchiveGuards(root string, item *Item) error {
	if err := requireNotBlocked(item); err != nil {
		return err
	}
	if err := requireRemainderDischarged(root, item); err != nil {
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
		// The sweep is the last moment an item is ever written, so it is the
		// last chance to close its reverse link — and the only route by which a
		// link filed BEFORE reciprocity existed becomes walkable from both ends.
		// `mg done` reconciles too, so for anything completed since this shipped
		// the call is a read that finds the tag already there and writes nothing.
		//
		// This is a side effect on OTHER items, never on which items this sweep
		// moves, so ArchiveDryRun stays an honest preview of the archive without
		// carrying it: the drift the dry run is written to avoid is a promise to
		// archive something the real run refuses, and nothing here refuses.
		reconcileBacklinks(root, c.item)

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
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "done" {
		return nil, explainArchiveFailure(root, id)
	}

	item, err := readMatch(m)
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
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "done" {
		return nil, explainArchiveFailure(root, id)
	}

	item, err := readMatch(m)
	if err != nil {
		return nil, err
	}

	// Record the link first, so the guard below reads the same store any later
	// reader will: the successor: tag is part of the archived record, not a
	// fact that lived only in this process's argv.
	if opts.Successor != "" {
		if err := linkSuccessorBothWays(root, m, item, opts.Successor); err != nil {
			return nil, err
		}
	} else {
		// Same reason as in Done: a successor: tag recorded by any other route
		// gets its reverse half here, so the chain is walkable from the archived
		// record and from the item that inherited it alike.
		reconcileBacklinks(root, item)
	}

	if guardErr := checkArchiveGuards(root, item); guardErr != nil {
		if !opts.Force {
			return nil, guardErr
		}
		event.Emit(root, "work.archive_forced", map[string]string{
			"item_id": item.ID,
			"reason":  mgerrCode(guardErr),
			"actor":   actor(),
		})
	}

	// Re-read for the done time: a successor link written above is a write,
	// and the record's modification time is what partitions it.
	m, err = matchIn(root, m.ID, store.Done)
	if err != nil {
		return nil, explainArchiveFailure(root, id)
	}

	if err := archiveFile(root, archiveCandidate{m: m, item: item, doneTime: m.doc.Mtime}); err != nil {
		var me *mgerr.Error
		if errors.As(err, &me) {
			return nil, me
		}
		return nil, ioErr(fmt.Sprintf("%s: could not be archived: %s", id, fsErrText(err)))
	}

	return item, nil
}

// ListArchived returns all work items in the archive directory
// (across all date partitions).
func ListArchived(root string) ([]*Item, error) {
	docs, err := listDocs(root, store.Archived)
	if err != nil {
		return nil, fmt.Errorf("reading archive/: %w", err)
	}

	var items []*Item
	for i := range docs {
		// Partitioned records only, as this has always listed: a loose file
		// directly under archive/ is visible to Resolve (so it cannot be
		// minted over) but is not a listed archive entry.
		if docs[i].Partition == "" {
			continue
		}
		item, err := itemOf(&docs[i])
		if err != nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

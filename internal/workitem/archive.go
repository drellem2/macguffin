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

// splitBlocked partitions candidates into those the design-successor guard
// permits and those it blocks (see successor.go). The sweep SKIPS blocked
// items rather than failing outright — one untracked design must not stop the
// cleanup of everything else — but it must not archive them either, or
// `mg archive --days=0` becomes a bulk bypass of the targeted form's refusal
// and the guard is decorative.
func splitBlocked(root string, cands []archiveCandidate) (ok, blocked []archiveCandidate, err error) {
	for _, c := range cands {
		blockErr := requireSuccessor(root, c.item)
		if blockErr != nil {
			blocked = append(blocked, c)
			continue
		}
		ok = append(ok, c)
	}
	return ok, blocked, nil
}

// Archive moves done items older than maxAge to the archive directory,
// partitioned by year-month (e.g., archive/2026-03/).
//
// It returns the items it archived AND the done design items it refused to
// archive because nothing tracks what they recommend. Skipped items stay in
// done/, where they remain visible; callers are expected to report them.
//
// This is the age sweep: it is otherwise unfiltered over done/ and archives
// every item past the threshold. maxAge=0 therefore archives ALL eligible done
// items. Callers wanting to act on one named item must use ArchiveItem, which
// shares none of this function's selection logic.
func Archive(root string, maxAge time.Duration) (archived, skipped []*Item, err error) {
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
	for _, c := range blocked {
		skipped = append(skipped, c.item)
	}

	return archived, skipped, nil
}

// ArchiveDryRun returns the items Archive(root, maxAge) would move and the ones
// it would skip, without moving anything. The sweep is an unfiltered mass
// mutation, so it is worth being able to look before leaping — and the preview
// applies the same guard as the mutation, because a dry run that promises to
// archive something the real run will refuse is a preview that has drifted.
func ArchiveDryRun(root string, maxAge time.Duration) (would, skipped []*Item, err error) {
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
	for _, c := range blocked {
		skipped = append(skipped, c.item)
	}
	return would, skipped, nil
}

// ArchiveOpts carries the caller's answer to the design-successor guard.
type ArchiveOpts struct {
	// Successor is the id of the item that tracks what a design recommends. It
	// is recorded as a successor: tag on the design BEFORE the move, so the
	// archived record names its own tracker.
	Successor string
	// Force archives a design that nothing tracks anyway — legitimate when the
	// design was abandoned rather than implemented. It is never named by the
	// refusal it bypasses (see errNoSuccessor) and its use is recorded as a
	// work.archive_forced event, so "rule forced" does not look like "rule
	// satisfied" to anyone reading the archive later.
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
		return item, nil
	}

	if err := requireSuccessor(root, item); err != nil && !opts.Force {
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
// textual, and why its failure message does not mention opts.Force.
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

	if guardErr := requireSuccessor(root, item); guardErr != nil {
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

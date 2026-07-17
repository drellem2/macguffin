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

// Archive moves done items older than maxAge to the archive directory,
// partitioned by year-month (e.g., archive/2026-03/).
// It returns the list of archived items.
//
// This is the age sweep: it is unfiltered over done/ and archives every item
// past the threshold. maxAge=0 therefore archives ALL done items. Callers
// wanting to act on one named item must use ArchiveItem, which shares none of
// this function's selection logic.
func Archive(root string, maxAge time.Duration) ([]*Item, error) {
	cands, err := archiveCandidates(root, maxAge)
	if err != nil {
		return nil, err
	}

	var archived []*Item
	for _, c := range cands {
		if err := archiveFile(root, c); err != nil {
			return nil, err
		}
		archived = append(archived, c.item)
	}

	return archived, nil
}

// ArchiveDryRun returns the items Archive(root, maxAge) would move, without
// moving anything. The sweep is an unfiltered mass mutation, so it is worth
// being able to look before leaping.
func ArchiveDryRun(root string, maxAge time.Duration) ([]*Item, error) {
	cands, err := archiveCandidates(root, maxAge)
	if err != nil {
		return nil, err
	}

	var items []*Item
	for _, c := range cands {
		items = append(items, c.item)
	}
	return items, nil
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
func ArchiveItem(root, id string) (*Item, error) {
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

package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Archive moves done items older than maxAge to the archive directory,
// partitioned by year-month (e.g., archive/2026-03/).
// It returns the list of archived items.
func Archive(root string, maxAge time.Duration) ([]*Item, error) {
	doneDir := filepath.Join(root, "work", "done")
	entries, err := os.ReadDir(doneDir)
	if err != nil {
		return nil, fmt.Errorf("reading done/: %w", err)
	}

	now := time.Now()
	var archived []*Item

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

		// Determine archive partition (YYYY-MM based on done time)
		partition := doneTime.Format("2006-01")
		archiveDir := filepath.Join(root, "work", "archive", partition)
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating archive dir %s: %w", archiveDir, err)
		}

		// Move the .md file
		dstPath := filepath.Join(archiveDir, e.Name())
		if err := os.Rename(path, dstPath); err != nil {
			return nil, fmt.Errorf("archiving %s: %w", item.ID, err)
		}

		// Move result sidecar if it exists
		id := strings.TrimSuffix(e.Name(), ".md")
		sidecarSrc := filepath.Join(doneDir, id+".result.json")
		if _, err := os.Stat(sidecarSrc); err == nil {
			sidecarDst := filepath.Join(archiveDir, id+".result.json")
			if err := os.Rename(sidecarSrc, sidecarDst); err != nil {
				return nil, fmt.Errorf("archiving sidecar for %s: %w", id, err)
			}
		}

		archived = append(archived, item)
	}

	return archived, nil
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

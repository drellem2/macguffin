package workitem

import (
	"os"

	"github.com/drellem2/macguffin/store/fsstore"
)

// Path-level helpers this package's tests were written against. They used to
// be production code; production now reaches storage only through
// store.Backend (backend.go), so they live here, test-only, unchanged in
// behaviour, for the tests that inspect the filesystem layout directly.

// readFile parses the work item at path.
func readFile(path string) (*Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	item, err := Parse(string(data))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil {
		item.Mtime = info.ModTime()
	}
	return item, nil
}

// moveResultSidecar moves <id>.result.json from srcDir to dstDir when it
// exists — the filesystem backend's own sidecar move.
func moveResultSidecar(srcDir, dstDir, id string) error {
	return fsstore.MoveSidecar(srcDir, dstDir, id)
}

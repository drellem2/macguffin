package workitem

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// --- the driver's pulse ---------------------------------------------------
//
// A COMPANION file: it lives beside the store on a local backend
// (store.Local) and nowhere on any other. This is one of the few files in the
// package allowed raw file I/O (see backendguard_test.go), and only on a path
// the backend's WorkDir names.

// sweepStampName is the file `mg schedule` stamps on every run. It lives in
// work/ beside the directories it reports on, is plain RFC3339 text, and is
// therefore answerable with `cat` — Design Principle 1 applies to the driver's
// own liveness as much as to an item's status.
const sweepStampName = ".last-sweep"

// errNoLocalStamp is a backend with no companion files: there is no stamp.
var errNoLocalStamp = errors.New("this storage backend keeps no sweep stamp")

// sweepStampPath is the stamp's path, or "" on a backend that is not local.
func sweepStampPath(root string) string {
	b, err := backendFor(root)
	if err != nil {
		return ""
	}
	l, ok := localOf(b)
	if !ok {
		return ""
	}
	return filepath.Join(l.WorkDir(), sweepStampName)
}

// RecordSweep stamps the moment the sweep ran. It is called ONLY by the
// `mg schedule` command, never by Done's internal sweep: the question this
// stamp answers is "is something driving the sweep on a clock", and a sweep
// that happened because somebody finished a ticket is not an answer to it.
//
// On a backend with no companion files it records nothing, and CheckDriver
// reports the driver as never seen.
func RecordSweep(root string) error {
	path := sweepStampPath(root)
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(snoozeNow().Format(time.RFC3339)+"\n"), 0o644)
}

// readSweepStamp reads the stamp RecordSweep wrote.
func readSweepStamp(root string) ([]byte, error) {
	path := sweepStampPath(root)
	if path == "" {
		return nil, errNoLocalStamp
	}
	return os.ReadFile(path)
}

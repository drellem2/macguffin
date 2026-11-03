package fsstore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/store"
)

// EventsFile is the audit log's file name under the workspace root.
const EventsFile = "events.jsonl"

// eventLog is <root>/events.jsonl.
type eventLog struct{ path string }

// EventLog returns the filesystem event log for root.
func EventLog(root string) store.EventLog {
	return eventLog{path: filepath.Join(root, EventsFile)}
}

// AppendEvent appends with O_APPEND, so concurrent writers' lines interleave
// whole rather than overwrite each other.
func (l eventLog) AppendEvent(line []byte) error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", l.path, err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("writing event: %w", err)
	}
	return nil
}

func (l eventLog) ReadEvents() ([]byte, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", l.path, err)
	}
	return data, nil
}

func (l eventLog) StatEvents() (store.EventsInfo, error) {
	info := store.EventsInfo{Where: l.path}
	fi, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return info, nil
		}
		return info, err
	}
	info.Exists = true
	info.Size = fi.Size()
	info.ModTime = fi.ModTime()
	return info, nil
}

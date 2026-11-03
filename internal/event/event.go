package event

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
)

const eventsFile = fsstore.EventsFile

// The event log is reached through the storage backend's hook
// (store.EventLog), not by opening events.jsonl here: a backend whose events
// live somewhere other than a local file (a shared store's own history) plugs
// in with SetLogOpener, and every Append/List/Read/Stat in mg follows it. The
// default is the filesystem log, <root>/events.jsonl, exactly as before.
var (
	logMu     sync.RWMutex
	logOpener func(root string) store.EventLog
)

// SetLogOpener installs the backend hook that maps a workspace root to its
// event log. nil restores the filesystem default. mgcli.Main calls it once,
// before any command runs.
func SetLogOpener(open func(root string) store.EventLog) {
	logMu.Lock()
	logOpener = open
	logMu.Unlock()
}

// Log returns the event log for root through the installed hook.
func Log(root string) store.EventLog {
	logMu.RLock()
	open := logOpener
	logMu.RUnlock()
	if open != nil {
		return open(root)
	}
	return fsstore.EventLog(root)
}

// Read returns the raw log for root (one JSON object per line), or nil when
// there is none yet.
func Read(root string) ([]byte, error) {
	return Log(root).ReadEvents()
}

// Stat describes root's event log without reading it.
func Stat(root string) (store.EventsInfo, error) {
	return Log(root).StatEvents()
}

// LogPath returns the path to the events log file under root.
func LogPath(root string) string {
	return filepath.Join(root, eventsFile)
}

// Entry represents a single event log entry.
type Entry struct {
	Ts   string `json:"ts"`
	Type string `json:"type"`
	// Extra holds arbitrary key-value pairs from --key=value flags.
	Extra map[string]string `json:"-"`
}

// MarshalJSON produces a flat JSON object with ts, type, and all extra fields.
func (e Entry) MarshalJSON() ([]byte, error) {
	m := make(map[string]string, len(e.Extra)+2)
	m["ts"] = e.Ts
	m["type"] = e.Type
	for k, v := range e.Extra {
		if k == "ts" || k == "type" {
			continue // reserved
		}
		m[k] = v
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads a flat JSON object back into an Entry.
func (e *Entry) UnmarshalJSON(data []byte) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	e.Ts = m["ts"]
	e.Type = m["type"]
	delete(m, "ts")
	delete(m, "type")
	e.Extra = m
	return nil
}

// Emit is a best-effort wrapper around Append. If the write fails, the error
// is logged to stderr but not returned to the caller. State transitions must
// never fail because the event log is unavailable.
func Emit(root string, eventType string, kvs map[string]string) {
	if _, err := Append(root, eventType, kvs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: emit %s event: %v\n", eventType, err)
	}
}

// Append writes a new event entry to <root>/events.jsonl.
func Append(root string, eventType string, kvs map[string]string) (Entry, error) {
	entry := Entry{
		Ts:    time.Now().UTC().Format(time.RFC3339),
		Type:  eventType,
		Extra: kvs,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return Entry{}, fmt.Errorf("marshalling event: %w", err)
	}

	data = append(data, '\n')
	if err := Log(root).AppendEvent(data); err != nil {
		return Entry{}, err
	}

	return entry, nil
}

// ListOpts controls filtering for List.
type ListOpts struct {
	Type  string // filter by event type (exact match)
	Since string // filter events at or after this RFC3339 timestamp
	Tail  int    // return only the last N entries (0 = all)
}

// List reads events from <root>/events.jsonl with optional filtering.
func List(root string, opts ListOpts) ([]Entry, error) {
	data, err := Read(root)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip malformed lines
		}

		if opts.Type != "" && e.Type != opts.Type {
			continue
		}
		if opts.Since != "" && e.Ts < opts.Since {
			continue
		}
		entries = append(entries, e)
	}

	if opts.Tail > 0 && len(entries) > opts.Tail {
		entries = entries[len(entries)-opts.Tail:]
	}

	return entries, nil
}

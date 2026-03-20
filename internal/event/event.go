package event

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const eventsFile = "events.jsonl"

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

	p := filepath.Join(root, eventsFile)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, fmt.Errorf("opening %s: %w", p, err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return Entry{}, fmt.Errorf("writing event: %w", err)
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
	p := filepath.Join(root, eventsFile)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", p, err)
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

package spend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is the on-disk shape of one NDJSON line in the spend store.
// Field names are part of the public store contract — `mg spend --json` and
// future PM consumers parse these.
type Record struct {
	Ts          time.Time `json:"ts"`
	Agent       string    `json:"agent"`
	Model       string    `json:"model"`
	Input       int       `json:"input"`
	CacheRead   int       `json:"cache_read"`
	CacheCreate int       `json:"cache_create"`
	Output      int       `json:"output"`
	Session     string    `json:"session"`
	MessageUUID string    `json:"message_uuid"`
	// ItemID is set on records read from by-agent/<name>.jsonl with no
	// claimed item at message time (overhead bucket — always empty there)
	// and on records read from by-item/<id>.jsonl (always set to <id>).
	// Not serialised — the file path is the source of truth.
	ItemID string `json:"-"`
}

// StoreRoot returns the spend directory under a macguffin root.
func StoreRoot(root string) string {
	return filepath.Join(root, "spend")
}

// ItemDir returns the per-item bucket directory.
func ItemDir(root string) string {
	return filepath.Join(StoreRoot(root), "by-item")
}

// AgentDir returns the per-agent overhead bucket directory.
func AgentDir(root string) string {
	return filepath.Join(StoreRoot(root), "by-agent")
}

// EnsureStore creates the spend directory tree if it is missing.
func EnsureStore(root string) error {
	for _, d := range []string{ItemDir(root), AgentDir(root)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	return nil
}

// itemPath returns the NDJSON file for a given mg-id.
func itemPath(root, itemID string) string {
	return filepath.Join(ItemDir(root), itemID+".jsonl")
}

// agentPath returns the NDJSON file for a given agent name.
func agentPath(root, agent string) string {
	return filepath.Join(AgentDir(root), agent+".jsonl")
}

// AppendItem writes records to by-item/<id>.jsonl (creates the file if absent).
func AppendItem(root, itemID string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	if err := EnsureStore(root); err != nil {
		return err
	}
	return appendRecords(itemPath(root, itemID), recs)
}

// AppendAgent writes records to by-agent/<name>.jsonl.
func AppendAgent(root, agent string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	if err := EnsureStore(root); err != nil {
		return err
	}
	return appendRecords(agentPath(root, agent), recs)
}

func appendRecords(path string, recs []Record) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, r := range recs {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("marshalling record: %w", err)
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	return w.Flush()
}

// ReadItem returns all records currently stored for an mg-id. Missing file
// returns (nil, nil) — an unscanned item is not an error.
func ReadItem(root, itemID string) ([]Record, error) {
	return readRecords(itemPath(root, itemID), itemID, "")
}

// ReadAgent returns all records currently stored in an agent's overhead bucket.
func ReadAgent(root, agent string) ([]Record, error) {
	return readRecords(agentPath(root, agent), "", agent)
}

func readRecords(path, itemID, agent string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var recs []Record
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		r.ItemID = itemID
		if agent != "" && r.Agent == "" {
			r.Agent = agent
		}
		recs = append(recs, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}
	return recs, nil
}

// ListItems returns the set of mg-ids that have a spend file on disk.
func ListItems(root string) ([]string, error) {
	return listIDs(ItemDir(root))
}

// ListAgents returns the set of agents that have a spend file on disk.
func ListAgents(root string) ([]string, error) {
	return listIDs(AgentDir(root))
}

func listIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
	}
	return ids, nil
}

// ReadAll returns every record across by-item/ and by-agent/. Used by query
// callers that need to walk the entire store (`mg spend --by tag`).
func ReadAll(root string) ([]Record, error) {
	var all []Record

	items, err := ListItems(root)
	if err != nil {
		return nil, err
	}
	for _, id := range items {
		recs, err := ReadItem(root, id)
		if err != nil {
			return nil, err
		}
		all = append(all, recs...)
	}

	agents, err := ListAgents(root)
	if err != nil {
		return nil, err
	}
	for _, ag := range agents {
		recs, err := ReadAgent(root, ag)
		if err != nil {
			return nil, err
		}
		all = append(all, recs...)
	}
	return all, nil
}

// Key uniquely identifies a stored message for dedup purposes. The aggregator
// keys on (session, message-uuid) so re-running over the same transcripts
// yields the same store (idempotent — see design §8 "polecat ID reuse").
type Key struct {
	Session     string
	MessageUUID string
}

// SeenSet builds the set of (session, message_uuid) pairs already in the
// store. Used by the aggregator to skip already-stored entries on rerun.
func SeenSet(root string) (map[Key]struct{}, error) {
	all, err := ReadAll(root)
	if err != nil {
		return nil, err
	}
	seen := make(map[Key]struct{}, len(all))
	for _, r := range all {
		seen[Key{Session: r.Session, MessageUUID: r.MessageUUID}] = struct{}{}
	}
	return seen, nil
}

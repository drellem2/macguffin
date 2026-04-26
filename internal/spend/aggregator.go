package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// Run is one aggregator invocation. Configure it, call Run.Aggregate, and read
// Result. Defaults are picked up when fields are zero/empty.
type Run struct {
	// Root is the macguffin root (~/.macguffin). When empty, EnsureStore
	// resolves the default.
	Root string

	// ProjectsDir is the Claude Code transcripts root
	// (~/.claude/projects). When empty, DefaultProjectsDir is used.
	ProjectsDir string

	// Warn receives messages about unknown transcript shapes (per design
	// §8 — "skip-and-log unknown shapes"). Defaults to writing to stderr.
	Warn func(string)
}

// Result reports what an aggregator pass did.
type Result struct {
	TranscriptsScanned int
	MessagesScanned    int
	NewItemRecords     int
	NewAgentRecords    int
	SkippedDup         int
}

// claimInterval represents the period during which an actor had an item
// claimed. ReleaseTs is set to the far future for an item still claimed at
// scan time.
type claimInterval struct {
	Actor     string
	ItemID    string
	ClaimTs   time.Time
	ReleaseTs time.Time
}

// farFuture sentinel for "still claimed". Picked well past any plausible
// transcript timestamp so attribution falls through correctly.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// buildClaimIntervals reads events.jsonl and builds per-actor claim windows.
// Pairs each `work.claim` with the next `work.done`, `work.unclaim`, or
// `work.shelve` for the same (actor, item_id). Unclosed claims extend to
// farFuture so a still-running polecat's messages still attribute.
func buildClaimIntervals(root string) ([]claimInterval, error) {
	entries, err := event.List(root, event.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}

	type openKey struct{ actor, item string }
	open := make(map[openKey]time.Time)
	var intervals []claimInterval

	for _, e := range entries {
		actor := e.Extra["actor"]
		item := e.Extra["item_id"]
		if actor == "" || item == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.Ts)
		if err != nil {
			continue
		}
		ts = ts.UTC()
		k := openKey{actor: actor, item: item}
		switch e.Type {
		case "work.claim":
			open[k] = ts
		case "work.done", "work.unclaim", "work.shelve", "work.archive":
			if claimTs, ok := open[k]; ok {
				intervals = append(intervals, claimInterval{
					Actor:     actor,
					ItemID:    item,
					ClaimTs:   claimTs,
					ReleaseTs: ts,
				})
				delete(open, k)
			}
		}
	}

	for k, claimTs := range open {
		intervals = append(intervals, claimInterval{
			Actor:     k.actor,
			ItemID:    k.item,
			ClaimTs:   claimTs,
			ReleaseTs: farFuture,
		})
	}

	return intervals, nil
}

// attribute returns the mg-id an actor's message at ts maps to, or "" if no
// claim interval matches (overhead bucket).
func attribute(intervals []claimInterval, actor string, ts time.Time) string {
	var best string
	var bestStart time.Time
	for _, iv := range intervals {
		if iv.Actor != actor {
			continue
		}
		if ts.Before(iv.ClaimTs) || ts.After(iv.ReleaseTs) {
			continue
		}
		// On overlap pick the most recent claim — multi-claim races on the
		// same actor mean the latest claim is what they're "really" on.
		if best == "" || iv.ClaimTs.After(bestStart) {
			best = iv.ItemID
			bestStart = iv.ClaimTs
		}
	}
	return best
}

// Aggregate scans transcripts under r.ProjectsDir, joins each assistant
// message with claim intervals from r.Root's events.jsonl, and appends new
// records to the spend store. Re-running with no new transcript data yields
// no writes (idempotent — keys on (session, message_uuid)).
func (r *Run) Aggregate() (Result, error) {
	var res Result

	if r.Warn == nil {
		r.Warn = func(s string) { fmt.Fprintln(os.Stderr, "spend: warn: "+s) }
	}

	projects := r.ProjectsDir
	if projects == "" {
		p, err := DefaultProjectsDir()
		if err != nil {
			return res, err
		}
		projects = p
	}

	if err := EnsureStore(r.Root); err != nil {
		return res, err
	}

	seen, err := SeenSet(r.Root)
	if err != nil {
		return res, err
	}

	intervals, err := buildClaimIntervals(r.Root)
	if err != nil {
		return res, err
	}

	dirs, err := os.ReadDir(projects)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, fmt.Errorf("reading %s: %w", projects, err)
	}

	itemBatch := make(map[string][]Record)
	agentBatch := make(map[string][]Record)

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		agent := AgentFromProjectDir(d.Name())
		if agent == "" {
			continue // not a pogo agent dir
		}

		sessionFiles, err := os.ReadDir(filepath.Join(projects, d.Name()))
		if err != nil {
			r.Warn(fmt.Sprintf("reading %s: %v", d.Name(), err))
			continue
		}
		for _, sf := range sessionFiles {
			if sf.IsDir() {
				continue
			}
			name := sf.Name()
			if filepath.Ext(name) != ".jsonl" {
				continue
			}
			res.TranscriptsScanned++
			path := filepath.Join(projects, d.Name(), name)
			msgs, err := ParseTranscript(path, agent, r.Warn)
			if err != nil {
				r.Warn(fmt.Sprintf("parse %s: %v", path, err))
				continue
			}
			for _, m := range msgs {
				res.MessagesScanned++
				k := Key{Session: m.Session, MessageUUID: m.MessageUUID}
				if _, ok := seen[k]; ok {
					res.SkippedDup++
					continue
				}
				seen[k] = struct{}{}
				rec := Record{
					Ts:          m.Ts,
					Agent:       agent,
					Model:       m.Model,
					Input:       m.Input,
					CacheRead:   m.CacheRead,
					CacheCreate: m.CacheCreate,
					Output:      m.Output,
					Session:     m.Session,
					MessageUUID: m.MessageUUID,
				}
				if itemID := attribute(intervals, agent, m.Ts); itemID != "" {
					rec.ItemID = itemID
					itemBatch[itemID] = append(itemBatch[itemID], rec)
					res.NewItemRecords++
				} else {
					agentBatch[agent] = append(agentBatch[agent], rec)
					res.NewAgentRecords++
				}
			}
		}
	}

	// Write batches in deterministic order. Each per-file batch is
	// further sorted by timestamp so the file stays roughly chronological.
	itemIDs := sortedKeys(itemBatch)
	for _, id := range itemIDs {
		recs := itemBatch[id]
		sort.Slice(recs, func(i, j int) bool { return recs[i].Ts.Before(recs[j].Ts) })
		if err := AppendItem(r.Root, id, recs); err != nil {
			return res, err
		}
	}
	agentNames := sortedKeys(agentBatch)
	for _, ag := range agentNames {
		recs := agentBatch[ag]
		sort.Slice(recs, func(i, j int) bool { return recs[i].Ts.Before(recs[j].Ts) })
		if err := AppendAgent(r.Root, ag, recs); err != nil {
			return res, err
		}
	}

	return res, nil
}

func sortedKeys(m map[string][]Record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

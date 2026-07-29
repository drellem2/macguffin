package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// UpdateField represents which field to update and how.
type UpdateField struct {
	Title      *string  // replace title
	Body       *string  // replace body
	AppendBody *string  // append to the existing body (see appendToBody)
	Type       *string  // replace type
	Repo       *string  // replace repo
	Assignee   *string  // replace assignee
	Priority   *string  // replace priority
	Budget     *int     // replace budget; 0 unsets (nil), positive sets to N
	Depends    []string // replace all dependencies (nil = no change, empty = clear)
	AddDepends []string // append to existing dependencies
	RmDepends  []string // remove from existing dependencies
	Tags       []string // replace all tags (nil = no change, empty = clear)
	AddTags    []string // append to existing tags
	RmTags     []string // remove from existing tags

	// IfUnchanged is an optional precondition: the stored body must hash to
	// this value (a full BodyHash or a prefix of at least minHashPrefix
	// characters) or the whole update is refused before anything is written.
	// Empty means no precondition — the historical, unguarded behaviour.
	IfUnchanged string
}

// BodyChange reports what an update did to the stored body. It exists so the
// CLI can print a size delta on the success line: after mg-f326's incident 1 a
// body went 227 → 113 lines and the writer was told only "Updated", so the loss
// was found seven minutes later by a re-read and a grep. A number on the write
// itself is the cheapest instrument that would have shown it immediately.
type BodyChange struct {
	Changed     bool
	Mode        string // "replace", "append", or "incidental" (a title rewrite)
	HashBefore  string
	HashAfter   string
	LinesBefore int
	LinesAfter  int
}

// Update applies field changes to an existing work item and writes it back.
// If dependency changes cause the item to have unmet deps, it is moved from
// available/ to pending/. Conversely, if all deps are now met, it moves from
// pending/ to available/.
func Update(root, id string, fields UpdateField) (*Item, error) {
	item, _, err := UpdateWithBodyChange(root, id, fields)
	return item, err
}

// UpdateWithBodyChange is Update plus a report of what happened to the body.
// Update is the thin wrapper, so the dozens of callers that do not care about
// the body delta are unaffected.
//
// LOST UPDATES (mg-f326). This is a read-modify-write: it reads the stored
// item, applies fields, and writes the whole file back. That is safe for the
// incremental fields (--add-tags and friends compose against what is on disk at
// write time) and unsafe for Body, which arrives as a complete replacement
// computed by the caller from a read that happened at caller-scale — seconds,
// minutes, or an agent's whole turn earlier. Anything written into that window
// is destroyed with exit 0.
//
// Two defences, in the order they should be reached for:
//
//  1. AppendBody. An append composes against the body on disk at write time, so
//     it cannot destroy a section it never saw. All three of mg-f326's
//     collisions were appends of separate dated sections that had no reason to
//     conflict. This closes the caller-scale window entirely rather than
//     detecting a write into it.
//  2. IfUnchanged. When a full-body replacement genuinely is the right shape,
//     naming the version you read turns a silent clobber into a loud refusal.
//     It is opt-in: a bare Body write behaves exactly as it always has, because
//     mg self-installs across the whole fleet on merge and a write path that
//     starts refusing by default would take out the tooling needed to report
//     that it had.
//
// Neither defence is a recovery story, and mg-9fc8 is the incident that made
// the difference matter: --if-unchanged was passed and SATISFIED while the body
// was destroyed anyway, because the corruption entered at the READ end of the
// read-modify-write (a failed `mg show` wrote its own usage error into the file
// the caller then sent back). A guard on the concurrency end cannot see that.
// So there is a third, unconditional measure that assumes every defence above
// has already failed:
//
//  3. A body backup. Before a replace-mode edit overwrites a body, the prior
//     body is written to work/.bodybak/<id>/ and restorable with
//     `mg restore-body`. It predicts nothing and refuses nothing. See
//     bodybackup.go.
//
// Deliberately NOT a lock. Callers are long-lived agents that can die
// mid-edit, so a lock needs a timeout, and a timeout is the same race with more
// moving parts. The defect is silence, not concurrency.
//
// A residual window remains between this function's own read and its own write
// — microseconds inside one process, against the caller-scale window that
// produced every observed incident. Closing it would require the locking above.
func UpdateWithBodyChange(root, id string, fields UpdateField) (*Item, *BodyChange, error) {
	itemPath, status, err := FindPath(root, id)
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(itemPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading work item: %w", err)
	}

	item, err := Parse(string(data))
	if err != nil {
		return nil, nil, fmt.Errorf("parsing work item: %w", err)
	}

	// The body as stored, captured before any field is applied. This is both
	// the value IfUnchanged is checked against and the "before" side of the
	// reported delta.
	bodyBefore := item.Body

	// The tag set as stored, likewise captured before any field is applied. The
	// workflow-marker guard needs it to tell a workflow tag this write ADDS from
	// one the item already carried — see writeShape in workflowmarker.go.
	tagsBefore := append([]string(nil), item.Tags...)

	// Every non-body field as stored. The body has had a before/after record
	// since mg-f326; the metadata had none at all, so a `--assignee` change —
	// the dispatch gate — left no trace in events.jsonl (mg-3122). See
	// fieldchange.go.
	metaBefore := snapshotMeta(item)

	// The precondition runs FIRST, before a single field is applied and long
	// before the write, so a refusal leaves the stored item byte-identical.
	if fields.IfUnchanged != "" {
		want, err := normalizeHashArg(fields.IfUnchanged)
		if err != nil {
			return nil, nil, mgerr.Usage("invalid_value",
				fmt.Sprintf("--if-unchanged=%q is not a body hash: %v", fields.IfUnchanged, err),
				fmt.Sprintf("Get the current one with 'mg show %s --body-hash'.", id))
		}
		if !bodyHashMatches(want, bodyBefore) {
			return nil, nil, errBodyChanged(id, itemPath, want, bodyBefore)
		}
	}

	// Apply field updates
	if fields.Title != nil {
		oldTitle := item.Title
		item.Title = *fields.Title
		// Update title in body
		if oldTitle != "" && strings.Contains(item.Body, "# "+oldTitle) {
			item.Body = strings.Replace(item.Body, "# "+oldTitle, "# "+item.Title, 1)
		}
	}

	if fields.Body != nil {
		item.Body = *fields.Body
	}

	if fields.AppendBody != nil {
		item.Body = appendToBody(item.Body, *fields.AppendBody)
	}

	if fields.Type != nil {
		item.Type = *fields.Type
	}

	if fields.Repo != nil {
		item.Repo = *fields.Repo
	}

	if fields.Assignee != nil {
		item.Assignee = *fields.Assignee
	}

	if fields.Priority != nil {
		item.Priority = *fields.Priority
	}

	if fields.Budget != nil {
		if *fields.Budget == 0 {
			item.Budget = nil
		} else {
			b := *fields.Budget
			item.Budget = &b
		}
	}

	// Dependencies: full replacement takes precedence over incremental
	if fields.Depends != nil {
		item.Depends = fields.Depends
	} else {
		if len(fields.AddDepends) > 0 {
			item.Depends = addUnique(item.Depends, fields.AddDepends)
		}
		if len(fields.RmDepends) > 0 {
			item.Depends = removeAll(item.Depends, fields.RmDepends)
		}
	}

	// Tags: full replacement takes precedence over incremental
	if fields.Tags != nil {
		item.Tags = fields.Tags
	} else {
		if len(fields.AddTags) > 0 {
			item.Tags = addUnique(item.Tags, fields.AddTags)
		}
		if len(fields.RmTags) > 0 {
			item.Tags = removeAll(item.Tags, fields.RmTags)
		}
	}

	// One fact, one marker (see workflowmarker.go). Checked on the POST-update
	// body and tags, because either half can be edited independently: an
	// `--add-tags=gh-issue` onto an unmarked body, or a --body rewrite that
	// drops the carrier block from an already-tagged item, both reintroduce the
	// divergence that mg-560d nearly shipped. Runs before the write, so a
	// refusal leaves the stored item untouched.
	//
	// The shape is passed in because "what the resulting item looks like" is not
	// enough on its own: a pure append cannot author the body's leading block,
	// so on an item that ALREADY carried the tag it can only inherit a missing
	// carrier, never create one. Refusing there left mg-d878's 41 legacy items
	// with no append-only route to a correction at all.
	tags, err := reconcileWorkflowMarkers(composeBody(item), item.Tags, writeShape{
		appendOnly: fields.AppendBody != nil && fields.Body == nil,
		priorTags:  tagsBefore,
	})
	if err != nil {
		return nil, nil, err
	}
	item.Tags = tags

	// AFTER reconcileWorkflowMarkers, because that step can itself add a tag —
	// and a tag mg wrote on the caller's behalf is exactly as worth recording
	// as one the caller asked for.
	metaChanges := diffMeta(metaBefore, snapshotMeta(item))

	// Measure the body against composeBody, not item.Body: composeBody is what
	// actually lands on disk (it synthesises the "# Title" heading when the
	// supplied body lacks one), so anything measuring item.Body would be
	// reporting on a string that never gets stored.
	change := describeBodyChange(fields, bodyBefore, composeBody(item))

	// Keep the body we are about to destroy. mg-9fc8: a replace-mode edit took
	// a 149-line body to 4 lines of a shell usage error, --if-unchanged was
	// passed AND satisfied (it proves nobody else wrote in the window; it says
	// nothing about whether the caller's own read succeeded), and the audit line
	// recorded both hashes and neither body. The bytes came back only because
	// they happened to still be in a scratchpad — luck, not a property of the
	// tool. See bodybackup.go for why this is a backup and not a guard.
	//
	// BEFORE the write, so a backup that cannot be taken refuses the edit
	// instead of silently proceeding unprotected: a recovery story that quietly
	// stops being true is worse than none, because it is relied upon. The item
	// on disk is byte-identical after this refusal, exactly as it is after an
	// --if-unchanged refusal.
	backupPath := ""
	if change.Changed && change.Mode == "replace" && strings.TrimSpace(bodyBefore) != "" {
		dir := bodyBackupDirFor(root, itemPath, status, item.ID)
		backupPath, err = saveBodyBackup(dir, bodyBefore, time.Now())
		if err != nil {
			return nil, nil, ioErr(fmt.Sprintf(
				"%s: refusing to replace the body — the prior body could not be saved to %s: %s. Nothing was written.",
				id, dir, fsErrText(err)))
		}
	}

	content := Render(item)
	if err := os.WriteFile(itemPath, []byte(content), 0o644); err != nil {
		return nil, nil, ioErr(fmt.Sprintf("%s: could not be saved: %s", id, fsErrText(err)))
	}

	// A durable record that a prior version existed. mg-f326's complaint was
	// not only that a write destroyed 85 lines but that nothing anywhere said
	// so afterwards: `grep -c` returns the same zero for a deliberate deletion
	// and a destroyed one, so once a clobber is known to have happened every
	// genuine absence in the blast radius reads as damage. This line does not
	// recover the bytes, but it does let a later reader tell "the body shrank
	// by 114 lines at 04:05, replacing hash a1b2…" from "that section was never
	// there" — which is the distinction no instrument could previously make.
	//
	// body_backup closes the gap the rest of the line only measured: a reader
	// who finds the shrink here now has the path to the bytes, instead of two
	// hashes proving they are gone. It is empty for the modes that overwrite
	// nothing (append, --title) — an absent field means "nothing was at risk",
	// never "the backup failed", because a failed backup refuses the edit above
	// and never reaches this line.
	//
	// The condition is "the stored item moved", not "the body moved" (mg-3122).
	// A metadata-only edit used to emit NOTHING — `mg edit <id> --assignee=X`
	// printed "Updated" and left events.jsonl byte-identical — which made the
	// assignee, the field `config.IsDispatchGated` reads to decide whether an
	// item is ever dispatched at all, silently mutable by anyone. mode is
	// "metadata" for those, so a reader can tell a write that touched no body
	// from one that overwrote a body with an identical one; body_hash_before
	// and body_hash_after are still emitted and still equal, which is the
	// positive statement that the body was NOT at risk here.
	if change.Changed || len(metaChanges) > 0 {
		mode := change.Mode
		if !change.Changed {
			mode = "metadata"
		}
		extra := map[string]string{
			"item_id":          item.ID,
			"actor":            actor(),
			"mode":             mode,
			"guarded":          strconv.FormatBool(fields.IfUnchanged != ""),
			"body_hash_before": change.HashBefore,
			"body_hash_after":  change.HashAfter,
			"lines_before":     strconv.Itoa(change.LinesBefore),
			"lines_after":      strconv.Itoa(change.LinesAfter),
		}
		if backupPath != "" {
			extra["body_backup"] = backupPath
		}
		// `fields` names what moved so a reader can grep one key instead of
		// diffing the whole object; the per-field pairs carry the values.
		if len(metaChanges) > 0 {
			names := make([]string, 0, len(metaChanges))
			for _, fc := range metaChanges {
				names = append(names, fc.Name)
				extra[fc.Name+"_before"] = fc.Before
				extra[fc.Name+"_after"] = fc.After
			}
			extra["fields"] = strings.Join(names, ",")
		}
		event.Emit(root, "work.edited", extra)
	}

	// After dependency changes, move items between available/ and pending/
	// as needed based on whether deps are met.
	depsChanged := fields.Depends != nil || len(fields.AddDepends) > 0 || len(fields.RmDepends) > 0
	if depsChanged {
		if status == "available" && len(item.Depends) > 0 {
			// Check if any dependency is unmet → move to pending/
			doneIDs, err := doneIDSet(root)
			if err != nil {
				return nil, nil, err
			}
			if !gateOpen(item, doneIDs, snoozeNow()) {
				dst := filepath.Join(root, "work", "pending", filepath.Base(itemPath))
				if err := os.Rename(itemPath, dst); err != nil {
					return nil, nil, ioErr(fmt.Sprintf("%s: saved, but could not be moved to pending: %s", id, fsErrText(err)))
				}
				if err := moveResultSidecar(filepath.Dir(itemPath), filepath.Dir(dst), id); err != nil {
					return nil, nil, ioErr(fmt.Sprintf("%s: moved to pending, but result sidecar could not follow: %s", id, fsErrText(err)))
				}
			}
		} else if status == "pending" {
			// Check if all deps are now met → move to available/
			doneIDs, err := doneIDSet(root)
			if err != nil {
				return nil, nil, err
			}
			// gateOpen, not allDepsMet: clearing the last dependency off a
			// SNOOZED item must not promote it. Dropping a dependency says
			// nothing about a wake time that has not arrived.
			if gateOpen(item, doneIDs, snoozeNow()) {
				dst := filepath.Join(root, "work", "available", filepath.Base(itemPath))
				if err := os.Rename(itemPath, dst); err != nil {
					return nil, nil, ioErr(fmt.Sprintf("%s: saved, but could not be promoted to available: %s", id, fsErrText(err)))
				}
				if err := moveResultSidecar(filepath.Dir(itemPath), filepath.Dir(dst), id); err != nil {
					return nil, nil, ioErr(fmt.Sprintf("%s: promoted to available, but result sidecar could not follow: %s", id, fsErrText(err)))
				}
			}
		}
	}

	return item, change, nil
}

// appendToBody joins text onto the end of body, separated by exactly one blank
// line.
//
// The appended text itself is taken VERBATIM — the same guarantee --body-file
// makes. Only the join is normalized, and it is normalized rather than
// concatenated raw because these bodies are markdown: a "## 2026-07-29 04:20"
// heading placed directly under the previous paragraph's last line is not a
// heading at all under CommonMark, it is more paragraph text. Requiring every
// caller to remember a leading blank line would make the safe path cost more
// keystrokes and more knowledge than the dangerous one, which is the gradient
// --body-file exists to invert.
//
// Trailing newlines on the existing body are collapsed to the single blank-line
// separator, so appending twice yields one blank line between sections, not a
// growing run of them.
func appendToBody(body, text string) string {
	trimmed := strings.TrimRight(body, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return text
	}
	return trimmed + "\n\n" + text
}

// describeBodyChange measures the stored body before and after an update and
// names which flag drove the change. "incidental" covers the case where no body
// flag was passed at all but the body still moved — in practice a --title edit,
// which rewrites the "# heading" line in place.
func describeBodyChange(fields UpdateField, before, after string) *BodyChange {
	mode := "incidental"
	switch {
	case fields.Body != nil:
		mode = "replace"
	case fields.AppendBody != nil:
		mode = "append"
	}
	return &BodyChange{
		Changed:     before != after,
		Mode:        mode,
		HashBefore:  BodyHash(before),
		HashAfter:   BodyHash(after),
		LinesBefore: countBodyLines(before),
		LinesAfter:  countBodyLines(after),
	}
}

// errBodyChanged is the loud refusal --if-unchanged exists to produce.
//
// It reports everything mg can actually observe about the change — the current
// hash, the current size, and when the file was last written — and no more. It
// cannot say WHAT changed, because the version the caller read is not stored
// anywhere mg can reach; claiming otherwise would be the same class of lie as
// the exit 0 this replaces. Naming the size and the timestamp is enough for a
// caller to find the other writer.
//
// Conflict (exit 4), not retryable: re-running the identical command with the
// same now-stale hash can never succeed, and a caller that retried on it would
// spin. The remedy is a re-read, which the hint spells out.
func errBodyChanged(id, itemPath, want, stored string) *mgerr.Error {
	modified := "unknown"
	if info, err := os.Stat(itemPath); err == nil {
		modified = info.ModTime().UTC().Format(time.RFC3339)
	}

	msg := fmt.Sprintf(
		"%s: the body changed since you read it — refusing to overwrite it.\n"+
			"  you passed --if-unchanged=%s\n"+
			"  on disk now %s (%d lines, last written %s)",
		id, want, BodyHash(stored), countBodyLines(stored), modified)

	hint := fmt.Sprintf(
		"Someone else wrote to %s after the read your body is based on. "+
			"Re-read it ('mg show %s --json'), merge your changes into the CURRENT body, "+
			"and retry with the new hash from 'mg show %s --body-hash'. "+
			"If you are adding a section rather than rewriting, use "+
			"'mg edit %s --append-body-file -' instead — an append composes against "+
			"what is on disk and cannot clobber a section it never saw.",
		id, id, id, id)

	return mgerr.Conflict("body_changed", msg, hint)
}

// addUnique appends values to a slice, skipping duplicates.
func addUnique(existing, add []string) []string {
	set := make(map[string]bool, len(existing))
	for _, v := range existing {
		set[v] = true
	}
	result := append([]string{}, existing...)
	for _, v := range add {
		if !set[v] {
			result = append(result, v)
			set[v] = true
		}
	}
	return result
}

// removeAll returns a new slice with specified values removed.
func removeAll(existing, remove []string) []string {
	rm := make(map[string]bool, len(remove))
	for _, v := range remove {
		rm[v] = true
	}
	var result []string
	for _, v := range existing {
		if !rm[v] {
			result = append(result, v)
		}
	}
	return result
}

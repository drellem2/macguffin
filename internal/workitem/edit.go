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

	// IfAssignee is the same precondition one field over, on the dispatch gate.
	// nil means no precondition; a non-nil pointer (including to "") requires
	// the STORED assignee to equal it or the whole update is refused before
	// anything is written.
	//
	// WHY THE GATE NEEDS ITS OWN GUARD (mg-5eee). --if-unchanged covers the
	// body, which is where mg-f326's losses were. It says nothing about the
	// metadata, and the metadata contains the one field that decides whether an
	// item is dispatched at all. On 2026-08-12 the mayor gated mg-27d4 with
	// `blocked:pm-pogo`, pm-pogo set it back to `mayor` a minute later, and the
	// mayor's next four writes to that item each printed `Updated mg-27d4` while
	// the hold the mayor believed in was gone. Nothing lied and nothing was
	// lost: every write did exactly what it was asked, and the audit log records
	// both agents by name. The gap is that a caller holding an item has no way
	// to say "and it is still held" — so a hold survives only as long as nobody
	// else disagrees with it, and the disagreement is silent to the holder.
	//
	// It is a precondition rather than a warning because mg has no baseline of
	// its own: within one invocation there is a read and a write microseconds
	// apart, and no record of what the CALLER last saw. Only the caller knows
	// the value they are relying on, so only the caller can supply it.
	IfAssignee *string
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

	// The title before and after, as READ BACK from the stored body — not as
	// held in memory. The success line used to print the in-memory title, which
	// in the retitle case was the value the write had just destroyed: the CLI
	// asserted a title that was already false at the moment it printed it
	// (mg-bac6). These two make the field's movement reportable instead.
	TitleBefore string
	TitleAfter  string

	// Headings in the stored body beyond the title's own. mg never creates
	// these now, but a caller's body can legitimately carry several H1 sections
	// — and can equally carry a stacked near-duplicate of the title, which is
	// what 196 stored bodies turned out to have. Counted, not refused: the
	// difference between the two is editorial, and mg does not get to rewrite
	// prose to satisfy a number.
	ExtraHeadings int
}

// TitleMoved reports whether the stored title changed.
func (c *BodyChange) TitleMoved() bool { return c != nil && c.TitleBefore != c.TitleAfter }

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
	//
	// asserted is kept for the audit line: it is the ONLY place a caller's own
	// read-state is expressible, and it is what makes body_read_state below
	// mean something. See recordedReadState.
	asserted := ""
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
		asserted = want
	}

	// The same, on the dispatch gate. Also before any field is applied, so a
	// refusal leaves the stored item byte-identical.
	if fields.IfAssignee != nil && item.Assignee != *fields.IfAssignee {
		return nil, nil, errAssigneeChanged(id, itemPath, *fields.IfAssignee, item.Assignee)
	}

	// A gate that does not gate is refused before the write, not stored and
	// reported as success. See assigneegate.go.
	if fields.Assignee != nil {
		if err := ValidateAssignee(*fields.Assignee); err != nil {
			return nil, nil, err
		}
	}

	// The title as stored, i.e. as Parse derived it from the body's first
	// heading. The guard below compares against this.
	titleBefore := item.Title

	// Apply field updates.
	//
	// The title needs no separate body rewrite here. It used to do a
	// strings.Replace of "# "+oldTitle anywhere in the body; the coupling now
	// lives in reconcileTitleHeading (titleheading.go), which rewrites the FIRST
	// heading line positionally, on the way to disk, for every writer. Setting
	// the field is the whole operation.
	if fields.Title != nil {
		item.Title = *fields.Title
	}

	if fields.Body != nil {
		item.Body = *fields.Body
	}

	if fields.AppendBody != nil {
		item.Body = appendToBody(item.Body, *fields.AppendBody)
	}

	// THE SILENT SIDE EFFECT, MADE LOUD (mg-bac6).
	//
	// A body edit whose new first heading says something other than the current
	// title renames the item, because the title is read back from that heading
	// and nowhere else. That is a write to a field the caller did not name, and
	// it exited 0 with a success line for four months — long enough for two
	// agents to each report a different half of it as the bug, and long enough
	// to eat the titles of mg-2ce4 and mg-0418.
	//
	// Refused only when the caller did NOT name a title, so the refusal is
	// precisely "you are about to change something you did not mention". Naming
	// --title makes either outcome available and neither surprising, which is
	// why the remedy in the hint is a field rather than a --force: the safe
	// procedure and the way past the guard are the same procedure.
	//
	// Runs BEFORE the write, so a refusal leaves the stored item byte-identical,
	// exactly like the --if-unchanged and backup refusals above.
	//
	// Bodies with no heading at all are not affected: the heading is synthesised
	// from the title, which preserves it. Nor are appends, which land below the
	// prose and cannot move the first heading — except on an item whose body was
	// empty, where the appended text becomes the whole body, and where being
	// asked to name the title is right.
	if fields.Title == nil {
		if se, isSideEffect := detectTitleSideEffect(titleBefore, item.Body); isSideEffect {
			return nil, nil, errSilentRetitle(id, se)
		}
	}

	// Normalise the in-memory body to the bytes that will actually be stored,
	// so the Item handed back to the caller is the item on disk. composeBody is
	// idempotent — the first heading now says the title, which is case 2 — so
	// the later composeBody calls below are unaffected. Without this, callers
	// (and the CLI's own success line) reason about a body that never existed.
	item.Body = composeBody(item)

	// The other door to the same defect. Rewriting a differing first heading in
	// place cannot STACK a duplicate, but it can land on a title the supplied
	// body already carries further down — and then the rewrite is what authored
	// the duplicate. Refused when this write increases the count, never merely
	// because the count is high, so the bodies already carrying a stacked title
	// stay editable. Before the write, so the item is untouched.
	if now := countTitleHeadings(item.Body, item.Title); now > 1 {
		if was := countTitleHeadings(bodyBefore, item.Title); now > was {
			return nil, nil, errDuplicateTitleHeading(id, item.Title, was, now)
		}
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

	// Placement, not just presence (see carrierplacement.go). Measured against
	// the body ON DISK so that only a declaration THIS edit introduces is
	// refused: an item that already carries an unreachable line keeps it, and an
	// --append-body-file correction onto it still goes through, for the same
	// reason mg-d878 grandfathers the missing-carrier case.
	if err := checkCarrierPlacement(composeBody(item), bodyBefore); err != nil {
		return nil, nil, err
	}

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
			"body_read_state":  recordedReadState(mode, asserted),
		}
		if asserted != "" {
			extra["body_hash_asserted"] = asserted
		}
		if backupPath != "" {
			extra["body_backup"] = backupPath
		}
		// A title move is recorded with the same before/after shape as any
		// other field, because it IS one — even though it has no frontmatter
		// key and rides inside the body. Without this, the one field an agent
		// searches an item by could change with nothing in events.jsonl saying
		// so (mg-bac6); the 196 bodies already carrying a stacked title have no
		// record of when it happened, which is why reconstructing this took two
		// probes and nine days.
		if change.TitleBefore != change.TitleAfter {
			extra["title_before"] = change.TitleBefore
			extra["title_after"] = change.TitleAfter
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
// The titles are derived from the two bodies by the same rule Parse uses, so
// they are what a re-read reports rather than what the caller intended.
func describeBodyChange(fields UpdateField, before, after string) *BodyChange {
	mode := "incidental"
	switch {
	case fields.Body != nil:
		mode = "replace"
	case fields.AppendBody != nil:
		mode = "append"
	}
	_, titleBefore, _ := firstHeadingLine(before)
	_, titleAfter, _ := firstHeadingLine(after)
	extra := countHeadings(after) - 1
	if extra < 0 {
		extra = 0
	}
	return &BodyChange{
		Changed:       before != after,
		Mode:          mode,
		HashBefore:    BodyHash(before),
		HashAfter:     BodyHash(after),
		LinesBefore:   countBodyLines(before),
		LinesAfter:    countBodyLines(after),
		TitleBefore:   titleBefore,
		TitleAfter:    titleAfter,
		ExtraHeadings: extra,
	}
}

// recordedReadState names, for one write, whether the log captured what the
// CALLER believed it was overwriting — the field that separates "no lost
// updates" from "lost updates are not observable here" (mg-43d0).
//
// THE GAP THIS CLOSES, AND WHY IT MATTERED. body_hash_before is taken from
// item.Body INSIDE this function, i.e. the stored state at WRITE time. It is
// never what the caller read. So it always equals the previous record's
// body_hash_after, and any "zero clobbers" computed from those two fields is
// true BY CONSTRUCTION rather than by measurement. pm-pogo computed exactly that
// figure over the live log and had to retract it. These two writes are identical
// in the record and always were:
//
//	a deliberate rewrite of the body as it currently stands
//	a rewrite by someone whose read was ten minutes and three edits stale
//
// The only place a caller's own read-state has ever existed is --if-unchanged,
// and it is opt-in: 93 of 138 measured replaces did not supply it. Recording its
// ABSENCE explicitly is the whole point — a silent gap reads as clean, and read
// as clean it was. "unmeasurable" is a statement mg can stand behind; "no
// clobbers observed" was not.
//
// This changes no behaviour and refuses nothing. It makes the question
// answerable going forward, for the writes made from here on.
//
// The three values, each a claim the log can support:
//
//	asserted      — the caller named the body version it believed it was
//	                overwriting (--if-unchanged), and that version is recorded
//	                alongside as body_hash_asserted.
//	unmeasurable  — a full-body replacement landed with no record of the
//	                caller's read-state. Whether it destroyed an unseen write is
//	                NOT DERIVABLE from this log, in either direction.
//	not_at_risk   — the write could not lose a body section: an append composes
//	                against the body on disk at write time, and metadata-only and
//	                title-incidental writes overwrite no prose at all.
//
// asserted wins over the mode because it is a fact about the caller and the
// others are facts about the write: a caller that named its read-state did so
// whether or not the body turned out to be at risk.
func recordedReadState(mode, asserted string) string {
	switch {
	case asserted != "":
		return "asserted"
	case mode == "replace":
		return "unmeasurable"
	default:
		return "not_at_risk"
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

// errAssigneeChanged is the loud refusal --if-assignee exists to produce.
//
// It names both values and when the file was last written, which is enough to
// find the other writer: `mg event list --type=work.edited` records every
// assignee move with its actor and its before/after pair (mg-3122), so the
// question "who un-gated this, and when" has an answer that does not depend on
// anyone having been watching.
//
// Conflict (exit 4), not retryable: re-running the identical command against the
// same stored value can never succeed. The remedy is to decide which of the two
// holds — which is a judgement, not a retry.
func errAssigneeChanged(id, itemPath, want, stored string) *mgerr.Error {
	modified := "unknown"
	if info, err := os.Stat(itemPath); err == nil {
		modified = info.ModTime().UTC().Format(time.RFC3339)
	}

	show := func(v string) string {
		if v == "" {
			return "(unset)"
		}
		return strconv.Quote(v)
	}

	msg := fmt.Sprintf(
		"%s: the assignee changed since you read it — refusing the edit.\n"+
			"  you passed --if-assignee=%s\n"+
			"  on disk now %s (last written %s)",
		id, show(want), show(stored), modified)

	hint := fmt.Sprintf(
		"Someone else reassigned %s after the read this edit is based on. The assignee is "+
			"the dispatch gate, so this is the difference between an item that is held and one "+
			"that is being offered. Find the writer with "+
			"'mg event list --type=work.edited | grep %s' — every assignee move records its "+
			"actor and both values. Then either accept theirs (drop --if-assignee) or re-assert "+
			"yours ('mg edit %s --assignee=%s').",
		id, id, id, want)

	return mgerr.Conflict("assignee_changed", msg, hint)
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

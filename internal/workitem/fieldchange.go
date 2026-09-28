package workitem

import (
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
)

// FieldChange is one metadata field an update moved, with the value on either
// side of the write.
//
// Why this exists (mg-3122). Until now `work.edited` was emitted only when the
// BODY changed. `mg edit <id> --assignee=X` and `mg edit <id> --priority=high`
// both printed "Updated <id>" and wrote nothing at all to events.jsonl, so the
// log recorded exit 0 as if nothing had happened. That matters most for
// `assignee`, which is the dispatch gate — `config.IsDispatchGated` suppresses
// both stall-watch and dispatch for `human` and `parked` — so the single field
// deciding whether an item is ever worked on could be flipped by any agent
// with no audit record whatsoever.
type FieldChange struct {
	Name   string
	Before string
	After  string
}

// metaSnapshot is the item's non-body metadata flattened to strings, so a
// before/after comparison is one struct equality per field and the values are
// already in the form the event log stores.
type metaSnapshot struct {
	title    string
	typ      string
	repo     string
	assignee string
	priority string
	budget   string
	depends  string
	tags     string
}

func snapshotMeta(item *Item) metaSnapshot {
	return metaSnapshot{
		title:    item.Title,
		typ:      item.Type,
		repo:     item.Repo,
		assignee: item.Assignee,
		priority: item.Priority,
		budget:   budgetString(item.Budget),
		depends:  strings.Join(item.Depends, ","),
		tags:     strings.Join(item.Tags, ","),
	}
}

// budgetString renders an optional budget. An unset budget is the empty string
// rather than "0", because 0 is also the value --budget=0 uses to UNSET one and
// a log that cannot tell "no budget" from "a budget of zero" reintroduces the
// same class of ambiguity in miniature.
func budgetString(b *int) string {
	if b == nil {
		return ""
	}
	return strconv.Itoa(*b)
}

// diffMeta returns the fields that moved, in a fixed order so two identical
// edits produce byte-identical `fields` lists in the log.
//
// An unset value is reported as the empty string on whichever side it falls,
// and a cleared field is a change like any other: `assignee_before=parked`,
// `assignee_after=` says the gate was opened, which is exactly the transition
// worth being able to find.
func diffMeta(before, after metaSnapshot) []FieldChange {
	pairs := []struct {
		name          string
		before, after string
	}{
		{"title", before.title, after.title},
		{"type", before.typ, after.typ},
		{"repo", before.repo, after.repo},
		{"assignee", before.assignee, after.assignee},
		{"priority", before.priority, after.priority},
		{"budget", before.budget, after.budget},
		{"depends", before.depends, after.depends},
		{"tags", before.tags, after.tags},
	}

	var changes []FieldChange
	for _, p := range pairs {
		if p.before != p.after {
			changes = append(changes, FieldChange{Name: p.name, Before: p.before, After: p.after})
		}
	}
	return changes
}

// emitTagWrite records a tag write made OUTSIDE Update — the successor: and
// predecessor: tags `mg done --successor` and `mg archive --successor` put on
// two items (drellem2/macguffin#38). Those writes changed a field `mg edit`
// would have logged, and logged nothing, so a tag on the item could not be
// dated or attributed afterwards.
//
// The line is the same shape Update emits for a metadata-only edit, so a
// consumer reading work.edited needs no second parser: mode=metadata,
// fields=tags, tags_before/tags_after in the comma-joined form, actor the
// invoker, and equal body hashes as the positive statement that no body moved.
func emitTagWrite(root string, item *Item, tagsBefore []string) {
	body := composeBody(item)
	hash := BodyHash(body)
	lines := strconv.Itoa(countBodyLines(body))
	event.Emit(root, "work.edited", map[string]string{
		"item_id":          item.ID,
		"actor":            actor(),
		"mode":             "metadata",
		"guarded":          "false",
		"body_hash_before": hash,
		"body_hash_after":  hash,
		"lines_before":     lines,
		"lines_after":      lines,
		"body_read_state":  recordedReadState("metadata", ""),
		"fields":           "tags",
		"tags_before":      strings.Join(tagsBefore, ","),
		"tags_after":       strings.Join(item.Tags, ","),
	})
}

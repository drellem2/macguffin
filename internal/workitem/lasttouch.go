package workitem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/drellem2/macguffin/internal/event"
)

// WHY AN EDIT SAYS WHO TOUCHED THE ITEM LAST (mg-43d0).
//
// Two agents editing one item cannot see each other, and the cost of that is
// not lost data — it is a false bug report. On 2026-08-11 the mayor mailed
// Daniel that `mg edit --append-body-file` was silently reverting an assignee
// field, filed it high as mg-5eee, and an investigation was dispatched. It then
// retracted: "it was pm-pogo and me clobbering each other". BOTH WRITES HAD
// LANDED. pm-pogo was deliberately handing the item back, twice, and the value
// `"mayor"` that read as corruption was a colleague answering.
//
// So the defect is that a colleague's edit and a corrupted field are the same
// observation. The fix is to name the colleague at the moment the next writer
// is standing there:
//
//	mg edit mg-1234 ...
//	note: mg-1234 was last edited 4m ago by mayor (append). ...
//
// DELIBERATELY NOT A LOCK, for the reason UpdateWithBodyChange already gives at
// length: 919 of 1,287 edits measured over 15 days already use --append-body-file,
// which composes against the body on disk at write time and cannot clobber.
// Locking would make the safe path heavier, which is how callers get routed onto
// the unsafe one. This adds a line of stderr and refuses nothing.
//
// WHAT SILENCE MEANS, EXACTLY. The note is suppressed when the last recorded
// editor is the caller themselves, because "you last wrote this" is the state
// the caller already believes they are in and printing it on every edit is how
// a warning becomes wallpaper. Silence therefore means: NO OTHER PARTY HAS A
// RECORDED EDIT OF THIS ITEM SINCE YOU LAST WROTE IT. That is one statement,
// not two — "nobody has ever edited it" and "only I have" are both instances of
// it.
//
// NO RECENCY THRESHOLD, and that is a decision rather than an omission. A cutoff
// ("only warn about edits inside 24h") would be a number nothing in the incident
// record justifies, and a wrong cutoff turns silence into a lie. The age is
// PRINTED instead, so a reader can discount a three-week-old touch themselves —
// which is a judgement they can make and mg cannot.
//
// THE SAME DEFECT, INSIDE THE REMEDY. This tells two writers apart by 'actor',
// and 'actor' falls through to the OS user when neither MG_ACTOR nor
// POGO_AGENT_NAME is set — and every agent on this box IS that one unix user.
// So two callers that both fall through are indistinguishable here, and the
// suppression below would read one as the other and stay quiet. It is the
// attribution limit 'mg event list --help' already documents rather than a new
// one, and the occupant of that step is pogod, which writes work.claim and
// work.done and not work.edited. Naming it anyway: a remedy for invisible
// concurrency that has its own invisible case should say where the case is.
//
// THE HONEST GAP: this reads events.jsonl, so it reports what was RECORDED, not
// what happened. Edits made before work.edited carried item_id (2026-07-29) are
// invisible here, as is anything written while the log was unwritable —
// event.Emit is best-effort by design, because a state transition must not fail
// because the log is full. An absent record therefore means "not recorded",
// which is weaker than "did not happen". It is stated here rather than in the
// note because the note fires on presence, and presence is never in doubt.

// Touch is the most recent recorded edit of one work item: who, when, and what
// shape of write. It is what the events log knows, not what mg observed.
type Touch struct {
	Actor string    // the 'actor' on the work.edited event — self-asserted, see 'mg event list --help'
	When  time.Time // parsed from the event's RFC3339 'ts'
	Mode  string    // "replace", "append", "metadata" or "incidental"
}

// LastTouch returns the most recent recorded work.edited event for id, or nil
// if the log has none (including when there is no log at all, which is the
// state of a freshly initialised workspace).
//
// It scans the log BACKWARDS and unmarshals only lines that could match, so the
// cost is one sequential read plus a substring scan rather than 40,000 JSON
// decodes. That matters because this runs on the front of every `mg edit`: the
// live log is 5.3 MB / 40,779 lines as of 2026-08-12 and grows monotonically,
// and a visibility aid that taxes the write path is one that gets removed.
//
// Errors are swallowed on purpose, exactly as event.Emit swallows its own. This
// is an advisory note on a write that has already been decided; a log that
// cannot be read must not be able to refuse an edit.
func LastTouch(root, id string) *Touch {
	if id == "" {
		return nil
	}
	data, err := os.ReadFile(event.LogPath(root))
	if err != nil {
		return nil
	}

	// Both needles are layout-independent substrings of any matching line —
	// they do not assume key order or spacing, because the authoritative check
	// is the unmarshal below. They exist only to reject the ~99% of lines that
	// cannot match before paying for a decode.
	idNeedle := []byte(id)
	typeNeedle := []byte("work.edited")

	for line, rest := lastLine(data); line != nil; line, rest = lastLine(rest) {
		if !bytes.Contains(line, idNeedle) || !bytes.Contains(line, typeNeedle) {
			continue
		}
		var e event.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // malformed lines are skipped, as event.List skips them
		}
		if e.Type != "work.edited" || e.Extra["item_id"] != id {
			continue
		}
		t := &Touch{Actor: e.Extra["actor"], Mode: e.Extra["mode"]}
		if ts, err := time.Parse(time.RFC3339, e.Ts); err == nil {
			t.When = ts
		}
		return t
	}
	return nil
}

// lastLine splits the final line off the end of data, returning that line and
// everything before it. Trailing newlines are consumed, so a log ending in "\n"
// does not yield a phantom empty last line. A nil line means data is exhausted.
func lastLine(data []byte) (line, rest []byte) {
	for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return nil, nil
	}
	i := bytes.LastIndexByte(data, '\n')
	return bytes.TrimRight(data[i+1:], "\r"), data[:i+1]
}

// EditNotice returns the stderr note `mg edit` prints about who touched the item
// last, or "" for silence. self is the caller's own actor identity (Actor()) and
// now is the clock the age is measured against — both passed in so the decision
// is a pure function that can be tested without an environment or a wall clock.
//
// The wording names the mode because that is the discriminator the mayor did not
// have: an append composed against the body on disk and cannot have destroyed
// what the reader is looking at, so "append" alone answers "was this a clobber?"
// with no.
func EditNotice(t *Touch, self, id string, now time.Time) string {
	if t == nil || t.Actor == "" || t.Actor == self {
		return ""
	}
	mode := t.Mode
	if mode == "" {
		mode = "unrecorded mode"
	}
	return fmt.Sprintf(
		"%s was last edited %s by %s (%s). A field you did not write is a colleague, "+
			"not corruption — 'mg event list --type=work.edited | grep %s' names every writer "+
			"and both body hashes.",
		id, humanAgo(t.When, now), t.Actor, mode, id)
}

// humanAgo renders how long ago an event happened, reusing HumanUntil so there
// stays one duration vocabulary in mg rather than two that disagree at the
// boundaries.
//
// A zero When means the event's 'ts' would not parse; say so rather than
// printing an age computed from the epoch, which would read as "56 years ago"
// and be believed. A When in the future is a clock step or a sub-second
// ordering artifact, not information: it renders as "just now".
func humanAgo(when, now time.Time) string {
	if when.IsZero() {
		return "at an unreadable time"
	}
	d := now.Sub(when)
	if d < time.Minute {
		return "just now"
	}
	return HumanUntil(d) + " ago"
}

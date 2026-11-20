package mgcli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "Structured event logging",
}

var eventAppendCmd = &cobra.Command{
	Use:   "append EVENT_TYPE [--key=value ...]",
	Short: "Append a structured event to the event log",
	Long: `Append a JSON line to <workspace>/events.jsonl.

Auto-adds 'ts' field with RFC3339 timestamp.
Event type is positional arg, all other fields are --key=value flags.

Only 'ts' is added for you. In particular 'actor' is NOT — mg's own state
changes resolve and stamp it themselves, but a hand-appended event carries only
what you pass, so pass --actor=<who> if the line should attribute to anyone. See
'mg event list --help' for what the field means and how to read it.

Example:
  mg event append agent.start --agent=crew-arch --type=crew
  mg event append work.claim --agent=cat-a3f --item=gt-a3f`,
	Args:               usageArgs(cobra.MinimumNArgs(1)),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// DisableFlagParsing means cobra never intercepts --help/-h, so guard
		// it here BEFORE any side effect — otherwise `mg event append --help`
		// persists a junk {"type":"--help"} event. See drellem2/pogo#54.
		if helpRequested(args) {
			return cmd.Help()
		}

		// DisableFlagParsing also means cobra never binds the persistent --root
		// flag here: `--root=/tmp/x` would be parsed below as an ordinary
		// --key=value pair and appended, as a junk "root" field, to the DEFAULT
		// store. Refuse rather than write to a store the caller believes it
		// redirected away from (mg-4fa7).
		if err := rejectRootFlag(args, cmd.UseLine()); err != nil {
			return err
		}

		root, err := resolveRoot()
		if err != nil {
			return err
		}

		eventType := args[0]
		// Invalid UTF-8 is refused rather than handed to the JSON encoder,
		// which would silently store U+FFFD in its place (drellem2/macguffin#41).
		if err := mgerr.CheckUTF8("event type", eventType); err != nil {
			return err
		}
		kvs := make(map[string]string)

		for i, arg := range args[1:] {
			// Named by position, so the offset points into the argument
			// exactly as the caller typed it, "--" included.
			if err := mgerr.CheckUTF8(fmt.Sprintf("argument %d", i+2), arg); err != nil {
				return err
			}
			if !strings.HasPrefix(arg, "--") {
				return fmt.Errorf("unexpected positional argument %q (use --key=value)", arg)
			}
			kv := strings.TrimPrefix(arg, "--")
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid flag %q (use --key=value)", arg)
			}
			kvs[parts[0]] = parts[1]
		}

		entry, err := event.Append(root, eventType, kvs)
		if err != nil {
			return err
		}

		data, _ := json.Marshal(entry)
		fmt.Println(string(data))
		return nil
	},
}

var eventListType string
var eventListSince string
var eventListUntil string
var eventListWithTitle bool
var eventListTail int
var eventListJSON bool

var eventListCmd = &cobra.Command{
	Use:   "list",
	Short: "List events from the event log",
	Long: `Read events from <workspace>/events.jsonl with optional filtering.

Examples:
  mg event list
  mg event list --type=agent.start
  mg event list --tail=10
  mg event list --since=2026-01-01T00:00:00Z
  mg event list --since=7d
  mg event list --type=work.done --since=2026-11-19 --until=2026-11-20 --with-title

TIME WINDOW: --since AND --until

Both bounds are parsed as times and compared as instants, so the offset a
bound is written in does not matter. Each accepts:

  RFC3339       2026-11-21T00:00:00+05:00, 2026-11-20T19:00:00Z (any offset)
  YYYY-MM-DD    a bare date means LOCAL midnight at the start of that day, the
                same anchor as 'mg spend --window=today'
  duration      24h, 30m, 7d: that long before now. No sign: -24h is a usage
                error, because it would name a time in the future

--since is inclusive (ts >= since); --until is EXCLUSIVE (ts < until), so
--since=2026-11-19 --until=2026-11-20 is exactly the local day of the 19th.
A value that is none of these is a usage error, not an empty result.

TITLES: --with-title

--with-title adds a 'title' field to every event that names an item
('item_id'). It is the item's CURRENT title, looked up across every status
including archived at the time you run the command; it is NOT the title the
item had when the event was written. An event that already carries a 'title'
key keeps it unchanged; only events without one get the item's title. An event
whose item can no longer be found gets no 'title' field.

READING 'actor'

This log is the ONLY read surface for 'actor' — 'mg show --json' does not expose
it — so the caveats live here rather than somewhere a reader of the field has no
reason to look.

'actor' is whoever RAN the command: MG_ACTOR, else POGO_AGENT_NAME, else the OS
user, else "unknown". It is never a property of the item acted on. Like
'creator' (see 'mg show --help') it is SELF-ASSERTED and forgeable, so it is
attribution and not authentication.

Read 'actor: daniel' as "pogod, or unknown" — NOT as Daniel. pogod runs with
neither MG_ACTOR nor POGO_AGENT_NAME set, so its own actions fall through to the
OS user, and every agent on this box IS that OS user. Measured 2026-07-30 over
the live log: every 'daniel' line was a work.claim or work.done written by the
daemon (pid = pogod's), including a work.claim on an item that had no assignee
at all. Those two types are the dispatch and completion path, so the reading
that matters most is the one the field cannot support.

Events written before mg-3122 carry the OLD meaning, in which 'actor' resolved
to the item's ASSIGNEE first, then its creator, and only then the OS user. The
log is append-only and history is not rewritten: treat 'actor' on a pre-mg-3122
line as "the assignee at the time", not as the caller. That is a statement about
old lines only — it is not what the field does now.

mail.read and mail.sent carry no 'actor' at all; they attribute with 'from' and
'to'. Every work.* type carries one. An absent 'actor' is therefore the shape of
the event, not a dropped value.

READING 'body_read_state' ON work.edited

Do not compute lost updates from 'body_hash_before'. It is read from the STORED
body inside the edit — the state at WRITE time, never what the caller read — so
it always equals the previous line's 'body_hash_after' by construction, and a
"zero clobbers" figure derived from the pair is guaranteed rather than measured.
That figure was computed over this log and retracted (mg-43d0).

'body_read_state' is the field that can answer the question, for lines written
from 2026-08-12 on:

  asserted      the caller named the body version it believed it was overwriting
                (--if-unchanged). The value is alongside as 'body_hash_asserted'.
  unmeasurable  a full-body replacement landed with no record of the caller's
                read-state. Whether it destroyed an unseen write is NOT derivable
                from this log, in either direction. It is not evidence of a
                clobber and it is not evidence of none.
  not_at_risk   the write could not lose a body section: an append composes
                against the body on disk at write time, and metadata-only and
                title-incidental writes overwrite no prose.

The field is about the BODY, which is what its name says. A metadata edit is
'not_at_risk' because no prose was at stake — the assignee it moved is guarded
by --if-assignee and by 'fields'/'assignee_before'/'assignee_after' on this same
line, not by this one. Lines written before 2026-08-12 carry no such field at
all; absent means "an older mg wrote this", which is itself unmeasurable.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		now := time.Now()
		since, err := parseTimeBound("--since", eventListSince, now)
		if err != nil {
			return err
		}
		until, err := parseTimeBound("--until", eventListUntil, now)
		if err != nil {
			return err
		}

		entries, err := event.List(root, event.ListOpts{
			Type:  eventListType,
			Since: since,
			Until: until,
			Tail:  eventListTail,
		})
		if err != nil {
			return err
		}

		var titles map[string]string
		if eventListWithTitle {
			titles = loadTitleIndex(root)
		}

		if info, statErr := event.Stat(root); statErr == nil && (!info.Exists || info.Size == 0) {
			fmt.Fprintf(os.Stderr, "no events found at %s\n", info.Where)
			return nil
		}

		for _, e := range entries {
			if titles != nil {
				_, hasTitle := e.Extra["title"]
				// An event that already carries a 'title' keeps it: --with-title
				// adds the item's title only where there is none.
				if title, ok := titles[e.Extra["item_id"]]; ok && e.Extra["item_id"] != "" && !hasTitle {
					extra := make(map[string]string, len(e.Extra)+1)
					for k, v := range e.Extra {
						extra[k] = v
					}
					extra["title"] = title
					e.Extra = extra
				}
			}
			data, _ := json.Marshal(e)
			fmt.Println(string(data))
		}
		return nil
	},
}

func init() {
	eventListCmd.Flags().StringVar(&eventListType, "type", "", "filter by event type")
	eventListCmd.Flags().StringVar(&eventListSince, "since", "", "keep events at or after this time: RFC3339, YYYY-MM-DD (local midnight), or a duration ago (24h, 7d)")
	eventListCmd.Flags().StringVar(&eventListUntil, "until", "", "keep events strictly BEFORE this time (exclusive); same forms as --since")
	eventListCmd.Flags().BoolVar(&eventListWithTitle, "with-title", false, "add each item's CURRENT title (not the title at event time) to events that carry item_id")
	eventListCmd.Flags().IntVar(&eventListTail, "tail", 0, "show only the last N entries")
	// `event list` output is already unconditional NDJSON (one JSON object per
	// line), so --json is accepted for consistency with list/spend/show and is
	// a no-op. Registering it turns the previous "unknown flag" error into a
	// success. See drellem2/pogo#55.
	eventListCmd.Flags().BoolVar(&eventListJSON, "json", false, "emit events as NDJSON (already the default; accepted for consistency)")

	eventCmd.AddCommand(eventAppendCmd)
	eventCmd.AddCommand(eventListCmd)
}

// parseTimeBound turns a --since/--until value into an instant. It accepts
// RFC3339 with any offset, a bare YYYY-MM-DD meaning local midnight at the
// start of that day (the anchor 'mg spend --window' uses), or a duration such
// as 24h or 7d meaning that long before now. Empty means no bound (zero time).
// Anything else is a usage error: an unparsed bound used to be compared as a
// string and silently matched nothing (drellem2/macguffin#48).
func parseTimeBound(flag, value string, now time.Time) (time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, now.Location()); err == nil {
		return t, nil
	}
	// A leading '-' is refused outright. time.ParseDuration accepts "-24h",
	// and now.Add(-(-24h)) is a bound in the FUTURE, which matched nothing and
	// exited 0 while "-7d" was already an error: the same silent-empty class
	// macguffin#48 removed. Durations count back from now; no sign is needed.
	if strings.HasPrefix(v, "-") {
		return time.Time{}, mgerr.Usage("invalid_value",
			fmt.Sprintf("%s: %q: durations count back from now; write %s, not %s", flag, value, strings.TrimLeft(v, "-"), v),
			"")
	}
	if d, err := parseDuration(v); err == nil {
		return now.Add(-d), nil
	}
	return time.Time{}, mgerr.Usage("invalid_value",
		fmt.Sprintf("%s: %q is not a time: want RFC3339 (2026-11-21T00:00:00+05:00), YYYY-MM-DD (local midnight), or a duration (24h, 7d)", flag, value),
		"")
}

// loadTitleIndex maps every work item id to its CURRENT title, across all
// active statuses and the archive. Archived records are indexed first so that
// an active record with the same id wins: the live title is the current one.
func loadTitleIndex(root string) map[string]string {
	idx := make(map[string]string)
	if archived, err := workitem.ListArchived(root); err == nil {
		for _, it := range archived {
			idx[it.ID] = it.Title
		}
	}
	for _, status := range workitem.ActiveStatuses() {
		items, err := workitem.ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, it := range items {
			idx[it.ID] = it.Title
		}
	}
	return idx
}

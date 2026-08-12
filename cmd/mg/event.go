package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
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
		kvs := make(map[string]string)

		for _, arg := range args[1:] {
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

		entries, err := event.List(root, event.ListOpts{
			Type:  eventListType,
			Since: eventListSince,
			Tail:  eventListTail,
		})
		if err != nil {
			return err
		}

		path := event.LogPath(root)
		if info, statErr := os.Stat(path); os.IsNotExist(statErr) || (statErr == nil && info.Size() == 0) {
			fmt.Fprintf(os.Stderr, "no events found at %s\n", path)
			return nil
		}

		for _, e := range entries {
			data, _ := json.Marshal(e)
			fmt.Println(string(data))
		}
		return nil
	},
}

func init() {
	eventListCmd.Flags().StringVar(&eventListType, "type", "", "filter by event type")
	eventListCmd.Flags().StringVar(&eventListSince, "since", "", "filter events at or after this RFC3339 timestamp")
	eventListCmd.Flags().IntVar(&eventListTail, "tail", 0, "show only the last N entries")
	// `event list` output is already unconditional NDJSON (one JSON object per
	// line), so --json is accepted for consistency with list/spend/show and is
	// a no-op. Registering it turns the previous "unknown flag" error into a
	// success. See drellem2/pogo#55.
	eventListCmd.Flags().BoolVar(&eventListJSON, "json", false, "emit events as NDJSON (already the default; accepted for consistency)")

	eventCmd.AddCommand(eventAppendCmd)
	eventCmd.AddCommand(eventListCmd)
}

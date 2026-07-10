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
  mg event list --since=2026-01-01T00:00:00Z`,
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

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/workspace"
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
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagParsing:    true,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
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
		root, err := workspace.DefaultRoot()
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

	eventCmd.AddCommand(eventAppendCmd)
	eventCmd.AddCommand(eventListCmd)
}

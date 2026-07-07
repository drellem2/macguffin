package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// countEventLines returns the number of non-empty lines in the workspace event
// log, or 0 if the file does not exist yet.
func countEventLines(t *testing.T, home string) int {
	t.Helper()
	path := filepath.Join(home, ".macguffin", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("reading events.jsonl: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// TestHelpGuard_EventAppend_NoPersist is the #54 SAFETY regression: `mg event
// append --help` (and -h) must show usage and persist NOTHING. event append
// sets DisableFlagParsing, so without the guard cobra hands "--help" through as
// the event type and a junk {"type":"--help"} event is written to the log.
func TestHelpGuard_EventAppend_NoPersist(t *testing.T) {
	bin := buildBinary(t)
	for _, helpFlag := range []string{"--help", "-h"} {
		t.Run(helpFlag, func(t *testing.T) {
			home := t.TempDir()
			env := emEnv(home)
			emInit(t, bin, env)

			before := countEventLines(t, home)
			out := emOK(t, bin, env, "event", "append", helpFlag)

			// Help text, not a persisted event echoed back.
			if !strings.Contains(out, "Append a JSON line") {
				t.Errorf("expected help output, got: %q", out)
			}
			if after := countEventLines(t, home); after != before {
				t.Errorf("event append %s persisted an event: before=%d after=%d", helpFlag, before, after)
			}
			// Belt-and-suspenders: no junk --help event landed on disk.
			if data, err := os.ReadFile(filepath.Join(home, ".macguffin", "events.jsonl")); err == nil {
				if strings.Contains(string(data), `"type":"--help"`) {
					t.Errorf("events.jsonl contains junk --help event: %s", data)
				}
			}
		})
	}
}

// TestHelpGuard_EventAppend_StillWorks confirms the guard does not break the
// normal append path (a real event type is still persisted).
func TestHelpGuard_EventAppend_StillWorks(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	emOK(t, bin, env, "event", "append", "agent.start", "--agent=cat-x")
	if n := countEventLines(t, home); n != 1 {
		t.Fatalf("expected exactly 1 persisted event, got %d", n)
	}
	data, err := os.ReadFile(filepath.Join(home, ".macguffin", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"agent.start"`) {
		t.Errorf("expected agent.start event, got: %s", data)
	}
}

// TestHelpGuard_Log is the #54 regression for `mg log --help`: log also sets
// DisableFlagParsing (args pass through to git log), so --help/-h must show mg
// usage instead of being forwarded to git.
func TestHelpGuard_Log(t *testing.T) {
	bin := buildBinary(t)
	for _, helpFlag := range []string{"--help", "-h"} {
		t.Run(helpFlag, func(t *testing.T) {
			home := t.TempDir()
			env := emEnv(home)
			emInit(t, bin, env)

			out := emOK(t, bin, env, "log", helpFlag)
			if !strings.Contains(out, "Usage:") || !strings.Contains(out, "git log") {
				t.Errorf("expected mg log usage, got: %q", out)
			}
		})
	}
}

// TestShowJSON_FullObject is the #55 regression for `mg show ID --json`: the
// full work item as ONE JSON object, sharing the frozen field names of
// `mg list --json` plus the single-item extras (creator, body, budget, spent).
func TestShowJSON_FullObject(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	id := emNew(t, bin, env, "Show JSON target")

	out := emOK(t, bin, env, "show", id, "--json")

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("show --json is not a single JSON object: %v\n%s", err, out)
	}
	if obj["id"] != id {
		t.Errorf("id = %v, want %s", obj["id"], id)
	}
	if obj["title"] != "Show JSON target" {
		t.Errorf("title = %v", obj["title"])
	}
	if obj["status"] != "available" {
		t.Errorf("status = %v, want available", obj["status"])
	}
	// budget is unset → JSON null (present, not omitted).
	if v, ok := obj["budget"]; !ok || v != nil {
		t.Errorf("budget should be present and null, got %v (present=%v)", v, ok)
	}
	// Frozen field set for a single item.
	want := []string{
		"id", "type", "status", "title", "tags", "assignee", "priority",
		"repo", "branch", "depends", "created", "mtime", "creator", "body",
		"budget", "spent",
	}
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing frozen field %q; got keys %v", k, sortedKeys(obj))
		}
	}
}

// TestEventListJSON_AcceptedNoBehaviorChange is the #55 regression for
// `mg event list --json`: the flag is accepted (previously "unknown flag") and
// output is unchanged, since event list already emits NDJSON unconditionally.
func TestEventListJSON_AcceptedNoBehaviorChange(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	emOK(t, bin, env, "event", "append", "demo.evt", "--k=v")

	withJSON := emOK(t, bin, env, "event", "list", "--json")
	plain := emOK(t, bin, env, "event", "list")
	if withJSON != plain {
		t.Errorf("--json changed event list output:\nwith:  %q\nplain: %q", withJSON, plain)
	}
	// Each emitted line must be valid JSON (NDJSON contract).
	for _, line := range strings.Split(strings.TrimSpace(withJSON), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("event list line is not valid JSON: %q (%v)", line, err)
		}
	}
}

// TestFlowJSON_OneSchema is the #55 / G1 regression: `mg flow --json` emits ONE
// stable schema whose top-level keys do NOT change between the default status
// path and a --group-by path. The grouped form fills `groups` and leaves the
// status-only fields empty; it does not emit a structurally different shape.
func TestFlowJSON_OneSchema(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	emNew(t, bin, env, "Flow JSON item")

	statusOut := emOK(t, bin, env, "flow", "--json")
	groupedOut := emOK(t, bin, env, "flow", "--json", "--group-by", "repo")

	var status, grouped map[string]any
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatalf("flow --json (status) invalid: %v\n%s", err, statusOut)
	}
	if err := json.Unmarshal([]byte(groupedOut), &grouped); err != nil {
		t.Fatalf("flow --json (grouped) invalid: %v\n%s", err, groupedOut)
	}

	// Same top-level keys regardless of the invocation flag — the invariant.
	sk, gk := sortedKeys(status), sortedKeys(grouped)
	if strings.Join(sk, ",") != strings.Join(gk, ",") {
		t.Errorf("flow --json schema differs by flag:\nstatus:  %v\ngrouped: %v", sk, gk)
	}
	for _, k := range []string{"generated_at", "group_by", "bottleneck", "statuses", "groups", "blocked", "spawn"} {
		if _, ok := status[k]; !ok {
			t.Errorf("status path missing top-level key %q", k)
		}
	}
	if status["group_by"] != "status" {
		t.Errorf("status path group_by = %v, want status", status["group_by"])
	}
	if grouped["group_by"] != "repo" {
		t.Errorf("grouped path group_by = %v, want repo", grouped["group_by"])
	}
	// Status-only fields are empty on the grouped path (one schema, grouped
	// adds `groups`; it does not repopulate status-grouping concepts).
	if s, ok := grouped["statuses"].([]any); !ok || len(s) != 0 {
		t.Errorf("grouped path statuses should be empty, got %v", grouped["statuses"])
	}
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

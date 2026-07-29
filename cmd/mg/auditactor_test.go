package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mg-3122, end to end through the real binary and the real events.jsonl.
//
// The unit tests in internal/workitem cover the same two defects, but they call
// Update directly. The defects were MEASURED at the CLI — `mg edit <id>
// --assignee=zzz-probe-actor` then reading ~/.macguffin/events.jsonl — and the
// invoker identity now comes from the process environment, which a unit test
// inside the package can only simulate. So the whole path is exercised here:
// argv → cobra → workitem.Update → the JSONL file on disk.

// mgAs runs the mg binary against root with POGO_AGENT_NAME set to agent, and
// with MG_ACTOR scrubbed so the ambient environment cannot answer for it.
func mgAs(t *testing.T, bin, root, agent string, args ...string) (string, int) {
	t.Helper()
	full := append([]string{"--root=" + root}, args...)
	cmd := exec.Command(bin, full...)
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "POGO_AGENT_NAME=") || strings.HasPrefix(kv, "MG_ACTOR=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "POGO_AGENT_NAME="+agent)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mg %v: %v", args, err)
	}
	return string(out), code
}

// readEventObjects returns every event in the log as a flat map, in file order.
func readEventObjects(t *testing.T, bin, root string) []map[string]string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "event", "list", "--json")
	if code != 0 {
		t.Fatalf("mg event list: exit %d\n%s", code, out)
	}
	var events []map[string]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue // the "no events found" stderr notice
		}
		var e map[string]string
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("event line %q is not a flat JSON object: %v", line, err)
		}
		events = append(events, e)
	}
	return events
}

const (
	cliInvoker  = "zzz-probe-invoker"
	cliAssignee = "zzz-probe-assignee"
)

// TestCLI_AuditActorIsTheInvoker_PositiveControl is the acceptance check
// mg-3122 spells out: set an assignee that DIFFERS from the invoker, act, and
// assert `actor` is the invoker.
//
// A test asserting only that `actor` is non-empty passes with the bug present —
// the assignee is a non-empty string too — so both strings here are values that
// could have come from nowhere else, and the assertion names one of them
// exactly while explicitly rejecting the other.
func TestCLI_AuditActorIsTheInvoker_PositiveControl(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliInvoker, "new", "task", "Probe attribution")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	if out, code := mgAs(t, bin, root, cliInvoker, "edit", id, "--assignee="+cliAssignee); code != 0 {
		t.Fatalf("mg edit --assignee: exit %d\n%s", code, out)
	}

	// Count BEFORE the act, so the event read afterwards is provably new. The
	// ticket's own investigation nearly recorded a refutation off a stale event
	// that looked identical to a fresh one.
	before := len(readEventObjects(t, bin, root))

	if out, code := mgAs(t, bin, root, cliInvoker, "edit", id, "--priority=high"); code != 0 {
		t.Fatalf("mg edit --priority: exit %d\n%s", code, out)
	}

	after := readEventObjects(t, bin, root)
	if len(after) != before+1 {
		t.Fatalf("event count %d → %d, want exactly one new event", before, len(after))
	}
	e := after[len(after)-1]

	if e["type"] != "work.edited" {
		t.Fatalf("newest event type = %q, want work.edited", e["type"])
	}
	if e["actor"] == cliAssignee {
		t.Errorf("actor = %q — that is the ASSIGNEE, which is the mg-3122 defect", e["actor"])
	}
	if e["actor"] != cliInvoker {
		t.Errorf("actor = %q, want %q (the invoker, from POGO_AGENT_NAME)", e["actor"], cliInvoker)
	}
}

// TestCLI_AssigneeChangeIsInTheLog is the ticket's second acceptance point: an
// --assignee change appears in events.jsonl with old and new values.
//
// It matters because `assignee` is the dispatch gate — config.IsDispatchGated
// suppresses stall-watch and dispatch for `human` and `parked` — so before this
// fix the field that decides whether an item is ever worked on could be flipped
// by any agent with no audit record at all.
func TestCLI_AssigneeChangeIsInTheLog(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliInvoker, "new", "task", "Gate me")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	if out, code := mgAs(t, bin, root, cliInvoker, "edit", id, "--assignee=mayor"); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}

	before := len(readEventObjects(t, bin, root))

	// The gate closes: mayor → parked. This is the transition that used to
	// leave events.jsonl byte-identical.
	if out, code := mgAs(t, bin, root, cliInvoker, "edit", id, "--assignee=parked"); code != 0 {
		t.Fatalf("mg edit --assignee=parked: exit %d\n%s", code, out)
	}

	after := readEventObjects(t, bin, root)
	if len(after) != before+1 {
		t.Fatalf("event count %d → %d, want exactly one new event for an --assignee change", before, len(after))
	}
	e := after[len(after)-1]

	if e["type"] != "work.edited" {
		t.Fatalf("newest event type = %q, want work.edited", e["type"])
	}
	if e["item_id"] != id {
		t.Errorf("item_id = %q, want %q", e["item_id"], id)
	}
	if e["assignee_before"] != "mayor" {
		t.Errorf("assignee_before = %q, want mayor", e["assignee_before"])
	}
	if e["assignee_after"] != "parked" {
		t.Errorf("assignee_after = %q, want parked", e["assignee_after"])
	}
	if e["fields"] != "assignee" {
		t.Errorf("fields = %q, want assignee", e["fields"])
	}
	if e["mode"] != "metadata" {
		t.Errorf("mode = %q, want metadata", e["mode"])
	}
	if e["actor"] != cliInvoker {
		t.Errorf("actor = %q, want %q", e["actor"], cliInvoker)
	}
}

// TestCLI_MetadataEditIsNotSilent walks the exact pair of commands mg-3122
// measured emitting zero events, and asserts the log grows for each.
func TestCLI_MetadataEditIsNotSilent(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliInvoker, "new", "task", "Silent no more")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	for _, step := range []struct{ flag, field, want string }{
		{"--assignee=" + cliAssignee, "assignee", cliAssignee},
		{"--priority=high", "priority", "high"},
	} {
		before := len(readEventObjects(t, bin, root))
		if out, code := mgAs(t, bin, root, cliInvoker, "edit", id, step.flag); code != 0 {
			t.Fatalf("mg edit %s: exit %d\n%s", step.flag, code, out)
		}
		after := readEventObjects(t, bin, root)
		if len(after) != before+1 {
			t.Fatalf("mg edit %s: event count %d → %d, want exactly one new event",
				step.flag, before, len(after))
		}
		e := after[len(after)-1]
		if got := e[step.field+"_after"]; got != step.want {
			t.Errorf("mg edit %s: %s_after = %q, want %q", step.flag, step.field, got, step.want)
		}
		if e["actor"] != cliInvoker {
			t.Errorf("mg edit %s: actor = %q, want %q", step.flag, e["actor"], cliInvoker)
		}
	}
}

// TestCLI_AuditActorFallsBackToTheOSUser: with no POGO_AGENT_NAME and no
// MG_ACTOR, attribution degrades to the unix user rather than to the assignee.
// Weak is acceptable; wrong is not.
func TestCLI_AuditActorFallsBackToTheOSUser(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	// mgAs with an empty agent leaves POGO_AGENT_NAME set to "", which the
	// resolver treats as unset.
	out, code := mgAs(t, bin, root, "", "new", "task", "No agent name")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	if out, code := mgAs(t, bin, root, "", "edit", id, "--assignee="+cliAssignee); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}

	before := len(readEventObjects(t, bin, root))
	if out, code := mgAs(t, bin, root, "", "edit", id, "--priority=low"); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}
	after := readEventObjects(t, bin, root)
	if len(after) != before+1 {
		t.Fatalf("event count %d → %d, want exactly one new event", before, len(after))
	}

	e := after[len(after)-1]
	if e["actor"] == cliAssignee {
		t.Errorf("actor = %q — falling back to the assignee is the defect, not the fallback", e["actor"])
	}
	if strings.TrimSpace(e["actor"]) == "" {
		t.Error("actor is empty; want the OS user, or at worst \"unknown\"")
	}
}

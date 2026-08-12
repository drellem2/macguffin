package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mg-43d0 at the boundary where the incident happened. The unit tests in
// internal/workitem cover the decision; only an exec can show the two things
// that actually failed on 2026-08-11 — that the note reaches the writer's
// terminal, and that it goes to stderr where it cannot corrupt anything parsing
// stdout.

// editAs runs mg with MG_ACTOR set, which is the identity work.edited records.
// Every agent on this box shares one unix user, so the actor is the only thing
// that separates mayor from pm-pogo.
func editAs(t *testing.T, bin string, env []string, agent string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append([]string{}, env...), "MG_ACTOR="+agent)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg %s (as %s) failed: %v\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), agent, err, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

// seedTouchedItem builds a workspace holding one item, created by pm-pogo.
func seedTouchedItem(t *testing.T) (bin string, env []string, id string) {
	t.Helper()
	tmpHome := t.TempDir()
	bin = buildBinary(t)
	env = append(os.Environ(), "HOME="+tmpHome, "POGO_AGENT_NAME=")

	editAs(t, bin, env, "pm-pogo", "init")
	out, _ := editAs(t, bin, env, "pm-pogo", "new", "--title=an item two agents will edit")
	id = strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")
	return bin, env, id
}

// THE INCIDENT. pm-pogo hands the item back; the mayor's next edit is told so,
// by name and by age, instead of finding an unexplained value and filing it as
// data corruption.
func TestCLI_EditNamesTheOtherWriter(t *testing.T) {
	bin, env, id := seedTouchedItem(t)

	editAs(t, bin, env, "pm-pogo", "edit", id, "--assignee=mayor")

	stdout, stderr := editAs(t, bin, env, "mayor", "edit", id, "--append-body", "## the mayor's reply")

	for _, want := range []string{"note:", id, "pm-pogo", "just now", "metadata", "not corruption"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	// stdout stays the machine-readable success line. A note on stdout would
	// break every caller that parses it.
	if strings.Contains(stdout, "last edited") {
		t.Errorf("the note leaked onto stdout:\n%s", stdout)
	}
	if !strings.HasPrefix(stdout, "Updated "+id) {
		t.Errorf("stdout = %q, want the usual success line", stdout)
	}
}

// Silence means "nobody else has written here since you did". An agent
// iterating on its own item — 71% of measured edits — must see nothing, or the
// note becomes wallpaper and fails on the one edit that matters.
func TestCLI_EditSaysNothingWhenYouAreTheLastWriter(t *testing.T) {
	bin, env, id := seedTouchedItem(t)

	editAs(t, bin, env, "mayor", "edit", id, "--assignee=mayor")
	_, stderr := editAs(t, bin, env, "mayor", "edit", id, "--priority=high")

	if strings.Contains(stderr, "last edited") {
		t.Errorf("mayor was warned about its own edit:\n%s", stderr)
	}
}

// A never-edited item has nobody to name. The note must not invent one, and
// must not fire on the item's own creation.
func TestCLI_EditSaysNothingOnAFirstEdit(t *testing.T) {
	bin, env, id := seedTouchedItem(t)

	_, stderr := editAs(t, bin, env, "mayor", "edit", id, "--priority=high")
	if strings.Contains(stderr, "last edited") {
		t.Errorf("a first edit produced a last-edited note:\n%s", stderr)
	}
}

// The note is advisory and must never be able to stop a write. A workspace
// whose events.jsonl cannot be read still edits.
func TestCLI_EditSucceedsWhenTheEventLogIsUnreadable(t *testing.T) {
	bin, env, id := seedTouchedItem(t)
	editAs(t, bin, env, "pm-pogo", "edit", id, "--assignee=mayor")

	var home string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	log := home + "/.macguffin/events.jsonl"
	if err := os.Chmod(log, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", log, err)
	}
	t.Cleanup(func() { os.Chmod(log, 0o644) })

	stdout, _ := editAs(t, bin, env, "mayor", "edit", id, "--priority=high")
	if !strings.HasPrefix(stdout, "Updated "+id) {
		t.Errorf("an unreadable event log blocked the edit: %q", stdout)
	}
}

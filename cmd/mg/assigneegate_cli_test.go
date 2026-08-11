package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The mg-5eee guards at the CLI boundary, where the exit code is the contract
// and where every caller in the fleet actually meets them. The unit tests in
// internal/workitem cover the decisions; these cover the two things only an
// exec can show — that the exit code a script branches on is the documented one,
// and that a refusal writes nothing to the item.

// seedGatedItem creates an item and returns (bin, env, id, homeDir).
func seedGatedItem(t *testing.T, assignee string) (string, []string, string) {
	t.Helper()
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("mg %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("init")
	out := run("new", "--title=gated item")
	id := strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")
	if assignee != "" {
		run("edit", id, "--assignee="+assignee)
	}
	return bin, env, id
}

func assigneeOf(t *testing.T, bin string, env []string, id string) string {
	t.Helper()
	cmd := exec.Command(bin, "show", id, "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, `"assignee"`); i >= 0 {
			rest := line[i+len(`"assignee"`):]
			rest = strings.TrimLeft(rest, ": ")
			rest = strings.TrimSuffix(strings.TrimSpace(rest), ",")
			return strings.Trim(rest, `"`)
		}
	}
	return ""
}

// TestCLI_EditRefusesNonGatingAssignee. Measured against the pre-fix binary,
// every one of these exited 0 and stored the value verbatim.
func TestCLI_EditRefusesNonGatingAssignee(t *testing.T) {
	bin, env, id := seedGatedItem(t, "blocked:pm-pogo")

	for _, typo := range []string{"blocekd:pm-pogo", "blocked-pm-pogo", "blocked:", "Blocked:pm-pogo"} {
		t.Run(typo, func(t *testing.T) {
			cmd := exec.Command(bin, "edit", id, "--assignee="+typo)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("mg edit --assignee=%s exited 0 and stored a hold that does not hold:\n%s", typo, out)
			}
			if code := cmd.ProcessState.ExitCode(); code != 2 {
				t.Errorf("exit code = %d, want 2 (usage)\n%s", code, out)
			}
			if !strings.Contains(string(out), "blocked:<agent>") {
				t.Errorf("refusal does not spell the vocabulary that works:\n%s", out)
			}
			if got := assigneeOf(t, bin, env, id); got != "blocked:pm-pogo" {
				t.Errorf("assignee = %q; the refusal should have left it alone", got)
			}
		})
	}
}

// TestCLI_EditIfAssignee is the mayor's sequence at the CLI: gate it, let
// another agent take it, then write to it while asserting the hold.
func TestCLI_EditIfAssignee(t *testing.T) {
	bin, env, id := seedGatedItem(t, "blocked:pm-pogo")

	// The append is allowed while the gate stands.
	cmd := exec.Command(bin, "edit", id, "--if-assignee=blocked:pm-pogo", "--append-body", "## still held")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("append with a satisfied precondition was refused: %v\n%s", err, out)
	}

	// pm-pogo hands it back, exactly as it did on mg-27d4.
	cmd = exec.Command(bin, "edit", id, "--assignee=mayor")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reassign failed: %v\n%s", err, out)
	}

	// The same append now refuses, loudly, with the conflict code.
	cmd = exec.Command(bin, "edit", id, "--if-assignee=blocked:pm-pogo", "--append-body", "## written believing it is held")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("append succeeded against a moved gate:\n%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 4 {
		t.Errorf("exit code = %d, want 4 (conflict)\n%s", code, out)
	}
	if !strings.Contains(string(out), "mayor") {
		t.Errorf("refusal does not name the value on disk:\n%s", out)
	}

	// Nothing was written.
	cmd = exec.Command(bin, "show", id, "--json")
	cmd.Env = env
	shown, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, shown)
	}
	if strings.Contains(string(shown), "written believing it is held") {
		t.Error("the refused append landed in the body anyway")
	}
}

// TestCLI_EditIfAssigneeAloneWritesNothing pins --if-assignee to the same shape
// --if-unchanged already has: carried as a field, not counted as one, so a bare
// precondition is refused rather than exiting 0. A caller who passes only a
// precondition believes they performed a check; exit 0 on a command that wrote
// nothing and compared nothing is the reading that must not be available.
func TestCLI_EditIfAssigneeAloneWritesNothing(t *testing.T) {
	bin, env, id := seedGatedItem(t, "human")

	cmd := exec.Command(bin, "edit", id, "--if-assignee=human")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a bare --if-assignee reported success:\n%s", out)
	}
	if !strings.Contains(string(out), "no fields specified") {
		t.Errorf("expected the no-fields refusal, got:\n%s", out)
	}
}

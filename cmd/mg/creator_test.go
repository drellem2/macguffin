package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mg-ddf4, end to end through the real binary.
//
// The unit tests in internal/workitem cover the same defect, but they call
// Create directly. The defect was MEASURED at the CLI — 2040 of 2041 items in
// ~/.macguffin read `creator: daniel` — and the filer's identity now comes from
// the process environment, which a unit test inside the package can only
// simulate. So the whole path runs here: argv → cobra → workitem.Create → the
// file on disk → `mg show --json`.
//
// The ticket's acceptance criterion is the shape of every positive test below:
// **a fix verified by filing one ticket proves nothing**, because one ticket is
// exactly the case where a constant field looks correct. Two agents, two items,
// asserted distinguishable.

const (
	cliFilerOne = "zzz-probe-filer-one"
	cliFilerTwo = "zzz-probe-filer-two"
)

// showCreator reads the creator off `mg show --json`, the shape a consumer
// actually reads, rather than off the frontmatter this test wrote.
func showCreator(t *testing.T, bin, root, id string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show %s --json: exit %d\n%s", id, code, out)
	}
	var item struct {
		Creator string `json:"creator"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &item); err != nil {
		t.Fatalf("mg show %s --json is not one JSON object: %v\n%s", id, err, out)
	}
	return item.Creator
}

// TestCLI_CreatorDistinguishesTwoAgents is the acceptance check verbatim: file
// two items as two different agents and demonstrate they are distinguishable.
func TestCLI_CreatorDistinguishesTwoAgents(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliFilerOne, "new", "task", "Filed by the first agent")
	if code != 0 {
		t.Fatalf("mg new as %s: exit %d\n%s", cliFilerOne, code, out)
	}
	firstID := extractID(t, out)

	out, code = mgAs(t, bin, root, cliFilerTwo, "new", "task", "Filed by the second agent")
	if code != 0 {
		t.Fatalf("mg new as %s: exit %d\n%s", cliFilerTwo, code, out)
	}
	secondID := extractID(t, out)

	firstCreator := showCreator(t, bin, root, firstID)
	secondCreator := showCreator(t, bin, root, secondID)

	if firstCreator != cliFilerOne {
		t.Errorf("%s: creator = %q, want %q", firstID, firstCreator, cliFilerOne)
	}
	if secondCreator != cliFilerTwo {
		t.Errorf("%s: creator = %q, want %q", secondID, secondCreator, cliFilerTwo)
	}
	if firstCreator == secondCreator {
		t.Fatalf("two agents filed two items and both read creator=%q: the field is still constant across the store", firstCreator)
	}
}

// TestCLI_CreatorIsRenderedByShow: the plain `mg show` view is where a human
// reads this field, and where both attribution errors of 2026-07-30 were made.
// The JSON assertion above does not cover it.
func TestCLI_CreatorIsRenderedByShow(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliFilerOne, "new", "task", "Rendered attribution")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	shown, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, shown)
	}
	if !strings.Contains(shown, "Creator:") || !strings.Contains(shown, cliFilerOne) {
		t.Errorf("mg show does not render creator=%q:\n%s", cliFilerOne, shown)
	}
}

// TestCLI_CreatorAndAuditActorAgree: the same invocation must name its caller
// identically in the item's creator and in the event log's actor. The two
// fields drifted apart because each was written separately against the same
// wrong intuition, a field apart; this pins them together at the CLI.
func TestCLI_CreatorAndAuditActorAgree(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgAs(t, bin, root, cliFilerOne, "new", "task", "One caller, two fields")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	if got := showCreator(t, bin, root, id); got != cliFilerOne {
		t.Fatalf("creator = %q, want %q", got, cliFilerOne)
	}

	// The same agent then acts on the item; the event it writes must name the
	// same caller the item's creator does.
	if out, code := mgAs(t, bin, root, cliFilerOne, "edit", id, "--priority=high"); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}
	events := readEventObjects(t, bin, root)
	if len(events) == 0 {
		t.Fatal("no events written")
	}
	last := events[len(events)-1]
	if last["actor"] != cliFilerOne {
		t.Errorf("audit actor = %q but creator = %q for the same caller", last["actor"], cliFilerOne)
	}
}

// TestCLI_CreatorFallsBackToUnixUserForAHuman: with neither MG_ACTOR nor
// POGO_AGENT_NAME set — a human at a terminal — the OS user is still the
// answer. The fix must not turn the human case into "unknown".
func TestCLI_CreatorFallsBackToUnixUserForAHuman(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "POGO_AGENT_NAME=") || strings.HasPrefix(kv, "MG_ACTOR=") {
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command(bin, "--root="+root, "new", "task", "Filed by a human at a terminal")
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new: %v\n%s", err, raw)
	}
	id := extractID(t, string(raw))

	creator := showCreator(t, bin, root, id)
	if creator == "" || creator == "unknown" {
		t.Errorf("creator = %q for a human filer; want the OS user", creator)
	}
}

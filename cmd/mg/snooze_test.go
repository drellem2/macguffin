package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// snoozeEnv builds an isolated store and returns the binary plus the env that
// points at it, along with the store root.
func snoozeEnv(t *testing.T) (bin string, env []string, root string) {
	t.Helper()
	home := t.TempDir()
	bin = buildBinary(t)
	env = append(os.Environ(), "HOME="+home)
	root = filepath.Join(home, ".macguffin")

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init: %v\n%s", err, out)
	}
	return bin, env, root
}

// snzRun runs mg and returns combined output plus the exit code (0 on success).
func snzRun(t *testing.T, bin string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("mg %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// snzNewItem files an item and returns its id.
func snzNewItem(t *testing.T, bin string, env []string, title string) string {
	t.Helper()
	out, code := snzRun(t, bin, env, "new", title)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	return strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")
}

// driveTheSweep runs `mg schedule` once, which is what stamps work/.last-sweep
// and satisfies the driver check.
func driveTheSweep(t *testing.T, bin string, env []string) string {
	t.Helper()
	out, code := snzRun(t, bin, env, "schedule")
	if code != 0 {
		t.Fatalf("mg schedule: exit %d\n%s", code, out)
	}
	return out
}

// TestCLI_SnoozeNeedsNoDriver is the INVERSION of the test that used to live
// here, and the inversion is the point of mg-ad63.
//
// `mg snooze` used to refuse on a store where nothing had run `mg schedule`
// recently, because a snooze was only a gate and the sweep was the only thing
// that opened it. Promotion is now a property of the binary — any mg invocation
// opens an elapsed gate — so that premise is false, and the refusal would fire
// hardest in exactly the situation the feature was built for: a fleet that has
// lost its sweep cron.
//
// So the assertion is now that a snooze on a driverless store SUCCEEDS, and
// that nothing in the confirmation promises a sweep will be what wakes it.
func TestCLI_SnoozeNeedsNoDriver(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Revisit the pricing page")

	// Deliberately no driveTheSweep: work/.last-sweep does not exist at all,
	// which is the strongest form of "nothing is driving the sweep".
	if _, err := os.Stat(filepath.Join(root, "work", ".last-sweep")); !os.IsNotExist(err) {
		t.Fatalf("this test requires a store with no sweep stamp; Stat says: %v", err)
	}

	out, code := snzRun(t, bin, env, "snooze", id, "--for", "3d")
	if code != 0 {
		t.Fatalf("mg snooze refused on a driverless store; promotion no longer needs a driver: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Snoozed "+id) {
		t.Errorf("expected a Snoozed confirmation, got:\n%s", out)
	}
	if strings.Contains(out, "nothing is driving") {
		t.Errorf("the driver refusal is gone; it must not reappear as a warning:\n%s", out)
	}
	if strings.Contains(out, "`mg schedule` sweep") {
		t.Errorf("the confirmation must not promise the sweep is what wakes it:\n%s", out)
	}

	// And the gate really is on disk, in pending/, where the promoter looks.
	if _, err := os.Stat(filepath.Join(root, "work", "pending", id+".md")); err != nil {
		t.Errorf("expected pending/%s.md: %v", id, err)
	}
}

// --force survives as an accepted no-op. Removing the flag would turn every
// script that passes it into an exit-2 unknown-flag failure; keeping it and
// saying it is deprecated costs a line and breaks nobody.
func TestCLI_SnoozeForceIsADeprecatedNoOp(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	id := snzNewItem(t, bin, env, "Forced")

	out, code := snzRun(t, bin, env, "snooze", id, "--for", "3d", "--force")
	if code != 0 {
		t.Fatalf("mg snooze --force: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "deprecated") {
		t.Errorf("--force must say it is deprecated rather than silently doing nothing, got:\n%s", out)
	}
	if !strings.Contains(out, "Snoozed "+id) {
		t.Errorf("--force must still snooze, got:\n%s", out)
	}
}

// The swallower case, constructed: a wake time that has already passed, and a
// wake time mg cannot parse, both fail AT SNOOZE TIME rather than sitting
// quietly in pending/.
func TestCLI_SnoozeRefusesPastAndMalformedTimes(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Gate me")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"past absolute", []string{"--until", "2020-01-02T03:04:05Z"}, "has already passed"},
		{"past date", []string{"--until", "2020-01-02"}, "has already passed"},
		{"unparseable", []string{"--until", "next tuesday"}, "not a time I recognise"},
		{"unparseable duration", []string{"--for", "tomorrow"}, "not a duration"},
		{"negative duration", []string{"--for", "-3h"}, "not a"},
		{"both flags", []string{"--until", "2030-01-01", "--for", "3d"}, "cannot use both"},
		{"neither flag", nil, "a wake time is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"snooze", id}, c.args...)
			out, code := snzRun(t, bin, env, args...)
			if code == 0 {
				t.Fatalf("mg snooze %v succeeded; it must refuse\n%s", c.args, out)
			}
			if code != 2 {
				t.Errorf("exit code = %d, want 2 (usage)\n%s", code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("refusal does not say %q:\n%s", c.want, out)
			}
			// Every refusal leaves the item exactly where it was.
			showOut, _ := snzRun(t, bin, env, "show", id)
			if strings.Contains(showOut, "Snooze:") {
				t.Errorf("a refused snooze wrote an attribute anyway:\n%s", showOut)
			}
		})
	}
}

// TestCLI_SnoozeWakesAfterDriverDowntime is the acceptance case at the CLI
// boundary. The wake time is moved into the past on disk rather than waited
// for; the store is then in exactly the state it would be in if pogod had been
// down through the wake instant, and ONE sweep must recover it.
func TestCLI_SnoozeWakesAfterDriverDowntime(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Come back to this")

	if out, code := snzRun(t, bin, env, "snooze", id, "--for", "3d"); code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}

	pendingPath := filepath.Join(root, "work", "pending", id+".md")
	if _, err := os.Stat(pendingPath); err != nil {
		t.Fatalf("expected pending/%s.md: %v", id, err)
	}

	// A sweep before the wake time must NOT promote, and must report the item
	// as waiting — the population has to be auditable.
	out := driveTheSweep(t, bin, env)
	if strings.Contains(out, "Promoted "+id) {
		t.Fatalf("swept an item before its wake time:\n%s", out)
	}
	if !strings.Contains(out, "snoozed:") || !strings.Contains(out, id) {
		t.Errorf("`mg schedule` must list what is still snoozed, got:\n%s", out)
	}

	// Simulate the interval elapsing with nothing running: rewrite the gate to
	// a time well in the past and sweep exactly once.
	rewriteSnooze(t, pendingPath, time.Now().UTC().Add(-72*time.Hour).Format(time.RFC3339))

	out = driveTheSweep(t, bin, env)
	if !strings.Contains(out, "Promoted "+id) {
		t.Fatalf("one late sweep must recover the item, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", id+".md")); err != nil {
		t.Errorf("expected available/%s.md after the late sweep: %v", id, err)
	}

	showOut, _ := snzRun(t, bin, env, "show", id)
	if strings.Contains(showOut, "Snooze:") {
		t.Errorf("a spent gate must be cleared on promotion:\n%s", showOut)
	}
}

// A `snooze:` value that reaches disk unparseable (only a hand-edit can produce
// one) holds the item AND is named by the sweep. Held-and-silent is the
// swallow; held-and-named is the fix.
func TestCLI_MalformedSnoozeOnDiskIsHeldAndNamed(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Hand-edited gate")

	if out, code := snzRun(t, bin, env, "snooze", id, "--for", "3d"); code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}
	pendingPath := filepath.Join(root, "work", "pending", id+".md")
	rewriteSnooze(t, pendingPath, "next tuesday")

	out := driveTheSweep(t, bin, env)
	if strings.Contains(out, "Promoted "+id) {
		t.Fatalf("an unparseable gate must not release the item:\n%s", out)
	}
	if !strings.Contains(out, "can never be promoted") || !strings.Contains(out, "next tuesday") {
		t.Errorf("the sweep must name the bad value, got:\n%s", out)
	}

	showOut, _ := snzRun(t, bin, env, "show", id)
	if !strings.Contains(showOut, "never open") {
		t.Errorf("`mg show` must flag a gate that can never open, got:\n%s", showOut)
	}
}

// A pending item with no snooze must be untouched by any of this: same
// placement, same promotion, no attribute, and it must not appear in the
// snoozed report.
func TestCLI_PendingWithoutSnoozeIsUnchanged(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	driveTheSweep(t, bin, env)

	parent := snzNewItem(t, bin, env, "Phase 1")
	out, code := snzRun(t, bin, env, "new", "--depends="+parent, "Phase 2")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	child := strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")

	sweep := driveTheSweep(t, bin, env)
	if strings.Contains(sweep, "snoozed:") {
		t.Errorf("an un-snoozed pending item must not appear in the snoozed report:\n%s", sweep)
	}
	if strings.Contains(sweep, "Promoted "+child) {
		t.Errorf("promoted a dependent whose parent is unfinished:\n%s", sweep)
	}

	if out, code := snzRun(t, bin, env, "claim", parent); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	out, code = snzRun(t, bin, env, "done", parent)
	if code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Promoted "+child) {
		t.Errorf("done must still auto-promote the dependent, got:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(root, "work", "available", child+".md"))
	if err != nil {
		t.Fatalf("reading the promoted child: %v", err)
	}
	if strings.Contains(string(data), "snooze:") {
		t.Errorf("an item that was never snoozed must carry no snooze line:\n%s", data)
	}
}

func TestCLI_UnsnoozeLiftsTheGate(t *testing.T) {
	bin, env, root := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Changed my mind")

	if out, code := snzRun(t, bin, env, "snooze", id, "--for", "2w"); code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}
	out, code := snzRun(t, bin, env, "unsnooze", id)
	if code != 0 {
		t.Fatalf("mg unsnooze: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("unsnooze must say where the item went, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", id+".md")); err != nil {
		t.Errorf("expected available/%s.md: %v", id, err)
	}

	// Lifting a gate that is not there is a refusal, not a silent success.
	if out, code := snzRun(t, bin, env, "unsnooze", id); code == 0 {
		t.Errorf("unsnoozing an unsnoozed item must refuse, got:\n%s", out)
	}
}

// The `snooze` field is additive on the frozen --json contract, and it carries
// the value verbatim so a consumer sees what is on disk.
func TestCLI_SnoozeAppearsInJSON(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Machine readable")

	if out, code := snzRun(t, bin, env, "snooze", id, "--until", "2030-06-01T09:00:00Z"); code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}

	out, code := snzRun(t, bin, env, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshalling show --json: %v\n%s", err, out)
	}
	if got := doc["snooze"]; got != "2030-06-01T09:00:00Z" {
		t.Errorf("snooze = %v, want the stored RFC3339 value", got)
	}

	// An item with no snooze reports an empty string, never a missing key.
	other := snzNewItem(t, bin, env, "No gate")
	out, code = snzRun(t, bin, env, "show", other, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, out)
	}
	doc = map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshalling show --json: %v\n%s", err, out)
	}
	v, ok := doc["snooze"]
	if !ok {
		t.Error("the snooze key must always be present in --json output")
	}
	if v != "" {
		t.Errorf("snooze = %v on an un-snoozed item, want an empty string", v)
	}
}

// A local wall-clock input is echoed back as the absolute UTC instant it
// resolved to, so the operator can see what was actually recorded.
func TestCLI_SnoozeEchoesTheResolvedInstant(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	id := snzNewItem(t, bin, env, "Local input")

	out, code := snzRun(t, bin, env, "snooze", id, "--until", "2030-06-01 14:30")
	if code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}
	// Whatever the host zone is, the echoed value is RFC3339 UTC.
	if !strings.Contains(out, "2030-06-01T") || !strings.Contains(out, "Z") {
		t.Errorf("expected an RFC3339 UTC instant echoed back, got:\n%s", out)
	}
}

// rewriteSnooze replaces the `snooze:` frontmatter value in place, which is how
// "time passed while nothing ran" and "somebody hand-edited the gate" are both
// reproduced without sleeping.
func rewriteSnooze(t *testing.T, path, value string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, "snooze: ") {
			lines[i] = "snooze: " + value
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no snooze: line in %s:\n%s", path, data)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

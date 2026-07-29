package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// longTitle is deliberately wider than any sane terminal.
const longTitle = "mg list runs off the right-hand edge of the screen on any terminal narrower than the longest title in the store"

var longTags = []string{"cli", "ergonomics", "listing", "needs-review"}

// listTruncFixture files one item with a long title, four tags, an assignee and
// a live snooze, and returns the binary, the env and the item's id and wake
// time as written on disk.
func listTruncFixture(t *testing.T) (bin string, env []string, id, snoozeRaw string) {
	t.Helper()
	bin, env, _ = snoozeEnv(t)

	id = snzNewItem(t, bin, env, longTitle)
	if out, code := snzRun(t, bin, env, "edit", id, "--tags="+strings.Join(longTags, ",")); code != 0 {
		t.Fatalf("mg edit --tags: exit %d\n%s", code, out)
	}
	if out, code := snzRun(t, bin, env, "assign", id, "bob"); code != 0 {
		t.Fatalf("mg assign: exit %d\n%s", code, out)
	}
	driveTheSweep(t, bin, env)
	out, code := snzRun(t, bin, env, "snooze", id, "--for", "3d")
	if code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}
	// "Snoozed mg-1234 until 2026-08-01T12:00:00Z (in 3 days): ..."
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "until" && i+1 < len(fields) {
			snoozeRaw = fields[i+1]
			break
		}
	}
	if snoozeRaw == "" {
		t.Fatalf("could not read the wake time back out of: %q", out)
	}
	return bin, env, id, snoozeRaw
}

// itemLine returns the `mg list` line for id, or fails.
func itemLine(t *testing.T, out, id string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}
	t.Fatalf("no line for %s in:\n%s", id, out)
	return ""
}

// TestCLI_ListPipedOutputIsUntouched is the POSITIVE control the whole feature
// hangs on. `mg list` into a pipe is what `grep`, `awk` and every script in the
// fleet see, and it must be byte-for-byte what it has always been. A test that
// only checks the truncated form passes on an implementation that truncates
// unconditionally and quietly corrupts all of them.
func TestCLI_ListPipedOutputIsUntouched(t *testing.T) {
	bin, env, id, snoozeRaw := listTruncFixture(t)

	// exec.Command gives the child a pipe, not a terminal — exactly the case.
	out, code := snzRun(t, bin, env, "list")
	if code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, out)
	}

	want := fmt.Sprintf("  %-10s %-8s %s \033[2m[%s]\033[0m \033[2mbob\033[0m \033[2m[snoozed %s]\033[0m",
		id, "task", longTitle, strings.Join(longTags, ", "), snoozeRaw)
	if got := itemLine(t, out, id); got != want {
		t.Errorf("piped `mg list` line changed.\n got %q\nwant %q", got, want)
	}
	if strings.Contains(out, truncMarker) {
		t.Errorf("piped `mg list` truncated something:\n%s", out)
	}

	// --status takes the other of the two print paths; pin it too (no indent).
	statusOut, code := snzRun(t, bin, env, "list", "--status=pending")
	if code != 0 {
		t.Fatalf("mg list --status=pending: exit %d\n%s", code, statusOut)
	}
	wantStatus := strings.TrimPrefix(want, "  ")
	if got := itemLine(t, statusOut, id); got != wantStatus {
		t.Errorf("piped `mg list --status=pending` line changed.\n got %q\nwant %q", got, wantStatus)
	}
}

// TestCLI_ListJSONIsUnaffected pins that the machine-readable path never sees
// the line fitter, --wide or no --wide.
func TestCLI_ListJSONIsUnaffected(t *testing.T) {
	bin, env, id, snoozeRaw := listTruncFixture(t)

	out, code := snzRun(t, bin, env, "list", "--json")
	if code != 0 {
		t.Fatalf("mg list --json: exit %d\n%s", code, out)
	}
	wide, code := snzRun(t, bin, env, "list", "--json", "--wide")
	if code != 0 {
		t.Fatalf("mg list --json --wide: exit %d\n%s", code, wide)
	}
	if out != wide {
		t.Errorf("--wide changed --json output:\n%q\nvs\n%q", out, wide)
	}
	if strings.Contains(out, truncMarker) {
		t.Errorf("--json output carries a truncation marker:\n%s", out)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var item listJSONItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("unmarshalling %q: %v", line, err)
		}
		if item.ID != id {
			continue
		}
		found = true
		if item.Title != longTitle {
			t.Errorf("--json title = %q, want the full %q", item.Title, longTitle)
		}
		if strings.Join(item.Tags, ",") != strings.Join(longTags, ",") {
			t.Errorf("--json tags = %v, want %v", item.Tags, longTags)
		}
		if item.Assignee != "bob" {
			t.Errorf("--json assignee = %q, want %q", item.Assignee, "bob")
		}
		if item.Snooze != snoozeRaw {
			t.Errorf("--json snooze = %q, want %q", item.Snooze, snoozeRaw)
		}
	}
	if !found {
		t.Errorf("%s missing from --json output:\n%s", id, out)
	}
}

// TestCLI_ListWideFlagsAreAccepted covers the opt-out spellings end to end.
func TestCLI_ListWideFlagsAreAccepted(t *testing.T) {
	bin, env, id, _ := listTruncFixture(t)
	plain, code := snzRun(t, bin, env, "list")
	if code != 0 {
		t.Fatalf("mg list: exit %d\n%s", code, plain)
	}
	for _, flag := range []string{"--wide", "--no-truncate"} {
		out, code := snzRun(t, bin, env, "list", flag)
		if code != 0 {
			t.Fatalf("mg list %s: exit %d\n%s", flag, code, out)
		}
		if out != plain {
			t.Errorf("mg list %s differs from piped `mg list`:\n%q\nvs\n%q", flag, out, plain)
		}
		if !strings.Contains(itemLine(t, out, id), longTitle) {
			t.Errorf("mg list %s shortened the title:\n%s", flag, out)
		}
	}
}

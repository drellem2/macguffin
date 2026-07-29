package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage for mg-f326, driven through the real binary. The
// package-level tests in internal/workitem prove the mechanism; these prove the
// CLI actually wires it up — the flags exist, the exit code reaches the shell,
// and the hash a caller can obtain is the hash the guard accepts. A caller who
// followed the help text and still got clobbered would be this ticket again.

// luSeed creates an item with a known body and returns its ID.
func luSeed(t *testing.T, bin string, env []string, dir, title, body string) string {
	t.Helper()
	id := emNew(t, bin, env, title)
	path := writeBodyFile(t, dir, "seed.md", body)
	emOK(t, bin, env, "edit", id, "--body-file", path)
	return id
}

// TestCLI_IfUnchangedRefusesConcurrentFullBodyWrite is mg-f326's acceptance
// criterion at the CLI seam: the interleaving (read A, write B, write A) must
// leave A's write REFUSED with a non-zero exit, not accepted with exit 0.
//
// Everything it asserts is the opposite of what the tool did before: the write
// fails, the exit code is 4, stderr names the change, and B's body survives.
func TestCLI_IfUnchangedRefusesConcurrentFullBodyWrite(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	base := "# Contested\n\n## original\n\nthe body both agents read.\n"
	id := luSeed(t, bin, env, dir, "Contested", base)

	// A reads and captures the version it is holding.
	hashA := strings.TrimSpace(emOK(t, bin, env, "show", id, "--body-hash"))
	if len(hashA) != 64 {
		t.Fatalf("--body-hash should print a bare sha256, got %q", hashA)
	}

	// B writes in between, unguarded — the historical call shape.
	bPath := writeBodyFile(t, dir, "b.md", base+"\n## B's reconciliation\n\neighty-five lines.\n")
	emOK(t, bin, env, "edit", id, "--body-file", bPath)

	// A writes the whole body it composed before B existed.
	aPath := writeBodyFile(t, dir, "a.md", base+"\n## A's section\n\ncomposed from the older read.\n")
	stdout, stderr, exit := taxRun(bin, env, "edit", id, "--if-unchanged="+hashA, "--body-file", aPath)

	if exit == 0 {
		t.Fatalf("stale full-body write exited 0 — the guard did not reach the CLI.\n%s%s", stdout, stderr)
	}
	if exit != 4 {
		t.Errorf("exit = %d, want 4 (conflict); stderr:\n%s", exit, stderr)
	}
	// The failure must name what it can: the item, the hash the caller passed,
	// and that a size is now different.
	for _, want := range []string{id, hashA, "changed since you read it", "lines"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal must mention %q, got stderr:\n%s", want, stderr)
		}
	}
	// It must also point at the way out, including the append shape.
	if !strings.Contains(stderr, "--append-body-file") {
		t.Errorf("refusal should offer the append escape hatch, got stderr:\n%s", stderr)
	}

	// B's work survives on disk.
	shown := emOK(t, bin, env, "show", id)
	if !strings.Contains(shown, "B's reconciliation") {
		t.Errorf("B's work was destroyed by a refused write:\n%s", shown)
	}
	if strings.Contains(shown, "A's section") {
		t.Errorf("a refused write was partially applied:\n%s", shown)
	}
}

// TestCLI_UnguardedFullBodyWriteStillClobbers is the CLI-level control. Same
// interleaving, no --if-unchanged, and the clobber must still happen with exit
// 0 — because mg self-installs across the fleet on merge, and turning the
// most-used write path into a refusing one by default is a decision that has to
// be made on purpose, not inherited from a flag someone added.
func TestCLI_UnguardedFullBodyWriteStillClobbers(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	base := "# Contested\n\n## original\n\nthe body both agents read.\n"
	id := luSeed(t, bin, env, dir, "Contested", base)

	bPath := writeBodyFile(t, dir, "b.md", base+"\n## B's reconciliation\n\neighty-five lines.\n")
	emOK(t, bin, env, "edit", id, "--body-file", bPath)

	aPath := writeBodyFile(t, dir, "a.md", base+"\n## A's section\n\ncomposed from the older read.\n")
	emOK(t, bin, env, "edit", id, "--body-file", aPath)

	shown := emOK(t, bin, env, "show", id)
	if strings.Contains(shown, "B's reconciliation") {
		t.Error("a bare --body-file no longer clobbers: either the guard became default-on " +
			"(a fleet-wide change that must be deliberate and separately shipped) or this " +
			"test stopped exercising the interleaving")
	}
}

// TestCLI_AppendBodyFileSurvivesInterleaving is shape 2 — the one that would
// have prevented all three of the night's incidents without any guard, any
// coordination, or any change to how the agents were working. Both agents
// append, neither has seen the other, and both sections are on disk afterwards.
func TestCLI_AppendBodyFileSurvivesInterleaving(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	id := luSeed(t, bin, env, dir, "Shared", "# Shared\n\n## original\n\nshared content.\n")

	bPath := writeBodyFile(t, dir, "b.md", "## B's section\n\nB's analysis.\n")
	emOK(t, bin, env, "edit", id, "--append-body-file", bPath)

	aPath := writeBodyFile(t, dir, "a.md", "## A's section\n\nA's analysis, written blind.\n")
	emOK(t, bin, env, "edit", id, "--append-body-file", aPath)

	shown := emOK(t, bin, env, "show", id)
	for _, want := range []string{"## original", "B's section", "A's section"} {
		if !strings.Contains(shown, want) {
			t.Errorf("append lost %q:\n%s", want, shown)
		}
	}
	if strings.Index(shown, "A's section") < strings.Index(shown, "B's section") {
		t.Errorf("appends must accumulate in write order:\n%s", shown)
	}
}

// TestCLI_AppendBodyFileVerbatimThroughShell puts the append path through a REAL
// shell with a quoted heredoc, which is the form the help text tells callers to
// use. The append flag reads its text with the same verbatim reader as
// --body-file, so the safer write path must not quietly reintroduce the shell
// expansion hazard that --body-file exists to remove (mg-7850).
func TestCLI_AppendBodyFileVerbatimThroughShell(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	id := emNew(t, bin, env, "Append hazard")

	line := bin + " edit " + id + " --append-body-file - <<'EOF'\n" + hazardBody + "EOF\n"
	stdout, stderr, err := runSh(t, env, line)
	if err != nil {
		t.Fatalf("append via heredoc failed: %v\n%s\n%s", err, stdout, stderr)
	}

	got := showBodyJSON(t, bin, env, id)
	if !strings.Contains(got, hazardBody) {
		t.Errorf("--append-body-file must store the heredoc's bytes verbatim.\nwant substring: %q\n got body: %q", hazardBody, got)
	}
}

// TestCLI_BodyHashAgreesWithJSON pins the contract that makes the guard usable:
// the bare hash from --body-hash is byte-identical to the body_hash --json
// carries, and both are the SHA-256 of the body --json returns. A caller who
// derived the hash either way must be able to write.
func TestCLI_BodyHashAgreesWithJSON(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	id := luSeed(t, bin, env, dir, "Hashes", "# Hashes\n\nbody with `metachars` and $VARS.\n")

	bare := strings.TrimSpace(emOK(t, bin, env, "show", id, "--body-hash"))

	var doc struct {
		Body     string `json:"body"`
		BodyHash string `json:"body_hash"`
	}
	if err := json.Unmarshal([]byte(emOK(t, bin, env, "show", id, "--json")), &doc); err != nil {
		t.Fatalf("decoding show --json: %v", err)
	}
	if doc.BodyHash != bare {
		t.Errorf("--body-hash (%s) and --json body_hash (%s) disagree", bare, doc.BodyHash)
	}

	// And the hash a caller obtains actually unlocks a write.
	newPath := writeBodyFile(t, dir, "new.md", "# Hashes\n\nrewritten under guard.\n")
	emOK(t, bin, env, "edit", id, "--if-unchanged="+doc.BodyHash, "--body-file", newPath)
	if !strings.Contains(emOK(t, bin, env, "show", id), "rewritten under guard") {
		t.Error("a write guarded by the hash mg itself reported was refused")
	}
}

// TestCLI_EditReportsBodyDelta pins the instrument added for the second-order
// damage. In incident 1 a body went 227 → 113 lines and the writer was told
// only "Updated"; the loss surfaced seven minutes later by a re-read and a
// grep. The size now rides on the write itself.
func TestCLI_EditReportsBodyDelta(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	id := luSeed(t, bin, env, dir, "Sized", "# Sized\n\none\ntwo\nthree\nfour\nfive\n")

	shrunk := writeBodyFile(t, dir, "small.md", "# Sized\n")
	out := emOK(t, bin, env, "edit", id, "--body-file", shrunk)
	if !strings.Contains(out, "→") || !strings.Contains(out, "lines") {
		t.Errorf("a body-changing edit must report the size delta, got: %q", out)
	}

	// A frontmatter-only edit leaves the body alone, so it must stay quiet
	// rather than printing a 0 → 0 delta on every --tags change.
	out = emOK(t, bin, env, "edit", id, "--priority=high")
	if strings.Contains(out, "lines") {
		t.Errorf("an edit that did not touch the body must not report a body delta, got: %q", out)
	}
}

// TestCLI_EditEmitsBodyEditEvent pins the durable record. mg-f326's deepest
// complaint was that after a known clobber `grep -c` returns the same zero for
// a deliberate deletion and a destroyed one, so every genuine absence in the
// blast radius reads as damage. The event does not recover bytes; it makes the
// two distinguishable after the fact.
func TestCLI_EditEmitsBodyEditEvent(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	id := luSeed(t, bin, env, dir, "Recorded", "# Recorded\n\nalpha\nbravo\ncharlie\n")
	before := strings.TrimSpace(emOK(t, bin, env, "show", id, "--body-hash"))

	shrunk := writeBodyFile(t, dir, "small.md", "# Recorded\n")
	emOK(t, bin, env, "edit", id, "--body-file", shrunk)

	data, err := os.ReadFile(filepath.Join(home, ".macguffin", "events.jsonl"))
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}

	var found map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e map[string]string
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e["type"] == "work.edited" && e["item_id"] == id && e["body_hash_before"] == before {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("no work.edited event recording the pre-write hash %s:\n%s", before, data)
	}
	if found["mode"] != "replace" {
		t.Errorf("mode = %q, want %q", found["mode"], "replace")
	}
	if found["guarded"] != "false" {
		t.Errorf("guarded = %q, want %q for an unguarded --body-file write", found["guarded"], "false")
	}
	if found["lines_before"] == found["lines_after"] {
		t.Errorf("event must record the size on both sides, got before=%q after=%q",
			found["lines_before"], found["lines_after"])
	}
}

// TestCLI_EditBodyFlagConflicts pins that a caller who asks for both a
// replacement and an append is refused rather than silently given one of them —
// they would not find out which until the next read.
func TestCLI_EditBodyFlagConflicts(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	dir := t.TempDir()

	id := luSeed(t, bin, env, dir, "Conflicts", "# Conflicts\n\noriginal.\n")
	path := writeBodyFile(t, dir, "x.md", "text\n")

	cases := [][]string{
		{"edit", id, "--body=a", "--append-body=b"},
		{"edit", id, "--body-file", path, "--append-body-file", path},
		{"edit", id, "--append-body=a", "--append-body-file", path},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			_, stderr, exit := taxRun(bin, env, args...)
			if exit != 2 {
				t.Errorf("exit = %d, want 2 (usage); stderr:\n%s", exit, stderr)
			}
			if !strings.Contains(emOK(t, bin, env, "show", id), "original") {
				t.Error("a refused flag combination must not have written anything")
			}
		})
	}
}

// TestCLI_ShowBodyHashRejectsJSON pins that the two output modes cannot be
// combined into something that is neither.
func TestCLI_ShowBodyHashRejectsJSON(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	id := emNew(t, bin, env, "Both flags")
	_, stderr, exit := taxRun(bin, env, "show", id, "--json", "--body-hash")
	if exit != 2 {
		t.Errorf("exit = %d, want 2 (usage); stderr:\n%s", exit, stderr)
	}
}

// showBodyJSON returns an item's stored body via --json, which round-trips the
// bytes without the human formatter in the way.
func showBodyJSON(t *testing.T, bin string, env []string, id string) string {
	t.Helper()
	var doc struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(emOK(t, bin, env, "show", id, "--json")), &doc); err != nil {
		t.Fatalf("decoding show --json: %v", err)
	}
	return doc.Body
}

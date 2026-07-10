package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the end-to-end contract for the resolver's shadow notice
// (mg-fb07). A short ID that names one LIVE work item and one ARCHIVED record
// resolves to the live item — and mg says so, on STDERR.
//
// STDERR is the whole contract. pogo drives this binary and parses
// `mg show --json` / `mg list --json` off STDOUT. A note on stdout is not a
// cosmetic wart; it is a wire-format break that would make every JSON consumer
// fail to unmarshal. These tests assert the stream, not just the text.

// shadowStore seeds a live item under a fresh HOME and plants an archived twin
// sharing its ID in work/archive/2026-04/. It returns the env and the ID.
func shadowStore(t *testing.T, bin string) (env []string, home, id string) {
	t.Helper()
	home = t.TempDir()
	env = emEnv(home)
	emInit(t, bin, env)
	id = emNew(t, bin, env, "shadowed by an archived twin")

	archive := filepath.Join(home, ".macguffin", "work", "archive", "2026-04")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(home, ".macguffin", "work", "available", id+".md")
	body, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("reading the live item: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archive, id+".md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return env, home, id
}

// wantArchivePath is the store-relative path the note must name, verbatim.
func wantArchivePath(id string) string {
	return filepath.Join("work", "archive", "2026-04", id+".md")
}

// TestCLI_ShadowNotice_ShowResolvesAndWarnsOnStderr is the mg-4fa7 acceptance
// case: `mg show <shadowed-id>` exits 0, prints the LIVE item on stdout, and
// names the archived twin's path on stderr.
func TestCLI_ShadowNotice_ShowResolvesAndWarnsOnStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, _, id := shadowStore(t, bin)

	stdout, stderr, exit := taxRun(bin, env, "show", id)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "Status:    available") {
		t.Errorf("show did not render the LIVE item:\n%s", stdout)
	}
	if !strings.Contains(stderr, wantArchivePath(id)) {
		t.Errorf("stderr does not name the archived twin's path %q:\n%q", wantArchivePath(id), stderr)
	}
	if !strings.Contains(stderr, id) {
		t.Errorf("stderr note does not name the id: %q", stderr)
	}
	// The note is a note, not an error: it must not leak onto stdout.
	if strings.Contains(stdout, "note:") || strings.Contains(stdout, wantArchivePath(id)) {
		t.Errorf("the shadow note leaked onto STDOUT:\n%s", stdout)
	}
}

// TestCLI_ShadowNotice_JSONStdoutStaysParseable is the sharpest way to get this
// wrong. `mg show --json` must emit exactly one JSON object on stdout with the
// note diverted to stderr, or every pogo consumer breaks.
func TestCLI_ShadowNotice_JSONStdoutStaysParseable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, _, id := shadowStore(t, bin)

	stdout, stderr, exit := taxRun(bin, env, "show", id, "--json")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", exit, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a single JSON object — the note corrupted it: %v\n%s", err, stdout)
	}
	if got["id"] != id {
		t.Errorf("json id = %v, want %s", got["id"], id)
	}
	if got["status"] != "available" {
		t.Errorf("json status = %v, want available (the LIVE item)", got["status"])
	}
	if !strings.Contains(stderr, wantArchivePath(id)) {
		t.Errorf("stderr does not name the archived twin: %q", stderr)
	}

	// `mg list --json` shares the same stdout contract. It emits NDJSON: one
	// object per line, so a stray note would break exactly one record.
	stdout, _, exit = taxRun(bin, env, "list", "--json")
	if exit != 0 {
		t.Fatalf("list exit = %d, want 0", exit)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("mg list --json emitted a non-JSON line: %v\n%q", err, line)
		}
	}
}

// TestCLI_ShadowNotice_ClaimSucceeds is the other half of acceptance: the
// shadowed ID must be claimable, which is what actually unblocks mg-4fa7.
func TestCLI_ShadowNotice_ClaimSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, _, id := shadowStore(t, bin)

	stdout, stderr, exit := taxRun(bin, env, "claim", id)
	if exit != 0 {
		t.Fatalf("claim exit = %d, want 0\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("claim stdout does not confirm the id: %q", stdout)
	}
	if !strings.Contains(stderr, wantArchivePath(id)) {
		t.Errorf("claim resolved the shadow silently: %q", stderr)
	}

	if out, _, exit := taxRun(bin, env, "show", id); exit != 0 || !strings.Contains(out, "Status:    claimed") {
		t.Errorf("after claim, show = exit %d:\n%s", exit, out)
	}
}

// TestCLI_ShadowNotice_UnshadowedIsSilent: the note is a shadow warning, not a
// resolve trace. An ordinary `mg show` must not print anything on stderr —
// otherwise every scripted caller that checks stderr-empty starts failing.
func TestCLI_ShadowNotice_UnshadowedIsSilent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	id := emNew(t, bin, env, "a perfectly ordinary item")

	_, stderr, exit := taxRun(bin, env, "show", id)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("unshadowed `mg show` wrote to stderr: %q", stderr)
	}
}

// TestCLI_ShadowNotice_AmbiguityStillErrors: liveness breaks a live-vs-archived
// tie, never an archived-vs-archived one. Two archived twins and no live item
// stay ambiguous — exit 4, both paths named, nothing on stdout.
func TestCLI_ShadowNotice_AmbiguityStillErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, home, id := shadowStore(t, bin)

	// Move the live copy into a second archive partition: now 0 live, 2 archived.
	live := filepath.Join(home, ".macguffin", "work", "available", id+".md")
	second := filepath.Join(home, ".macguffin", "work", "archive", "2026-05")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(live, filepath.Join(second, id+".md")); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, exit := taxRun(bin, env, "show", id)
	if exit != 4 {
		t.Fatalf("exit = %d, want 4 (conflict)\nstderr: %s", exit, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout must stay empty on an ambiguity error: %q", stdout)
	}
	for _, want := range []string{wantArchivePath(id), filepath.Join("work", "archive", "2026-05", id+".md")} {
		if !strings.Contains(stderr, want) {
			t.Errorf("error does not name candidate %q:\n%s", want, stderr)
		}
	}
}

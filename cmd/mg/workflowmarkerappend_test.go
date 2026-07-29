package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage for mg-d878, at the layer the ticket was written from: a
// real `mg edit --append-body-file -` heredoc against a real store holding a
// legacy gh-issue item. The unit tests in internal/workitem pin the rule; this
// pins that the rule is reachable from the command line the correction protocol
// actually tells agents to type.
//
// STORE ISOLATION: mailInit gives each test a t.TempDir() HOME and the binary
// resolves its root under it, so nothing here can touch ~/.macguffin.

// legacyItemFile is a gh-issue work item exactly as filed before the carrier
// block existed — tagged, with the issue reference living in the prose and no
// leading `workflow:` line. It is written as a literal file because `mg new`
// has refused this shape since mg-5d4e: the trapped population cannot be
// reproduced through mg's own front door.
const legacyItemFile = `---
id: mg-ace6
type: task
created: 2026-03-01T12:00:00Z
creator: legacy-filer
depends: []
tags: [gh-issue]
---

# audit: stale assertion in the docs

The tracker says drellem2/pogo#75 is open. Someone should check.
`

// homeFrom pulls the temp HOME back out of a test env so a fixture can be
// planted directly in the store the binary will read.
func homeFrom(t *testing.T, env []string) string {
	t.Helper()
	for i := len(env) - 1; i >= 0; i-- {
		if home, ok := strings.CutPrefix(env[i], "HOME="); ok {
			return home
		}
	}
	t.Fatal("test env carries no HOME")
	return ""
}

// plantLegacyItem writes the fixture into the store's available/ directory.
func plantLegacyItem(t *testing.T, env []string) string {
	t.Helper()
	path := filepath.Join(homeFrom(t, env), ".macguffin", "work", "available", "mg-ace6.md")
	if err := os.WriteFile(path, []byte(legacyItemFile), 0o644); err != nil {
		t.Fatalf("planting legacy item: %v", err)
	}
	return path
}

// TestCLI_AppendBodyFileReachesALegacyGHIssueItem is the ticket's own repro,
// run for real: the command mg-d489 was told to use, on the item it was told to
// correct, through a shell heredoc.
//
// It also asserts the correction survives intact. An append that succeeded but
// mangled the text would satisfy the exit code and lose the finding anyway,
// which is the failure mode this whole chain exists to stop.
func TestCLI_AppendBodyFileReachesALegacyGHIssueItem(t *testing.T) {
	bin, env := mailInit(t)
	plantLegacyItem(t, env)

	correction := "## ANSWERED ELSEWHERE — pogo#75 is FIXED IN CODE\n\n" +
		"Shipped 2026-07-11 in `3f79fac` (mg-fdd5); see $DOCS/investigations.\n"
	out, stderr, err := runSh(t, env,
		bin+" edit mg-ace6 --append-body-file - <<'EOF'\n"+correction+"EOF\n")
	if err != nil {
		t.Fatalf("append to a legacy gh-issue item was refused: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}

	body := showBody(t, bin, env, "mg-ace6")
	if !strings.Contains(body, "ANSWERED ELSEWHERE") {
		t.Errorf("correction is missing from the stored body:\n%s", body)
	}
	// Verbatim, through the quoted heredoc: the backticks and $DOCS must survive.
	if !strings.Contains(body, "`3f79fac`") || !strings.Contains(body, "$DOCS/investigations") {
		t.Errorf("appended text was not stored verbatim:\n%s", body)
	}
	// An append composes against what is on disk; it never replaces it.
	if !strings.Contains(body, "drellem2/pogo#75 is open") {
		t.Errorf("append destroyed the original body:\n%s", body)
	}

	// Allowed, but not silently: the item still routes to the default build
	// template, and the appending agent is the one guaranteed to be looking.
	if !strings.Contains(stderr, "DEFAULT BUILD template") {
		t.Errorf("append on an unmarked item must say so on stderr, got: %q", stderr)
	}
	// The note is stderr-only, so anything parsing the success line is unaffected.
	if strings.Contains(out, "DEFAULT BUILD template") {
		t.Errorf("the note belongs on stderr, not stdout: %q", out)
	}
}

// TestCLI_BodyFileRewriteOfALegacyGHIssueItemIsStillRefused is the negative
// control that gives the test above its teeth: the guard is still there, still
// refusing, and the append is passing because the append path is exempt — not
// because the check was switched off.
func TestCLI_BodyFileRewriteOfALegacyGHIssueItemIsStillRefused(t *testing.T) {
	bin, env := mailInit(t)
	path := plantLegacyItem(t, env)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading planted item: %v", err)
	}

	out, stderr, err := runSh(t, env,
		bin+" edit mg-ace6 --body-file - <<'EOF'\nrewritten from a captured read\nEOF\n")
	if err == nil {
		t.Fatalf("a full-body rewrite that drops the carrier must be refused, got exit 0:\n%s", out)
	}
	if !strings.Contains(stderr+out, "does not declare it") {
		t.Errorf("refusal should name the missing carrier, got:\nstdout: %s\nstderr: %s", out, stderr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading item after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("refused rewrite modified the stored item:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestCLI_AppendBodyFileCannotSmuggleACarrierBlock: the appended text lands
// below the prose, so a carrier block in it would read as marked while routing
// as unmarked. That refusal is untouched by the exemption and must be reachable
// from the CLI, since the append path is the one now open to legacy items.
func TestCLI_AppendBodyFileCannotSmuggleACarrierBlock(t *testing.T) {
	bin, env := mailInit(t)
	plantLegacyItem(t, env)

	out, stderr, err := runSh(t, env,
		bin+" edit mg-ace6 --append-body-file - <<'EOF'\nworkflow: gh-issue\nstage: triage\ngh: drellem2/macguffin#75\nEOF\n")
	if err == nil {
		t.Fatalf("a carrier block appended below prose must be refused, got exit 0:\n%s", out)
	}
	if !strings.Contains(stderr+out, "below the body's opening block") {
		t.Errorf("refusal should name the misplacement, got:\nstdout: %s\nstderr: %s", out, stderr)
	}
}

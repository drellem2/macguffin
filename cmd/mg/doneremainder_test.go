package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The declared-remainder guard on `mg done` (mg-8970).
//
// The escape it closes, measured: mg-ee98 was the gh drellem2/pogo#94 triage —
// `type: task`, completed `done`, verdict IMPLEMENT on a REPRODUCED data-loss
// mechanism — and nothing carried the fix. The archive-time `--successor` guard
// did not fire, because that guard's population is named by `type: design` and
// a triage is not a type. A human noticed the ABSENCE and filed the build by
// hand; nothing in mg would have.
//
// The ORDER below is deliberate, and is the same discipline archivesuccessor_test.go
// applies next door: the refusal is proven FIRST, observed refusing the bare
// case, before anything asserts a discharged item passes. A guard that has only
// ever been seen to pass has not been tested.
//
// But the SECOND block is the one this ticket turns on, and it is not merely a
// converse control. Two predicates were proposed for this rule before this one
// and both were proxies: `type == design` (misses triage) and `stage is
// non-terminal` (fires on a PAUSE, which owes nothing). Over the whole store on
// 2026-07-29, a stage-keyed predicate would have fired on 41 items, 34 of them
// already archived — an over-fire count, not an escape count. mg self-installs
// on merge, so a guard that blocks routine completions at that volume gets
// removed by whoever it inconveniences. Hence: EVERY ITEM THAT DID NOT DECLARE
// MUST STILL COMPLETE, and that is asserted for an ordinary task, for a
// stage-carrying item at a terminal stage, and for a stage-carrying item at a
// NON-terminal stage — the last being exactly the population the rejected
// predicate would have caught.

// seedClaimedTagged creates an item with the given extra `mg new` args, claims
// it, and returns its id — leaving it in claimed/, one step from done.
func seedClaimedTagged(t *testing.T, bin, root, title string, extra ...string) string {
	t.Helper()
	args := append([]string{"new", "--title=" + title}, extra...)
	out, code := mgArchive(t, bin, root, args...)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	_, rest, ok := strings.Cut(out, "Created ")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id = strings.TrimSpace(id)
	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", id, code, out)
	}
	return id
}

// bodyFileArg writes body to a temp file and returns the --body-file argument
// for it, so a carrier block reaches the item verbatim.
func bodyFileArg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing body file: %v", err)
	}
	return "--body-file=" + p
}

// --- The refusal, proven first ---------------------------------------------

// TestCLI_DoneDeclaredRemainderIsRefused is the primary control. The refusal
// must name the ITEM and the REASON — an agent hitting this is mid-protocol and
// asks "which one?" and "why now?" — and must leave the item claimed, so
// filing the successor and retrying is one step, not a recovery.
func TestCLI_DoneDeclaredRemainderIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT, nothing carries it", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("mg done on an item declaring a remainder exited 0 — the guard did not fire\n%s", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("refusal = %q, want it to name the item %s", out, id)
	}
	if !strings.Contains(out, "remainder") {
		t.Errorf("refusal = %q, want it to name the reason (a declared remainder)", out)
	}
	if !strings.Contains(out, "successor") {
		t.Errorf("refusal = %q, want it to name what is missing (a successor)", out)
	}
	if !strings.Contains(out, "--successor") {
		t.Errorf("refusal = %q, want it to name the remedy '--successor <id>'", out)
	}

	// Refusing must not have moved anything.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=claimed")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s left claimed/ despite the refusal:\n%s", id, listOut)
	}
	doneOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if strings.Contains(doneOut, id) {
		t.Errorf("%s reached done/ despite the refusal:\n%s", id, doneOut)
	}
}

// TestCLI_DoneRefusalDoesNotOfferRetraction: retracting the declaration
// (--rm-tags=declares-remainder) is the right move when a triage concludes
// nothing is owed, and it is documented in `mg done --help`, read deliberately.
// It must not appear in the refusal itself: an agent that hits a guard at speed
// reaches for whatever the message hands it, and a guard whose failure text
// teaches its own retraction is decorative — the same reasoning the archive
// guard applies to --force.
func TestCLI_DoneRefusalDoesNotOfferRetraction(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "a refusal that must not teach its own bypass", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("guard did not fire\n%s", out)
	}
	if strings.Contains(out, "--rm-tags") {
		t.Errorf("refusal = %q offers --rm-tags; the message must name the remedy, not the retraction", out)
	}
}

// TestCLI_DoneDanglingSuccessorIsRefused: a successor: tag naming an item that
// does not exist tracks nothing, exactly like no tag at all, and must be
// distinguishable from the bare case — "you pointed at a deleted item" and "you
// pointed at nothing" need different fixes.
func TestCLI_DoneDanglingSuccessorIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "declares a remainder, points at a ghost",
		"--declares-remainder", "--tags=successor:mg-ffff")

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("a successor tag naming nothing satisfied the guard\n%s", out)
	}
	if !strings.Contains(out, "mg-ffff") {
		t.Errorf("refusal = %q, want it to name the dangling successor", out)
	}
}

// TestCLI_DoneSuccessorMustExist: `--successor <id>` naming nothing must be
// refused at the moment it is typed, not recorded and discovered later.
func TestCLI_DoneSuccessorMustExist(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "declares a remainder", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id, "--successor=mg-ffff")
	if code == 0 {
		t.Fatalf("--successor=mg-ffff (no such item) was accepted\n%s", out)
	}
	if !strings.Contains(out, "mg-ffff") {
		t.Errorf("output = %q, want it to say the successor does not exist", out)
	}
}

// TestCLI_DoneSelfSuccessorIsRefused: an item cannot track itself. Without
// this, `--successor <its-own-id>` is a one-flag bypass that looks like
// compliance in the record.
func TestCLI_DoneSelfSuccessorIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "declares a remainder", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id, "--successor="+id)
	if code == 0 {
		t.Fatalf("a self-successor was accepted\n%s", out)
	}
	if !strings.Contains(out, "own successor") {
		t.Errorf("refusal = %q, want it to say an item cannot be its own successor", out)
	}
}

// --- It must NOT over-fire --------------------------------------------------
//
// This is the block the ticket turns on. A guard that blocks routine
// completions is removed by whoever it inconveniences (mg-3412), and mg
// self-installs on merge, so over-firing breaks the fleet's ability to complete
// work at all.

// TestCLI_DoneOrdinaryTaskStillCompletes: the overwhelming majority of
// completions. No declaration, no carrier block, nothing for the guard to read.
func TestCLI_DoneOrdinaryTaskStillCompletes(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "an ordinary task with no declaration at all")

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on an ordinary task: exit %d — the guard over-fires\n%s", code, out)
	}
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s did not reach done/:\n%s", id, listOut)
	}
}

// TestCLI_DoneTerminalStageStillCompletes: a workflow item at a TERMINAL stage.
// Its remainder, if any, is discharged; nothing is owed.
func TestCLI_DoneTerminalStageStillCompletes(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	body := bodyFileArg(t, "workflow: gh-issue\nstage: merge\ngh: drellem2/pogo#94\n\nMerged.\n")
	id := seedClaimedTagged(t, bin, root, "gh-issue at a terminal stage", body)

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on a terminal-stage item: exit %d — the guard over-fires\n%s", code, out)
	}
}

// TestCLI_DoneNonTerminalStageWithoutDeclarationStillCompletes is the direct
// refutation of the REJECTED predicate, and the reason this guard keys on a
// declaration rather than on `stage:`.
//
// `stage: gated` is exactly what mg-ee98 carried. A stage-keyed predicate fires
// here. This one does not, because the item declared nothing — and it must not,
// because the identical shape covers an item merely PAUSED at that stage, which
// owes no successor and whose completion is correct. Distinguishing "waiting on
// someone else" from "deliberately set aside" is not something a stage value
// can express, which is the whole finding.
func TestCLI_DoneNonTerminalStageWithoutDeclarationStillCompletes(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	body := bodyFileArg(t, "workflow: gh-issue\nstage: gated\ngh: drellem2/pogo#94\n\nHeld pending a ruling.\n")
	id := seedClaimedTagged(t, bin, root, "gh-issue paused at a non-terminal stage", body)

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on a non-terminal-stage item that declared nothing: exit %d\n"+
			"the predicate regressed to a stage proxy, which over-fires on paused items\n%s", code, out)
	}
}

// TestCLI_DoneSnoozeAttributeIsNotADeclaration: a snooze (mg-0a5f) is a single
// `snooze:` frontmatter timestamp on a pending/ item — no stage, no workflow
// marker, no successor obligation. It is a declared PAUSE. Pinning it here
// because the settled design turned on the paused-vs-owes-a-remainder
// distinction, and the property that makes it a non-issue — a snoozed item
// declares nothing for this guard to read — is worth being a test rather than a
// comment, in case the snooze shape ever grows a stage.
func TestCLI_DoneSnoozeAttributeIsNotADeclaration(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "an item that was once snoozed")

	// A spent snooze attribute left on the item must not read as a remainder.
	path := filepath.Join(root, "work", "claimed")
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("reading claimed/: %v", err)
	}
	var target string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id) {
			target = filepath.Join(path, e.Name())
		}
	}
	if target == "" {
		t.Fatalf("could not find %s in claimed/", id)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	// The file opens with "---\n", so the first "\n---\n" is the CLOSING
	// frontmatter delimiter — snooze: is rendered last, immediately above it.
	patched := strings.Replace(string(raw), "\n---\n", "\nsnooze: 2026-01-01T00:00:00Z\n---\n", 1)
	if patched == string(raw) {
		t.Fatalf("could not plant a snooze attribute; frontmatter shape changed:\n%s", raw)
	}
	if err := os.WriteFile(target, []byte(patched), 0o644); err != nil {
		t.Fatalf("writing %s: %v", target, err)
	}
	// The fixture is only meaningful if mg reads the attribute back.
	if showOut, _ := mgArchive(t, bin, root, "show", id); !strings.Contains(showOut, "2026-01-01") {
		t.Fatalf("mg does not see the planted snooze, so this proves nothing:\n%s", showOut)
	}

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on an item carrying a snooze attribute: exit %d — a pause is not a remainder\n%s", code, out)
	}
}

// --- The converse control ---------------------------------------------------

// TestCLI_DoneDeclaredRemainderWithSuccessorCompletes: a guard that refuses
// everything is indistinguishable from a broken tool. The discharged item must
// complete, and must carry the successor: tag into done/ so the completed
// record names its own tracker for any later reader.
func TestCLI_DoneDeclaredRemainderWithSuccessorCompletes(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	triage := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")
	build := seedAvailable(t, bin, root, "task", "build the thing the triage recommended")

	out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build)
	if code != 0 {
		t.Fatalf("mg done %s --successor=%s: exit %d\n%s", triage, build, code, out)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, triage) {
		t.Errorf("%s did not reach done/:\n%s", triage, listOut)
	}
	showOut, _ := mgArchive(t, bin, root, "show", triage)
	if !strings.Contains(showOut, "successor:"+build) {
		t.Errorf("completed %s does not carry successor:%s:\n%s", triage, build, showOut)
	}
}

// TestCLI_DoneResultSurvivesTheSuccessorLink: the guard sits ahead of the move,
// and --successor rewrites the item file before it. Neither may cost the caller
// the --result sidecar the polecat protocol always passes.
func TestCLI_DoneResultSurvivesTheSuccessorLink(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	triage := seedClaimedTagged(t, bin, root, "triage with a result", "--declares-remainder")
	build := seedAvailable(t, bin, root, "task", "the build it recommends")

	out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build, `--result={"branch":"polecat-ee98"}`)
	if code != 0 {
		t.Fatalf("mg done with --successor and --result: exit %d\n%s", code, out)
	}
	sidecar := filepath.Join(root, "work", "done", triage+".result.json")
	raw, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("reading %s: %v", sidecar, err)
	}
	if !strings.Contains(string(raw), "polecat-ee98") {
		t.Errorf("result sidecar = %q, want the branch the caller passed", raw)
	}
}

// TestCLI_DeclaresRemainderFlagWritesOneCanonicalTag: mg emits the declaration
// itself so it cannot be misspelled or forgotten, and passing it twice — once
// as a flag, once as a raw tag — must not file two copies.
func TestCLI_DeclaresRemainderFlagWritesOneCanonicalTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, code := mgArchive(t, bin, root, "new", "--title=declared two ways",
		"--declares-remainder", "--tags=declares-remainder,urgent")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	_, rest, _ := strings.Cut(out, "Created ")
	dup, _, _ := strings.Cut(rest, ":")
	dup = strings.TrimSpace(dup)

	showOut, _ := mgArchive(t, bin, root, "show", dup)
	if n := strings.Count(showOut, "declares-remainder"); n != 1 {
		t.Errorf("item carries %d copies of the declaration tag, want 1:\n%s", n, showOut)
	}
	if !strings.Contains(showOut, "urgent") {
		t.Errorf("the flag dropped the caller's own tags:\n%s", showOut)
	}
}

// --- The backstop, and what must not have changed ---------------------------

// TestCLI_ArchiveBackstopsADeclaredRemainder: `mg done` is the primary — it
// fails at the moment, to the agent standing there — but an item that reached
// done/ before this guard existed, or whose successor was deleted afterwards,
// must not then be archived silently. mg archive refuses it too.
func TestCLI_ArchiveBackstopsADeclaredRemainder(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	triage := seedClaimedTagged(t, bin, root, "triage whose successor is later deleted", "--declares-remainder")
	build := seedAvailable(t, bin, root, "task", "a build that will be removed")

	if out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}

	// Delete the successor out from under the completed record.
	matches, err := filepath.Glob(filepath.Join(root, "work", "available", build+"*"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("could not locate %s to delete: %v", build, err)
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			t.Fatalf("removing %s: %v", m, err)
		}
	}

	out, code := mgArchive(t, bin, root, "archive", triage)
	if code == 0 {
		t.Fatalf("mg archive took an item declaring a remainder with a dead successor\n%s", out)
	}
	if !strings.Contains(out, build) {
		t.Errorf("refusal = %q, want it to name the dangling successor %s", out, build)
	}
}

// TestCLI_DesignSuccessorGuardStillFiresAtArchive: the `type: design` guard is
// NOT weakened or replaced by this ticket — it becomes the backstop. A done
// design that never declared a remainder must still be refused by mg archive,
// keyed on its type, exactly as before.
func TestCLI_DesignSuccessorGuardStillFiresAtArchive(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "a design that declared nothing")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("the type: design archive guard stopped firing\n%s", out)
	}
	if !strings.Contains(out, "--successor") {
		t.Errorf("refusal = %q, want the design guard's remedy", out)
	}
}

// TestCLI_DoneDesignWithoutDeclarationStillCompletes is the other half of the
// line between the two guards, and the reason this ticket did not simply move
// the type check to done: `type: design` is a PROXY, and adding a fourth
// placement for it would re-introduce the very substitution this guard exists
// to end. A design completes at done/ as it always has; the type-keyed refusal
// lives at archive, where mg-12a0 put it.
func TestCLI_DoneDesignWithoutDeclarationStillCompletes(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "a design nobody declared a remainder on", "--type=design")

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on a design: exit %d — the done guard grew a type proxy\n%s", code, out)
	}
}

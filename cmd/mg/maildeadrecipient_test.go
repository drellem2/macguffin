package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers mg-cf1e: `mg mail send` accepted a KNOWN mailbox belonging to
// an agent that was gone, silently.
//
// mg-d639 gave mail a bad address — an UNKNOWN recipient exits 3 with a
// did-you-mean. What it could not see is the other way a delivery goes unread: a
// box that is perfectly real, whose agent has finished. A maildir outlives its
// agent, so the send is well-formed, the exit is 0, and nothing tells the sender
// the message will not be read. That is the shape drellem2/pogo#131 was reported
// from — a reviewer waiting on a builder that had already exited.
//
// The defining property of every test here is the pairing: the delivery STILL
// HAPPENS and the exit is STILL 0, and the warning is on stderr beside it.
// Refusing would be worse than the bug — mail to a finished agent's box is often
// deliberate, and `mg mail` is on the hot path for every agent in the fleet.

// mgSplit runs mg with stdout and stderr kept APART, which is the whole point
// here: the existing mgExit helper returns CombinedOutput, and a test built on
// it cannot tell a warning that keeps `--json` parseable from one that corrupts
// it. Those are opposite outcomes and they must not look alike.
func mgSplit(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running mg %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

// seedItemWithMailbox creates a work item and delivers one message to the box
// named for it, so the mailbox EXISTS independently of the item's lifecycle —
// which is the premise of the whole ticket. Returns the full id ("mg-XXXX") and
// the bare mailbox name ("XXXX").
func seedItemWithMailbox(t *testing.T, bin string, env []string, title string) (id, box string) {
	t.Helper()
	out, code := mgExit(t, bin, env, "new", title, "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	id = extractItemID(t, out)
	box = strings.TrimPrefix(id, "mg-")
	if out, code := mgExit(t, bin, env, "mail", "send", box,
		"--from=mayor", "--subject=dispatch", "--body=go"); code != 0 {
		t.Fatalf("seeding mailbox %s failed: exit %d\n%s", box, code, out)
	}
	return id, box
}

// finishItem drives an item to done through the real lifecycle, which is the
// only way the store records one.
func finishItem(t *testing.T, bin string, env []string, id string) {
	t.Helper()
	if out, code := mgExit(t, bin, env, "claim", id); code != 0 {
		t.Fatalf("claim %s failed: exit %d\n%s", id, code, out)
	}
	if out, code := mgExit(t, bin, env, "done", id, `--result={"ok":true}`); code != 0 {
		t.Fatalf("done %s failed: exit %d\n%s", id, code, out)
	}
}

// countMessages reports how many messages sit in a mailbox's new/, so a test can
// assert the delivery HAPPENED rather than trusting the exit code that the bug
// itself produced.
func countMessages(t *testing.T, env []string, box string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(homeFromEnv(env), ".macguffin", "mail", box, "new"))
	if err != nil {
		t.Fatalf("reading %s's new/: %v", box, err)
	}
	return len(entries)
}

// ---------------------------------------------------------------------------
// The probe, as a test.
// ---------------------------------------------------------------------------

// TestCLI_MailSendWarnsOnDoneRecipient is the ticket's positive control. Before
// this change it exited 0, printed "Delivered", and said nothing else — a
// terminal work item was a signal the sender never saw.
func TestCLI_MailSendWarnsOnDoneRecipient(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a polecat's work")
	finishItem(t, bin, env, id)

	before := countMessages(t, env, box)
	stdout, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=reviewer", "--subject=are you there", "--body=waiting on you")

	// The delivery still happens. This half is as load-bearing as the warning:
	// a refusal here could strand a coordinator mid-cycle.
	if code != 0 {
		t.Fatalf("a send to a finished agent's box must still succeed, got exit %d\nstdout:%s\nstderr:%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Delivered:") {
		t.Errorf("stdout must still report the delivery, got:\n%s", stdout)
	}
	if got := countMessages(t, env, box); got != before+1 {
		t.Errorf("the message must actually be delivered: %d messages before, %d after", before, got)
	}

	// And the sender is told.
	if stderr == "" {
		t.Fatalf("sending to a box whose work item is done must warn on stderr, got nothing")
	}
	if !strings.Contains(stderr, id) {
		t.Errorf("the warning must name the work item %s, got:\n%s", id, stderr)
	}
	if !strings.Contains(stderr, "completed") {
		t.Errorf("the warning must say what state the item is in, got:\n%s", stderr)
	}
}

// TestCLI_MailSendWarnsOnArchivedRecipient: archived is further past done, and
// warns for the same reason.
func TestCLI_MailSendWarnsOnArchivedRecipient(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "an archived polecat's work")
	finishItem(t, bin, env, id)
	if out, code := mgExit(t, bin, env, "archive", id); code != 0 {
		t.Fatalf("archive %s failed: exit %d\n%s", id, code, out)
	}

	stdout, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=reviewer", "--subject=late", "--body=hello")
	if code != 0 {
		t.Fatalf("send to an archived item's box must still succeed, got exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "archived") {
		t.Errorf("the warning must say the item was archived, got:\n%s", stderr)
	}
}

// TestCLI_MailSendWarnsOnShelvedRecipient pins the one state where this warning
// deliberately DIVERGES from workitem.liveStates, which counts shelved as live.
// That set answers "can this still be worked?"; this warning answers "is anyone
// reading this box?" A shelved item is parked, so nobody is.
func TestCLI_MailSendWarnsOnShelvedRecipient(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a parked polecat's work")
	if out, code := mgExit(t, bin, env, "shelve", id); code != 0 {
		t.Skipf("shelve %s refused by its guard, so this state is unreachable here: %s", id, out)
	}

	stdout, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=mayor", "--subject=status?", "--body=hello")
	if code != 0 {
		t.Fatalf("send to a shelved item's box must still succeed, got exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "shelved") {
		t.Errorf("the warning must say the item is shelved, got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// Silence where silence is correct. These are the hot-path guards: `mg mail` is
// used by every agent in the fleet, and a warning that fires on the happy path
// is a warning the fleet learns to ignore.
// ---------------------------------------------------------------------------

// TestCLI_MailSendQuietOnClaimedRecipient: claimed is the one status that
// positively says an agent holds the item. Mailing it is the normal case.
func TestCLI_MailSendQuietOnClaimedRecipient(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a working polecat's work")
	if out, code := mgExit(t, bin, env, "claim", id); code != 0 {
		t.Fatalf("claim failed: exit %d\n%s", code, out)
	}

	_, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=mayor", "--subject=nudge", "--body=status?")
	if code != 0 {
		t.Fatalf("send to a claimed item's box failed: exit %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("mailing an agent that is running its item must stay silent, got:\n%s", stderr)
	}
}

// TestCLI_MailSendQuietOnAvailableRecipient guards the case that made work-item
// names addressable in the first place: the mayor's first message to a polecat
// it is about to spawn. The item is available, no agent has it yet, and warning
// here would fire on every legitimate dispatch in the fleet.
func TestCLI_MailSendQuietOnAvailableRecipient(t *testing.T) {
	bin, env := mailInit(t)
	_, box := seedItemWithMailbox(t, bin, env, "work nobody has claimed")

	_, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=mayor", "--subject=dispatch", "--body=go")
	if code != 0 {
		t.Fatalf("send to an available item's box failed: exit %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("an item awaiting dispatch is ahead of the sender, not behind it; must stay silent, got:\n%s", stderr)
	}
}

// TestCLI_MailSendQuietOnNonWorkItemMailbox: most mailboxes in the fleet —
// mayor, human, long-lived crew agents — are not named for a work item at all.
// Nothing about them is terminal and nothing should be said.
func TestCLI_MailSendQuietOnNonWorkItemMailbox(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")

	_, stderr, code := mgSplit(t, bin, env, "mail", "send", "mayor",
		"--from=polecat", "--subject=report", "--body=done")
	if code != 0 {
		t.Fatalf("send to a registered non-work-item box failed: exit %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("a mailbox that is not a work item has no lifecycle to report, got:\n%s", stderr)
	}
}

// TestCLI_MailSendQuietWhenALiveRecordSharesTheName: an id can name more than
// one record, and a live one wins. Under-warning is the correct direction to err
// on a hot path — a warning that fires while somebody IS reading teaches the
// fleet to ignore it, which costs more than the silence it replaced.
func TestCLI_MailSendQuietWhenALiveRecordSharesTheName(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a shadowed polecat's work")
	finishItem(t, bin, env, id)

	// Hand-place a live twin under the same id. There is no supported route to
	// this state, which is exactly why it is worth pinning: the resolver
	// arbitrates it elsewhere and this warning must agree with that arbitration.
	root := filepath.Join(homeFromEnv(env), ".macguffin")
	twin := filepath.Join(root, "work", "available", id+".md")
	if err := os.WriteFile(twin, []byte("# "+id+" a live twin\n"), 0o644); err != nil {
		t.Fatalf("planting a live twin: %v", err)
	}

	_, stderr, code := mgSplit(t, bin, env, "mail", "send", box,
		"--from=mayor", "--subject=which one", "--body=hello")
	if code != 0 {
		t.Fatalf("send failed: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, "probably gone") {
		t.Errorf("a live record sharing the id must suppress the warning, got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// The machine-readable half.
// ---------------------------------------------------------------------------

// TestCLI_MailSendJSONCarriesRecipientStatus: the warning must reach a scripted
// consumer WITHOUT corrupting the stdout contract that consumer parses. The
// warning stays on stderr and the fact rides in a new field.
//
// This is the remedy checked against the defect it remedies: a notice printed on
// stdout would make `mg mail send --json` unparseable, which is a quieter and
// worse version of "the sender cannot tell what happened".
func TestCLI_MailSendJSONCarriesRecipientStatus(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a polecat's finished work")
	finishItem(t, bin, env, id)

	stdout, stderr, code := mgSplit(t, bin, env, "mail", "send", box, "--json",
		"--from=reviewer", "--subject=late", "--body=hello")
	if code != 0 {
		t.Fatalf("send failed: exit %d\nstdout:%s\nstderr:%s", code, stdout, stderr)
	}

	var got mailSendJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("stdout must stay ONE parseable object, got %v for:\n%s", err, stdout)
	}
	if got.RecipientWorkItemStatus != "done" {
		t.Errorf("recipient_work_item_status = %q, want \"done\"", got.RecipientWorkItemStatus)
	}
	if strings.Contains(stdout, "warning") {
		t.Errorf("the warning must not be on stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "warning") {
		t.Errorf("the warning must still reach a human on stderr, got:\n%s", stderr)
	}
}

// TestCLI_MailSendJSONStatusEmptyForLiveRecipient: the field is empty whenever
// there is nothing to report, so a consumer tests for non-empty rather than
// enumerating states it has to keep in sync with mg.
func TestCLI_MailSendJSONStatusEmptyForLiveRecipient(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")

	stdout, _, code := mgSplit(t, bin, env, "mail", "send", "mayor", "--json",
		"--from=polecat", "--subject=report", "--body=done")
	if code != 0 {
		t.Fatalf("send failed: exit %d\n%s", code, stdout)
	}
	var got mailSendJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if got.RecipientWorkItemStatus != "" {
		t.Errorf("recipient_work_item_status = %q, want empty for a recipient with nothing to report", got.RecipientWorkItemStatus)
	}
}

// ---------------------------------------------------------------------------
// Reply, where the gap was actually reported from.
// ---------------------------------------------------------------------------

// TestCLI_MailReplyWarnsOnDoneRecipient: reply is where this matters most. The
// recipient is a From header rather than a name the sender chose, and answering
// an agent that has already exited is precisely the stall of drellem2/pogo#131.
func TestCLI_MailReplyWarnsOnDoneRecipient(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a builder's work")
	mailRegisterBoxes(t, bin, env, "reviewer")

	// The builder writes to the reviewer, then finishes and exits.
	if out, code := mgExit(t, bin, env, "mail", "send", "reviewer",
		"--from="+box, "--subject=ready for review", "--body=branch pushed"); code != 0 {
		t.Fatalf("seed send failed: exit %d\n%s", code, out)
	}
	finishItem(t, bin, env, id)

	orig := soleMsgID(t, env, "reviewer")
	stdout, stderr, code := mgSplit(t, bin, asAgent(env, "reviewer"),
		"mail", "reply", "reviewer/"+orig, "--body=one comment, please fix")
	if code != 0 {
		t.Fatalf("the reply must still be delivered, got exit %d\nstdout:%s\nstderr:%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Replied:") {
		t.Errorf("stdout must still report the reply, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, id) {
		t.Errorf("the reply must warn that %s is finished, got stderr:\n%s", id, stderr)
	}
}

// TestCLI_MailSendRefusalCarriesNoDeliveryWarning: the warning describes a
// message that EXISTS, so a send that never delivered must not carry one.
//
// This is the remedy checked against the defect one more time. The bug was a
// send whose output did not match what happened to the message; a warning
// printed beside a refusal would be the same mismatch wearing the fix's clothes.
func TestCLI_MailSendRefusalCarriesNoDeliveryWarning(t *testing.T) {
	bin, env := mailInit(t)

	stdout, stderr, code := mgSplit(t, bin, env, "mail", "send", "definitely-nobody-cf1e",
		"--from=tester", "--subject=x", "--body=y")
	if code != 3 {
		t.Fatalf("an unknown recipient must still be refused with exit 3, got %d\nstdout:%s\nstderr:%s", code, stdout, stderr)
	}
	if strings.Contains(stderr, "delivered, but") {
		t.Errorf("a refused send must not carry a delivery warning, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "Delivered:") {
		t.Errorf("a refused send must not report a delivery, got:\n%s", stdout)
	}
}

// TestCLI_MailSendWarningNamesTheWayToCheck: the warning is an inference from a
// state, not an assertion that a process is dead, so it has to hand the reader
// the command that settles it. A warning nobody can act on is noise on a hot
// path, and noise on a hot path is how the next signal gets ignored.
func TestCLI_MailSendWarningNamesTheWayToCheck(t *testing.T) {
	bin, env := mailInit(t)
	id, box := seedItemWithMailbox(t, bin, env, "a polecat's work")
	finishItem(t, bin, env, id)

	_, stderr, _ := mgSplit(t, bin, env, "mail", "send", box,
		"--from=reviewer", "--subject=x", "--body=y")
	if !strings.Contains(stderr, "mg show "+id) {
		t.Errorf("the warning must name the command that settles it, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "nothing is wrong") {
		t.Errorf("the warning must say that a deliberate send to a finished box is legitimate, got:\n%s", stderr)
	}
}

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers mg-d639: `mg mail` used to have no bad addresses. Three
// defects, each of which made a MISDELIVERY read as something other than a
// misdelivery — a phantom mailbox reported as Delivered, a fictional mailbox
// reported as a quiet one, and an agent's own inbox reported as somebody else's.

// mgExit runs an arbitrary mg subcommand and returns combined output plus the
// exit code, which is the half these tests care about: the old behaviour's
// defining property was exit 0 on a misdelivery.
func mgExit(t *testing.T, bin string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running mg %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// ---------------------------------------------------------------------------
// (1) A name nobody has used is a BAD ADDRESS, not a new mailbox.
// ---------------------------------------------------------------------------

// TestCLI_MailSendRefusesUnknownRecipient is the ticket's positive control,
// stated as a test: address a mail to a name that does not exist and OBSERVE
// THE FAILURE. Before mg-d639 this exited 0, printed "Delivered", and minted a
// dead drop.
func TestCLI_MailSendRefusesUnknownRecipient(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "mail", "send", "definitely-nobody-9ecf",
		"--from=tester", "--subject=x", "--body=y")
	if code == 0 {
		t.Fatalf("send to a name nobody has used must FAIL, got exit 0:\n%s", out)
	}
	if code != 3 {
		t.Errorf("unknown recipient should be not_found (exit 3), got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "definitely-nobody-9ecf") {
		t.Errorf("the refusal must name the recipient it refused, got:\n%s", out)
	}
	if !strings.Contains(out, "--create") {
		t.Errorf("the refusal must name the way through, got:\n%s", out)
	}
	if strings.Contains(out, "Delivered") {
		t.Errorf("a refused send must not report Delivered, got:\n%s", out)
	}

	// And nothing was created on the way out: a refused address leaves no
	// mailbox behind, or the second attempt would silently succeed.
	list, _ := mgExit(t, bin, env, "mail", "list")
	if strings.Contains(list, "definitely-nobody-9ecf") {
		t.Errorf("a refused send must not create the mailbox, got:\n%s", list)
	}
}

// TestCLI_MailSendSuggestsNearNeighbour: the typo that cost four mails was one
// dropped character ("9ecf" for "v9ecf"). Naming the neighbour turns the
// refusal into a correction.
func TestCLI_MailSendSuggestsNearNeighbour(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "v9ecf")

	out, code := mgExit(t, bin, env, "mail", "send", "9ecf",
		"--from=tester", "--subject=x", "--body=y")
	if code == 0 {
		t.Fatalf("send to the typo'd name must fail, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "did you mean v9ecf?") {
		t.Errorf("the refusal should suggest the near neighbour, got:\n%s", out)
	}
}

// TestCLI_MailSendCreateIsTheExplicitFirstDelivery: --create is what separates
// "this recipient is new" from "I mistyped". The distinction the old
// "(new mailbox created)" note could not draw is now drawn by the CALLER, at
// the point where only the caller knows the answer.
func TestCLI_MailSendCreateIsTheExplicitFirstDelivery(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "mail", "send", "brandnew", "--create",
		"--from=tester", "--subject=x", "--body=y")
	if code != 0 {
		t.Fatalf("--create must deliver to a new recipient, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(new mailbox created)") {
		t.Errorf("a first delivery should still note the new mailbox, got:\n%s", out)
	}

	// Registered by that delivery, the name no longer needs --create.
	out, code = mgExit(t, bin, env, "mail", "send", "brandnew",
		"--from=tester", "--subject=x2", "--body=y2")
	if code != 0 {
		t.Fatalf("a registered recipient needs no --create, got exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "new mailbox created") {
		t.Errorf("second delivery must not claim to create the mailbox, got:\n%s", out)
	}
}

// TestCLI_MailRegisterMakesANameAddressable: `mg mail register` is the
// registration mg never had, spelled out. It is idempotent, so it is safe in the
// provisioning scripts that are its reason for existing.
func TestCLI_MailRegisterMakesANameAddressable(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "mail", "register", "newcrew", "--json")
	if code != 0 {
		t.Fatalf("register failed: exit %d\n%s", code, out)
	}
	var reg mailRegisterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reg); err != nil {
		t.Fatalf("register --json is not valid JSON: %v\n%s", err, out)
	}
	if reg.Mailbox != "newcrew" || !reg.Created {
		t.Errorf("register json = %+v, want {newcrew true}", reg)
	}

	// Idempotent: exit 0, created false, nothing changed.
	out, code = mgExit(t, bin, env, "mail", "register", "newcrew", "--json")
	if code != 0 {
		t.Fatalf("re-registering must be a no-op, got exit %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reg); err != nil {
		t.Fatalf("register --json is not valid JSON: %v\n%s", err, out)
	}
	if reg.Created {
		t.Errorf("re-registering must report created=false, got %+v", reg)
	}

	// And the name is now addressable without --create.
	if out, code := mgExit(t, bin, env, "mail", "send", "newcrew",
		"--from=tester", "--subject=x", "--body=y"); code != 0 {
		t.Fatalf("a registered mailbox must accept mail, got exit %d:\n%s", code, out)
	}
}

// TestCLI_MailSendAcceptsAWorkItemName: a polecat's mailbox is named for the
// work item it is running, and that item exists before anyone has mailed the
// agent. Requiring --create for the mayor's first message to a new polecat would
// put the flag on every legitimate dispatch, which is where a flag stops being
// read — so a work item IS the registration.
func TestCLI_MailSendAcceptsAWorkItemName(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "new", "Polecat work", "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	id := extractItemID(t, out) // "mg-XXXX"
	box := strings.TrimPrefix(id, "mg-")

	// Addressed by the work-item alias, with no mailbox in existence yet.
	out, code = mgExit(t, bin, env, "mail", "send", id, "--from=mayor", "--subject=Directive", "--body=go")
	if code != 0 {
		t.Fatalf("a work-item name must be addressable without --create, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(new mailbox created)") {
		t.Errorf("the first delivery to a work-item box should still note it, got:\n%s", out)
	}

	// A neighbouring id that is NOT a work item stays a bad address.
	if out, code := mgExit(t, bin, env, "mail", "send", box+"x",
		"--from=mayor", "--subject=x", "--body=y"); code == 0 {
		t.Errorf("a name that is neither mailbox nor work item must be refused, got:\n%s", out)
	}
}

// TestCLI_MailReplyRefusesUnknownSender: a From header is free text its sender
// wrote, so reply is a send to an address mg never checked. It gets the same
// refusal and the same escape.
func TestCLI_MailReplyRefusesUnknownSender(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "arch")

	if _, code := mgExit(t, bin, env, "mail", "send", "arch",
		"--from=ghost-sender", "--subject=hello", "--body=b"); code != 0 {
		t.Fatal("seed send failed")
	}
	orig := soleMsgID(t, env, "arch")

	out, code := mgExit(t, bin, asAgent(env, "arch"), "mail", "reply", "arch/"+orig, "--body=on it")
	if code == 0 {
		t.Fatalf("replying to a sender with no mailbox must be refused, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "ghost-sender") {
		t.Errorf("the refusal must name the recipient, got:\n%s", out)
	}

	// The refused reply must not have consumed the original's unread state:
	// otherwise a failed answer costs the message too.
	list, _ := mgExit(t, bin, env, "mail", "list", "arch")
	if !strings.Contains(list, orig) {
		t.Errorf("a refused reply must leave the original unread, got:\n%s", list)
	}

	if out, code := mgExit(t, bin, asAgent(env, "arch"), "mail", "reply", "arch/"+orig,
		"--body=on it", "--create"); code != 0 {
		t.Fatalf("--create must let the reply through, got exit %d:\n%s", code, out)
	}
}

// ---------------------------------------------------------------------------
// (2) "never existed" and "real but empty" are different answers, in tooling too.
// ---------------------------------------------------------------------------

// TestCLI_MailListJSONDistinguishesMissingFromEmpty: under --json both cases
// used to emit NOTHING AT ALL — byte-identical empty output — so no scripted
// consumer could tell a quiet inbox from a misdelivery.
func TestCLI_MailListJSONDistinguishesMissingFromEmpty(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "realbox")

	decode := func(out string) mailboxJSON {
		t.Helper()
		var box mailboxJSON
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &box); err != nil {
			t.Fatalf("list --json on an empty mailbox must emit a status object, got %q (%v)", out, err)
		}
		return box
	}

	missing, code := mgExit(t, bin, env, "mail", "list", "9ecf", "--json")
	if code != 0 {
		t.Fatalf("list --json of a missing mailbox should exit 0, got %d:\n%s", code, missing)
	}
	got := decode(missing)
	if got.Mailbox != "9ecf" || got.Exists || got.Unread != 0 {
		t.Errorf("missing mailbox json = %+v, want {9ecf 0 false}", got)
	}

	empty, code := mgExit(t, bin, env, "mail", "list", "realbox", "--json")
	if code != 0 {
		t.Fatalf("list --json of an empty mailbox should exit 0, got %d:\n%s", code, empty)
	}
	got = decode(empty)
	if got.Mailbox != "realbox" || !got.Exists || got.Unread != 0 {
		t.Errorf("empty mailbox json = %+v, want {realbox 0 true}", got)
	}

	// The whole point: the two are no longer the same bytes.
	if strings.TrimSpace(missing) == strings.TrimSpace(empty) {
		t.Errorf("missing and empty must not produce identical json, both were:\n%s", missing)
	}

	// A mailbox with mail still emits pure message NDJSON — the status object
	// replaces an EMPTY stream, it does not join a populated one.
	if _, code := mgExit(t, bin, env, "mail", "send", "realbox",
		"--from=mayor", "--subject=s", "--body=b"); code != 0 {
		t.Fatal("send failed")
	}
	full, _ := mgExit(t, bin, env, "mail", "list", "realbox", "--json")
	for _, line := range strings.Split(strings.TrimSpace(full), "\n") {
		var msg mailMsgJSON
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.ID == "" {
			t.Errorf("a populated mailbox must emit only message objects, got line: %s", line)
		}
	}
}

// TestCLI_MailListMissingMailboxReadsAsMissing: the human line used to ACTIVELY
// CONFIRM the wrong hypothesis — "No mailbox for X yet" was read as "X has no
// new mail", which is how a stalled review loop stayed invisible.
func TestCLI_MailListMissingMailboxReadsAsMissing(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "bf3ae")

	out, code := mgExit(t, bin, env, "mail", "list", "bf3ad")
	if code != 0 {
		t.Fatalf("listing a missing mailbox should exit 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "No such mailbox: bf3ad") {
		t.Errorf("a missing mailbox must be reported as missing, got:\n%s", out)
	}
	if !strings.Contains(out, "Did you mean bf3ae?") {
		t.Errorf("a missing mailbox with a near neighbour should suggest it, got:\n%s", out)
	}

	// The real one reads as real, and says so even when it is quiet.
	out, code = mgExit(t, bin, env, "mail", "list", "bf3ae")
	if code != 0 {
		t.Fatalf("listing an empty mailbox should exit 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "mailbox exists") {
		t.Errorf("an existing-but-empty mailbox must say so, got:\n%s", out)
	}
	if strings.Contains(out, "No such mailbox") {
		t.Errorf("an existing mailbox must not read as missing, got:\n%s", out)
	}

	// --archived on a mailbox that never existed reports the mailbox, not the
	// archive: "no archived messages" for a fictional box is the same false
	// reassurance in a different subdirectory.
	out, _ = mgExit(t, bin, env, "mail", "list", "nosuchbox", "--archived")
	if !strings.Contains(out, "No such mailbox: nosuchbox") {
		t.Errorf("--archived on a missing mailbox must report the mailbox, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// (3) The cross-box guard stops firing on your OWN inbox.
// ---------------------------------------------------------------------------

// TestCLI_MailReadOwnWorkItemBox: mailboxes have no registration, so an agent's
// inbox is whichever name its SENDERS used — routinely the work item it is
// running. Agent "pd639" reading box "d639" was refused in wording that reads
// like a permissions error; a polecat meeting it concludes it may not read its
// own mail and leaves the mail unread, which is the exact outcome the guard
// exists to prevent.
func TestCLI_MailReadOwnWorkItemBox(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "new", "Polecat work", "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	id := extractItemID(t, out)
	box := strings.TrimPrefix(id, "mg-")

	if out, code := mgExit(t, bin, env, "mail", "send", id,
		"--from=mayor", "--subject=Directive", "--body=go"); code != 0 {
		t.Fatalf("send to the work-item box failed: exit %d\n%s", code, out)
	}
	msgID := soleMsgID(t, env, box)

	// The polecat's agent name is derived from the work item, not equal to it.
	out, code = mgExit(t, bin, asAgent(env, "p"+box), "mail", "read", box+"/"+msgID)
	if code != 0 {
		t.Fatalf("an agent must be able to read its own work-item box without --force, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Directive") {
		t.Errorf("the message should have been read, got:\n%s", out)
	}
}

// TestCLI_MailReadGuardStillFiresAcrossBoxes: the fix widens who counts as the
// owner; it must not open every mailbox to everyone. An agent whose name says
// nothing about the box is still refused.
func TestCLI_MailReadGuardStillFiresAcrossBoxes(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "new", "Polecat work", "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	id := extractItemID(t, out)
	box := strings.TrimPrefix(id, "mg-")

	if _, code := mgExit(t, bin, env, "mail", "send", id,
		"--from=mayor", "--subject=Directive", "--body=go"); code != 0 {
		t.Fatal("send failed")
	}
	msgID := soleMsgID(t, env, box)

	out, code = mgExit(t, bin, asAgent(env, "architect"), "mail", "read", box+"/"+msgID)
	if code == 0 {
		t.Fatalf("an unrelated agent must still be refused, got exit 0:\n%s", out)
	}
	// The refusal reframes a work-item box as probably-yours rather than framing
	// it purely as an intrusion on somebody else, which is what made agents
	// abandon their own mail instead of passing --force.
	if !strings.Contains(out, "WORK ITEM") || !strings.Contains(out, "--force") {
		t.Errorf("a work-item box refusal should say it may be the caller's own and name --force, got:\n%s", out)
	}

	// The message is still unread: a refusal that consumed it would be the very
	// loss the guard protects against.
	list, _ := mgExit(t, bin, env, "mail", "list", box)
	if !strings.Contains(list, msgID) {
		t.Errorf("a refused read must leave the message unread, got:\n%s", list)
	}

	// A box that is NOT a work item keeps the plain wording — there is nothing
	// to suggest the caller might own it.
	mailRegisterBoxes(t, bin, env, "mayor")
	if _, code := mgExit(t, bin, env, "mail", "send", "mayor",
		"--from=arch", "--subject=s", "--body=b"); code != 0 {
		t.Fatal("send to mayor failed")
	}
	crewMsg := soleMsgID(t, env, "mayor")
	out, code = mgExit(t, bin, asAgent(env, "architect"), "mail", "read", "mayor/"+crewMsg)
	if code == 0 {
		t.Fatalf("cross-box read of a crew mailbox must be refused, got exit 0:\n%s", out)
	}
	if strings.Contains(out, "WORK ITEM") {
		t.Errorf("a non-work-item box must not be described as one, got:\n%s", out)
	}
}

// TestCLI_MailReadGuardRefusesIdInsideAnId closes the one case where the
// name-containment evidence can hold and still be wrong. Work-item ids are
// fixed-width, so no two of them contain one another — but a name that is
// ITSELF an id can contain a shorter slice that happens to be another id. The
// caller then looks, by containment alone, like the owner of a box it has no
// claim on. An agent whose own name resolves to a work item is judged by that
// item, not by the substrings inside it.
func TestCLI_MailReadGuardRefusesIdInsideAnId(t *testing.T) {
	bin, env := mailInit(t)

	// Two mailboxes whose names overlap: "abcd" and the longer "abcde". Both
	// are registered, and both are made real work items so the containment and
	// work-item tests would otherwise BOTH pass.
	mailRegisterBoxes(t, bin, env, "abcd", "abcde")
	seedNamedItem(t, bin, env, "mg-abcd")
	seedNamedItem(t, bin, env, "mg-abcde")

	if _, code := mgExit(t, bin, env, "mail", "send", "abcd",
		"--from=mayor", "--subject=Directive", "--body=go"); code != 0 {
		t.Fatal("send failed")
	}
	msgID := soleMsgID(t, env, "abcd")

	// "abcde" contains "abcd", but "abcde" is itself a work item — so it names
	// its OWN box, and the overlap is coincidence.
	out, code := mgExit(t, bin, asAgent(env, "abcde"), "mail", "read", "abcd/"+msgID)
	if code == 0 {
		t.Fatalf("an agent whose own name is a work item must not own a box merely nested in it, got exit 0:\n%s", out)
	}
	if list, _ := mgExit(t, bin, env, "mail", "list", "abcd"); !strings.Contains(list, msgID) {
		t.Errorf("the refused read must leave the message unread, got:\n%s", list)
	}
}

// seedNamedItem plants a work item file with an exact id, which `mg new` cannot
// do — its ids are a hash of title and creation time. The guard reads the store
// through the ordinary resolver, so a plain file in available/ is enough.
func seedNamedItem(t *testing.T, bin string, env []string, id string) {
	t.Helper()
	home := homeFromEnv(env)
	path := filepath.Join(home, ".macguffin", "work", "available", id+".md")
	body := "---\nid: " + id + "\ntype: task\ncreated: 2026-08-07T00:00:00Z\n---\n\n# " + id + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seeding work item %s: %v", id, err)
	}
}

// extractItemID pulls the "mg-XXXX" id out of `mg new` output.
func extractItemID(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		f = strings.TrimSuffix(f, ":")
		if strings.HasPrefix(f, "mg-") {
			return f
		}
	}
	t.Fatalf("no work item id in mg new output:\n%s", out)
	return ""
}

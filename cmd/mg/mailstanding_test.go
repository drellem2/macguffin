package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers the second half of mg-d639: a mailbox that EXISTS was not
// evidence that anyone meant it to.
//
// The refusal added by mailaddress_test.go fires once per name — existence is
// what it consults — so a name talked past it once with --create is a good
// address forever after, indistinguishable on disk from one somebody
// deliberately established. The live proof is the `daniel` mailbox: in daily
// use, receiving real mail from several agents, never registered. It works, and
// "it works" is exactly the evidence that was missing.
//
// The tests below are about that AFTER-THE-FACT question. Registration is now a
// durable record, so "was this name meant?" has an answer that survives the
// send, and a box nobody established can be told apart from one somebody did.

// registrationPathFor spells the record's on-disk location out longhand rather
// than calling into the mail package, so the test pins the layout a scripted
// consumer would find rather than agreeing with the code by construction.
func registrationPathFor(env []string, box string) string {
	return filepath.Join(homeFromEnv(env), ".macguffin", "mail", box, ".registration.json")
}

// legacyBox reproduces a mailbox from before registration records existed: it
// is real, it holds mail, and nothing on disk says anyone established it. That
// is the `daniel` case, and it cannot be built any other way now — every
// supported route to a new box leaves a record or a work item behind, which is
// the point of the change.
func legacyBox(t *testing.T, bin string, env []string, box string, messages int) {
	t.Helper()
	for i := 0; i < messages; i++ {
		out, code := mgExit(t, bin, env, "mail", "send", box, "--create",
			"--from=someagent", "--subject=legacy", "--body=body")
		if code != 0 {
			t.Fatalf("seeding legacy box %s failed: exit %d\n%s", box, code, out)
		}
	}
	if err := os.Remove(registrationPathFor(env, box)); err != nil {
		t.Fatalf("stripping registration from %s: %v", box, err)
	}
}

// mailboxStandingJSON reads one box's status object out of `mg mail list
// --json`, which is where a scripted consumer finds it for ANY box.
//
// The two --json forms answer between them, and the split is the existing
// contract rather than anything this change introduced: the no-arg enumeration
// emits one status object per box that EXISTS, and the per-box form emits one
// in place of the empty stream it used to emit for a box with nothing to list.
// A box with mail is in the enumeration; a box that never existed is only
// reachable per-box, and is exactly where the per-box sentinel speaks. Asking
// the enumeration first and falling back mirrors that, so the helper reads a
// box's standing the way a consumer would rather than the way that happens to
// be convenient.
func mailboxStandingJSON(t *testing.T, bin string, env []string, box string) mailboxJSON {
	t.Helper()

	all, code := mgExit(t, bin, env, "mail", "list", "--json")
	if code != 0 {
		t.Fatalf("mail list --json failed: exit %d\n%s", code, all)
	}
	for _, line := range strings.Split(strings.TrimSpace(all), "\n") {
		if line == "" {
			continue
		}
		var b mailboxJSON
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			t.Fatalf("mail list --json line is not JSON: %v\n%s", err, line)
		}
		if b.Mailbox == box {
			return b
		}
	}

	// Not in the enumeration, so it does not exist: the per-box sentinel is
	// the only thing that can speak for it.
	out, code := mgExit(t, bin, env, "mail", "list", box, "--json")
	if code != 0 {
		t.Fatalf("mail list %s --json failed: exit %d\n%s", box, code, out)
	}
	var got mailboxJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("mail list %s --json is not one JSON object: %v\n%s", box, err, out)
	}
	if got.Exists {
		t.Fatalf("%s reports exists=true but is absent from the enumeration — the two --json forms disagree about one store:\n%s", box, all)
	}
	return got
}

// ---------------------------------------------------------------------------
// The record exists, and it says who and when.
// ---------------------------------------------------------------------------

// TestCLI_MailRegisterWritesADurableRecord: registration used to create the
// maildir "and nothing else", so it left no trace of itself. Existence was the
// only evidence, and existence is produced by delivery too.
func TestCLI_MailRegisterWritesADurableRecord(t *testing.T) {
	bin, env := mailInit(t)
	env = append(env, "MG_ACTOR=provisioner")

	out, code := mgExit(t, bin, env, "mail", "register", "newcrew", "--json")
	if code != 0 {
		t.Fatalf("register failed: exit %d\n%s", code, out)
	}
	var reg mailRegisterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reg); err != nil {
		t.Fatalf("register --json is not valid JSON: %v\n%s", err, out)
	}
	if !reg.Created || !reg.Registered {
		t.Errorf("a fresh register must report created and registered, got %+v", reg)
	}
	if reg.Adopted {
		t.Errorf("a box that did not exist cannot be adopted, got %+v", reg)
	}
	if reg.RegisteredBy != "provisioner" {
		t.Errorf("registered_by = %q, want the acting agent %q", reg.RegisteredBy, "provisioner")
	}
	if reg.Via != "register" {
		t.Errorf("via = %q, want %q", reg.Via, "register")
	}
	if reg.RegisteredAt == "" {
		t.Errorf("registered_at must say WHEN, got empty: %+v", reg)
	}

	// The record is on disk, not merely in the answer: the whole value of it is
	// being readable long after the command that printed this exited.
	data, err := os.ReadFile(registrationPathFor(env, "newcrew"))
	if err != nil {
		t.Fatalf("registration record must be on disk: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("registration record is not valid JSON: %v\n%s", err, data)
	}
	if onDisk["registered_by"] != "provisioner" {
		t.Errorf("on-disk record = %v, want registered_by=provisioner", onDisk)
	}

	if got := mailboxStandingJSON(t, bin, env, "newcrew"); got.Registration != "registered" {
		t.Errorf("registration = %q, want registered (%+v)", got.Registration, got)
	}
}

// TestCLI_MailRegisterDoesNotRewriteAnExistingRecord: re-registering stays
// idempotent, and idempotent here has to mean "changes nothing" rather than
// "writes the same thing again". The record names the FIRST deliberate act; a
// second register that stamped its own name over it would erase the only copy
// of who actually vouched for the name.
func TestCLI_MailRegisterDoesNotRewriteAnExistingRecord(t *testing.T) {
	bin, env := mailInit(t)

	first := append(append([]string{}, env...), "MG_ACTOR=first-owner")
	if out, code := mgExit(t, bin, first, "mail", "register", "shared"); code != 0 {
		t.Fatalf("register failed: exit %d\n%s", code, out)
	}
	before, err := os.ReadFile(registrationPathFor(env, "shared"))
	if err != nil {
		t.Fatalf("reading record: %v", err)
	}

	second := append(append([]string{}, env...), "MG_ACTOR=passer-by")
	out, code := mgExit(t, bin, second, "mail", "register", "shared", "--json")
	if code != 0 {
		t.Fatalf("re-registering must be exit 0, got %d:\n%s", code, out)
	}
	var reg mailRegisterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reg); err != nil {
		t.Fatalf("register --json is not valid JSON: %v\n%s", err, out)
	}
	if reg.Registered {
		t.Errorf("a re-register wrote no record, so registered must be false: %+v", reg)
	}
	if reg.RegisteredBy != "first-owner" {
		t.Errorf("registered_by = %q, want the record's actual owner %q — reporting the caller's own name is the lie this replaces",
			reg.RegisteredBy, "first-owner")
	}

	after, err := os.ReadFile(registrationPathFor(env, "shared"))
	if err != nil {
		t.Fatalf("reading record: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("re-registering rewrote the record:\nbefore: %s\nafter:  %s", before, after)
	}
}

// ---------------------------------------------------------------------------
// The `daniel` case: a box in use that nobody established.
// ---------------------------------------------------------------------------

// TestCLI_MailListMarksABoxNobodyEstablished is the whole ask, stated as a
// test: after the fact, an unregistered box must be distinguishable from a
// registered one. Both exist, both take mail, both work.
func TestCLI_MailListMarksABoxNobodyEstablished(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "properly-setup")
	legacyBox(t, bin, env, "daniel", 2)

	out, code := mgExit(t, bin, env, "mail", "list")
	if code != 0 {
		t.Fatalf("mail list failed: exit %d\n%s", code, out)
	}
	var danielLine, setupLine string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "daniel"):
			danielLine = line
		case strings.Contains(line, "properly-setup"):
			setupLine = line
		}
	}
	if !strings.Contains(danielLine, "UNREGISTERED") {
		t.Errorf("a box nobody established must be marked, got %q\nfull output:\n%s", danielLine, out)
	}
	if strings.Contains(setupLine, "UNREGISTERED") {
		t.Errorf("a registered box must NOT be marked, got %q", setupLine)
	}

	// The footer is what a reader scrolling past a thousand rows actually
	// sees, and it must name the way to close the gap.
	if !strings.Contains(out, "1 of 2 mailboxes is UNREGISTERED") {
		t.Errorf("the listing must count what it marked, got:\n%s", out)
	}
	if !strings.Contains(out, "mg mail register") {
		t.Errorf("the footer must name the command that fixes it, got:\n%s", out)
	}
	// And it must not read as a refusal: nothing is being blocked, and a
	// reader who concludes their mail is bouncing will go chase the wrong
	// thing entirely.
	if !strings.Contains(out, "Nothing is refused") {
		t.Errorf("the footer must say mail is still delivered, got:\n%s", out)
	}
}

// TestCLI_MailListSaysNothingWhenEveryBoxIsAccountedFor: the footer is a
// finding, not furniture. A store with nothing to report must print nothing,
// or the notice becomes the wallpaper "(new mailbox created)" became.
func TestCLI_MailListSaysNothingWhenEveryBoxIsAccountedFor(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor", "architect")

	out, code := mgExit(t, bin, env, "mail", "list")
	if code != 0 {
		t.Fatalf("mail list failed: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "UNREGISTERED") {
		t.Errorf("a fully registered store must print no notice, got:\n%s", out)
	}
}

// TestCLI_MailRegisterAdoptsABoxAlreadyInUse: "Mailbox X is already registered"
// was a false statement for every box that merely existed — which was all of
// them. Registering an existing box now performs the registration it never had,
// says the box was in use unregistered, and records how much mail it inherited
// WITHOUT claiming to vouch for it.
func TestCLI_MailRegisterAdoptsABoxAlreadyInUse(t *testing.T) {
	bin, env := mailInit(t)
	legacyBox(t, bin, env, "daniel", 3)
	env = append(env, "MG_ACTOR=operator")

	out, code := mgExit(t, bin, env, "mail", "register", "daniel")
	if code != 0 {
		t.Fatalf("adopting failed: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "is already registered") {
		t.Fatalf("a box nobody registered must not be reported as already registered, got:\n%s", out)
	}
	if !strings.Contains(out, "UNREGISTERED") {
		t.Errorf("the adoption must say the box was in use unregistered, got:\n%s", out)
	}
	if !strings.Contains(out, "3 messages") {
		t.Errorf("the adoption must say how much mail it inherited, got:\n%s", out)
	}

	out, code = mgExit(t, bin, env, "mail", "register", "daniel", "--json")
	if code != 0 {
		t.Fatalf("re-register failed: exit %d\n%s", code, out)
	}
	var reg mailRegisterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reg); err != nil {
		t.Fatalf("register --json is not valid JSON: %v\n%s", err, out)
	}
	if !reg.Adopted {
		t.Errorf("the record must remember it was an adoption, got %+v", reg)
	}
	if reg.PriorMessages != 3 {
		t.Errorf("prior_messages = %d, want 3 (%+v)", reg.PriorMessages, reg)
	}
	if reg.RegisteredBy != "operator" {
		t.Errorf("registered_by = %q, want operator", reg.RegisteredBy)
	}

	if got := mailboxStandingJSON(t, bin, env, "daniel"); got.Registration != "registered" {
		t.Errorf("after adoption registration = %q, want registered (%+v)", got.Registration, got)
	}
	// Adoption is bookkeeping, not a mail operation: the inherited mail is
	// still there and still unread.
	if got := mailboxStandingJSON(t, bin, env, "daniel"); got.Unread != 3 {
		t.Errorf("adoption must not touch mail, unread = %d want 3", got.Unread)
	}
}

// ---------------------------------------------------------------------------
// Standing is a machine-readable answer, and it is independent of existence.
// ---------------------------------------------------------------------------

// TestCLI_MailListJSONSeparatesStandingFromExistence: exists and registration
// answer different questions, and every combination of them occurs. A consumer
// that could only ask "does it exist" was blind to exactly the case that cost
// four mails.
func TestCLI_MailListJSONSeparatesStandingFromExistence(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "new", "Polecat work", "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	itemBox := strings.TrimPrefix(extractItemID(t, out), "mg-")

	mailRegisterBoxes(t, bin, env, "registered-box")
	legacyBox(t, bin, env, "legacy-box", 1)

	for _, tc := range []struct {
		box              string
		wantExists       bool
		wantRegistration string
		why              string
	}{
		{"registered-box", true, "registered",
			"somebody performed the deliberate act"},
		{"legacy-box", true, "unregistered",
			"it exists only because mail was delivered to it — the daniel case"},
		{itemBox, false, "work-item",
			"no box yet, but a work item vouches for the name: mail sent here is accepted"},
		{"definitely-nobody-9ecf", false, "unregistered",
			"nothing exists and nothing vouches: this is the address send refuses"},
	} {
		got := mailboxStandingJSON(t, bin, env, tc.box)
		if got.Exists != tc.wantExists || got.Registration != tc.wantRegistration {
			t.Errorf("%s: got exists=%v registration=%q, want exists=%v registration=%q (%s)",
				tc.box, got.Exists, got.Registration, tc.wantExists, tc.wantRegistration, tc.why)
		}
	}
}

// TestCLI_MailListPerBoxHumanOutputStaysQuiet: polecats poll their own box
// every ten minutes and their boxes are work-item boxes. A standing line on
// that path would be a nag on the healthy path, and the signal-to-noise
// mg-5168 bought back would go straight out again. The answer stays available
// under --json, where nothing has to read it.
func TestCLI_MailListPerBoxHumanOutputStaysQuiet(t *testing.T) {
	bin, env := mailInit(t)
	legacyBox(t, bin, env, "legacy-box", 1)

	out, code := mgExit(t, bin, env, "mail", "list", "legacy-box")
	if code != 0 {
		t.Fatalf("mail list failed: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "UNREGISTERED") {
		t.Errorf("the per-box listing must not nag on every poll, got:\n%s", out)
	}
}

// TestCLI_MailListFilteredJSONCarriesStanding: the sender-filter summary object
// is a superset of the plain one, and a consumer that switched to --exclude-from
// must not lose the standing field by doing so.
func TestCLI_MailListFilteredJSONCarriesStanding(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "watched")

	out, code := mgExit(t, bin, env, "mail", "list", "watched", "--exclude-from=scheduler", "--json")
	if code != 0 {
		t.Fatalf("filtered list failed: exit %d\n%s", code, out)
	}
	var got mailFilterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("filtered --json is not one JSON object: %v\n%s", err, out)
	}
	if got.Registration != "registered" {
		t.Errorf("filtered summary registration = %q, want registered (%+v)", got.Registration, got)
	}
}

// ---------------------------------------------------------------------------
// --create is an assertion, and assertions are attributable.
// ---------------------------------------------------------------------------

// TestCLI_MailSendCreateRecordsWhoTalkedPastTheRefusal: --create is the
// documented escape and therefore the reachable one. Without a record it
// evaporates the instant the box exists, and the escape hatch quietly becomes
// the way every phantom box gets minted from here on.
func TestCLI_MailSendCreateRecordsWhoTalkedPastTheRefusal(t *testing.T) {
	bin, env := mailInit(t)
	env = append(env, "MG_ACTOR=hasty-agent")

	out, code := mgExit(t, bin, env, "mail", "send", "brandnew", "--create",
		"--from=hasty-agent", "--subject=x", "--body=y")
	if code != 0 {
		t.Fatalf("--create send failed: exit %d\n%s", code, out)
	}

	data, err := os.ReadFile(registrationPathFor(env, "brandnew"))
	if err != nil {
		t.Fatalf("--create must leave a registration record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("record is not valid JSON: %v\n%s", err, data)
	}
	if rec["via"] != "send --create" {
		t.Errorf("via = %v, want %q — a box established while talking past a refusal must be findable as one",
			rec["via"], "send --create")
	}
	if rec["registered_by"] != "hasty-agent" {
		t.Errorf("registered_by = %v, want hasty-agent", rec["registered_by"])
	}
}

// TestCLI_MailSendWithoutCreateManufacturesNoRegistration: the remedy is an
// artifact of the same kind as the defect. A record stamped by an ordinary
// first delivery would be evidence of a step nobody took — the same lie as
// "already registered", told the other way round. A work-item box is legitimate
// BECAUSE the work item vouches for it, and that is what it must report.
func TestCLI_MailSendWithoutCreateManufacturesNoRegistration(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "new", "Polecat work", "--no-repo")
	if code != 0 {
		t.Fatalf("mg new failed: exit %d\n%s", code, out)
	}
	id := extractItemID(t, out)
	box := strings.TrimPrefix(id, "mg-")

	if out, code := mgExit(t, bin, env, "mail", "send", id,
		"--from=mayor", "--subject=Directive", "--body=go"); code != 0 {
		t.Fatalf("send to a work-item name failed: exit %d\n%s", code, out)
	}

	if _, err := os.Stat(registrationPathFor(env, box)); !os.IsNotExist(err) {
		t.Errorf("an ordinary delivery must not write a registration record (err=%v)", err)
	}
	if got := mailboxStandingJSON(t, bin, env, box); got.Registration != "work-item" {
		t.Errorf("registration = %q, want work-item (%+v)", got.Registration, got)
	}

	// And it is not marked in the enumeration: the store is mostly these, and
	// marking them would bury the handful that matter.
	list, _ := mgExit(t, bin, env, "mail", "list")
	if strings.Contains(list, "UNREGISTERED") {
		t.Errorf("a work-item box must not be marked unregistered, got:\n%s", list)
	}
}

// TestCLI_MailSendCreateOnAnExistingBoxVouchesForNothing: --create aimed at a
// box that already exists established nothing, so it cannot vouch for it. The
// box stays marked, which is the outcome that gets a human to look — and it
// stops --create being usable to silence the marker as well as the refusal.
func TestCLI_MailSendCreateOnAnExistingBoxVouchesForNothing(t *testing.T) {
	bin, env := mailInit(t)
	legacyBox(t, bin, env, "legacy-box", 1)

	if out, code := mgExit(t, bin, env, "mail", "send", "legacy-box", "--create",
		"--from=tester", "--subject=x", "--body=y"); code != 0 {
		t.Fatalf("send failed: exit %d\n%s", code, out)
	}
	if got := mailboxStandingJSON(t, bin, env, "legacy-box"); got.Registration != "unregistered" {
		t.Errorf("registration = %q, want unregistered — --create on an existing box establishes nothing (%+v)",
			got.Registration, got)
	}
}

// TestCLI_MailReplyCreateRecordsItsOwnSpelling: a reply's recipient is a From
// header the sender wrote, so it carries the same phantom-box risk and gets the
// same escape. Its record says which spelling was used, because "who answered
// into a name nobody could receive at" is a different question from "who sent
// to one".
func TestCLI_MailReplyCreateRecordsItsOwnSpelling(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "inbox")

	if out, code := mgExit(t, bin, env, "mail", "send", "inbox", "--create",
		"--from=ghost-sender", "--subject=hello", "--body=hi"); code != 0 {
		t.Fatalf("seed send failed: exit %d\n%s", code, out)
	}
	listing, code := mgExit(t, bin, env, "mail", "list", "inbox", "--json")
	if code != 0 {
		t.Fatalf("listing failed: exit %d\n%s", code, listing)
	}
	var msg mailMsgJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.Split(listing, "\n")[0])), &msg); err != nil {
		t.Fatalf("listing is not JSON: %v\n%s", err, listing)
	}

	// Replying without --create is refused: the From header names a box that
	// does not exist. With it, the record says how the box came to be.
	if out, code := mgExit(t, bin, env, "mail", "reply", "inbox/"+msg.ID, "--body=ok"); code == 0 {
		t.Fatalf("replying to an unreachable sender must be refused, got exit 0:\n%s", out)
	}
	if out, code := mgExit(t, bin, env, "mail", "reply", "inbox/"+msg.ID, "--create", "--body=ok"); code != 0 {
		t.Fatalf("reply --create failed: exit %d\n%s", code, out)
	}

	data, err := os.ReadFile(registrationPathFor(env, "ghost-sender"))
	if err != nil {
		t.Fatalf("reply --create must leave a registration record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("record is not valid JSON: %v\n%s", err, data)
	}
	if rec["via"] != "reply --create" {
		t.Errorf("via = %v, want %q", rec["via"], "reply --create")
	}
}

// ---------------------------------------------------------------------------
// Following the listing's own advice must not mint a phantom box.
// ---------------------------------------------------------------------------

// TestCLI_MailRegisterRefusesAStrayPrefixedBox: this is the remedy caught
// exhibiting the defect it remedies. `mg mail list` marks 463 prefixed strays
// UNREGISTERED and says "run 'mg mail register NAME'". Names are canonicalized,
// so doing that registered "01ce" rather than "cat-mg-01ce": it minted a NEW
// empty mailbox, reported success, and left the box the caller pointed at
// holding its mail and still marked. A phantom box, created by following the
// instructions, reported as a registration.
func TestCLI_MailRegisterRefusesAStrayPrefixedBox(t *testing.T) {
	bin, env := mailInit(t)

	// A stray box on disk under its prefixed name, as the pre-canonicalization
	// deliveries left them.
	mailDir := filepath.Join(homeFromEnv(env), ".macguffin", "mail", "cat-mg-01ce")
	for _, sub := range []string{"new", "cur", "tmp"} {
		if err := os.MkdirAll(filepath.Join(mailDir, sub), 0o755); err != nil {
			t.Fatalf("seeding stray box: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(mailDir, "new", "1.1.1"),
		[]byte("From: x\nSubject: s\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("seeding stray message: %v", err)
	}

	out, code := mgExit(t, bin, env, "mail", "register", "cat-mg-01ce")
	if code == 0 {
		t.Fatalf("registering a stray must be refused, got exit 0:\n%s", out)
	}
	if code != 4 {
		t.Errorf("stray registration should be a conflict (exit 4), got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "mg mail migrate") {
		t.Errorf("the refusal must name the command that actually merges a stray, got:\n%s", out)
	}
	if !strings.Contains(out, "01ce") {
		t.Errorf("the refusal must say which name it would have registered instead, got:\n%s", out)
	}

	// The point of refusing: no phantom twin was created.
	if _, err := os.Stat(filepath.Join(homeFromEnv(env), ".macguffin", "mail", "01ce")); !os.IsNotExist(err) {
		t.Errorf("a refused stray registration must not mint the canonical box (err=%v)", err)
	}

	// And the listing points strays at migrate rather than at a refusal.
	list, _ := mgExit(t, bin, env, "mail", "list")
	if !strings.Contains(list, "STRAYS") {
		t.Errorf("the listing must separate strays from names worth adopting, got:\n%s", list)
	}
}

// TestCLI_MailRegisterStillCanonicalizesANameThatIsNotOnDisk: the refusal is
// narrow. Registering the alias "mg-<id>" when no such box exists must keep
// reserving the canonical box, which is the behaviour that stops a registration
// minting the stray twin in the first place.
func TestCLI_MailRegisterStillCanonicalizesANameThatIsNotOnDisk(t *testing.T) {
	bin, env := mailInit(t)

	out, code := mgExit(t, bin, env, "mail", "register", "mg-abcd")
	if code != 0 {
		t.Fatalf("registering an alias with no box on disk must succeed, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "abcd") {
		t.Errorf("the alias must reserve the canonical box, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homeFromEnv(env), ".macguffin", "mail", "mg-abcd")); !os.IsNotExist(err) {
		t.Errorf("registering an alias must not create a box under the alias name (err=%v)", err)
	}
}

// ---------------------------------------------------------------------------
// The record survives its own failure modes.
// ---------------------------------------------------------------------------

// TestCLI_MailStandingSurvivesADamagedRecord: the remedy is subject to the
// defect it remedies. PRESENCE is the registration; the contents are detail. A
// truncated record must not be read as "never registered" — that would turn a
// damaged file into a silent retraction of the very fact it was written to
// record, which is the defect again wearing a different hat.
func TestCLI_MailStandingSurvivesADamagedRecord(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "damaged")

	if err := os.WriteFile(registrationPathFor(env, "damaged"), []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("damaging the record: %v", err)
	}

	if got := mailboxStandingJSON(t, bin, env, "damaged"); got.Registration != "registered" {
		t.Errorf("registration = %q, want registered — a damaged record loses detail, not the fact (%+v)",
			got.Registration, got)
	}

	out, code := mgExit(t, bin, env, "mail", "register", "damaged")
	if code != 0 {
		t.Fatalf("register on a damaged record failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "unreadable") {
		t.Errorf("a damaged record must say its detail is lost rather than inventing one, got:\n%s", out)
	}
}

// TestCLI_MailRegistrationRecordIsNotMistakenForMail: the record lives inside
// the mailbox directory, where every mail-counting path walks. If it were ever
// counted as a message, the fix would have invented a phantom mail in every box
// it touched.
func TestCLI_MailRegistrationRecordIsNotMistakenForMail(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "quiet")

	if got := mailboxStandingJSON(t, bin, env, "quiet"); got.Unread != 0 {
		t.Errorf("a registered empty box must have 0 unread, got %d", got.Unread)
	}
	out, code := mgExit(t, bin, env, "mail", "list", "quiet")
	if code != 0 {
		t.Fatalf("mail list failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "No unread messages") {
		t.Errorf("a registered empty box must read as empty, got:\n%s", out)
	}
	// And it does not become a mailbox of its own in the enumeration.
	list, _ := mgExit(t, bin, env, "mail", "list")
	if strings.Contains(list, "registration") {
		t.Errorf("the record must not appear as a mailbox, got:\n%s", list)
	}
}

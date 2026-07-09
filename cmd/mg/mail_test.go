package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMail runs `mg mail ...` against the given env and returns stdout, stderr,
// and the exit error (nil on exit 0).
func runMail(t *testing.T, bin string, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"mail"}, args...)...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// mailInit builds the binary, sets up a temp HOME, and runs `mg init`.
func mailInit(t *testing.T) (bin string, env []string) {
	t.Helper()
	tmpHome := t.TempDir()
	bin = buildBinary(t)
	env = mailTestEnv(tmpHome)
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}
	return bin, env
}

// TestCLI_MailSendUnknownRecipientSoftWarn: sending to a never-seen recipient
// succeeds (exit 0) but notes the new mailbox; a second send to the same box
// no longer notes it (#49).
func TestCLI_MailSendUnknownRecipientSoftWarn(t *testing.T) {
	bin, env := mailInit(t)

	out, _, err := runMail(t, bin, env, "send", "typoo", "--from=mayor", "--subject=s", "--body=b")
	if err != nil {
		t.Fatalf("first send should succeed (exit 0), got err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(new mailbox created)") {
		t.Errorf("first send to unknown recipient should note new mailbox, got: %s", out)
	}

	out, _, err = runMail(t, bin, env, "send", "typoo", "--from=mayor", "--subject=s2", "--body=b2")
	if err != nil {
		t.Fatalf("second send failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "new mailbox created") {
		t.Errorf("second send to existing mailbox must NOT note new mailbox, got: %s", out)
	}
}

// TestCLI_MailListNeverExistedVsEmpty: an existing-but-empty mailbox and a
// never-existed one produce distinct human output, both exit 0 (#49).
func TestCLI_MailListNeverExistedVsEmpty(t *testing.T) {
	bin, env := mailInit(t)

	// Never existed.
	out, _, err := runMail(t, bin, env, "list", "ghost")
	if err != nil {
		t.Fatalf("list of never-existed mailbox should exit 0, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No mailbox for ghost") {
		t.Errorf("never-existed mailbox should be called out distinctly, got: %s", out)
	}

	// Existing but empty: send then read the only message.
	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	// Grab the msg id from list, then read it so new/ empties.
	listOut, _, _ := runMail(t, bin, env, "list", "arch")
	fields := strings.Fields(listOut)
	var idTok string
	for _, f := range fields {
		if strings.HasPrefix(f, "arch/") {
			idTok = f
			break
		}
	}
	if idTok == "" {
		t.Fatalf("could not find message id token in: %s", listOut)
	}
	if _, _, err := runMail(t, bin, env, "read", idTok); err != nil {
		t.Fatalf("read failed: %v", err)
	}

	out, _, err = runMail(t, bin, env, "list", "arch")
	if err != nil {
		t.Fatalf("list of existing-empty mailbox failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No unread messages for arch") {
		t.Errorf("existing-empty mailbox should say 'No unread messages', got: %s", out)
	}
	if strings.Contains(out, "No mailbox for") {
		t.Errorf("existing-empty mailbox must not be reported as never-existed, got: %s", out)
	}
}

// TestCLI_MailListNoArgEnumeratesMailboxes: no-arg `mg mail list` lists every
// mailbox with unread counts (#53).
func TestCLI_MailListNoArgEnumeratesMailboxes(t *testing.T) {
	bin, env := mailInit(t)

	// Empty root (only whatever init created) — should not error.
	if _, _, err := runMail(t, bin, env, "list"); err != nil {
		t.Fatalf("no-arg list on fresh workspace failed: %v", err)
	}

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if _, _, err := runMail(t, bin, env, "send", "witness", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	out, _, err := runMail(t, bin, env, "list")
	if err != nil {
		t.Fatalf("no-arg list failed: %v\n%s", err, out)
	}
	for _, box := range []string{"arch", "witness"} {
		if !strings.Contains(out, box) {
			t.Errorf("no-arg list should enumerate %s, got: %s", box, out)
		}
	}
	if !strings.Contains(out, "unread") {
		t.Errorf("no-arg list should show unread counts, got: %s", out)
	}

	// --all/--archived with no agent is a clear error.
	if _, _, err := runMail(t, bin, env, "list", "--archived"); err == nil {
		t.Error("no-arg list --archived should error")
	}
}

// TestCLI_MailListJSON: per-mailbox list --json emits stable message NDJSON;
// no-arg list --json emits per-mailbox NDJSON (#50).
func TestCLI_MailListJSON(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=Hello", "--body=world"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Per-mailbox message NDJSON.
	out, _, err := runMail(t, bin, env, "list", "arch", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v\n%s", err, out)
	}
	var msg mailMsgJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &msg); err != nil {
		t.Fatalf("list --json line not valid JSON: %v\n%s", err, out)
	}
	if msg.From != "mayor" || msg.Subject != "Hello" || msg.ID == "" || msg.Read {
		t.Errorf("unexpected message json: %+v", msg)
	}

	// No-arg mailbox NDJSON.
	out, _, err = runMail(t, bin, env, "list", "--json")
	if err != nil {
		t.Fatalf("no-arg list --json failed: %v\n%s", err, out)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var box mailboxJSON
		if err := json.Unmarshal([]byte(line), &box); err != nil {
			t.Fatalf("mailbox json line invalid: %v\n%s", err, line)
		}
		if box.Mailbox == "arch" {
			found = true
			if box.Unread != 1 || !box.Exists {
				t.Errorf("arch mailbox json = %+v, want unread 1 exists true", box)
			}
		}
	}
	if !found {
		t.Errorf("no-arg list --json should include arch, got: %s", out)
	}
}

// TestCLI_MailSendJSONMailboxCreated: send --json carries mailbox_created,
// true on first delivery and false thereafter — the architect C1 field (#50).
func TestCLI_MailSendJSONMailboxCreated(t *testing.T) {
	bin, env := mailInit(t)

	out, _, err := runMail(t, bin, env, "send", "fresh", "--from=mayor", "--subject=s", "--body=b", "--json")
	if err != nil {
		t.Fatalf("send --json failed: %v\n%s", err, out)
	}
	var first mailSendJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &first); err != nil {
		t.Fatalf("send --json not valid JSON: %v\n%s", err, out)
	}
	if !first.MailboxCreated {
		t.Errorf("first send to new mailbox should set mailbox_created=true, got: %+v", first)
	}
	if first.To != "fresh" || first.From != "mayor" || first.MsgID == "" {
		t.Errorf("unexpected send json: %+v", first)
	}

	out, _, err = runMail(t, bin, env, "send", "fresh", "--from=mayor", "--subject=s2", "--body=b2", "--json")
	if err != nil {
		t.Fatalf("second send --json failed: %v\n%s", err, out)
	}
	var second mailSendJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &second); err != nil {
		t.Fatalf("second send --json not valid JSON: %v\n%s", err, out)
	}
	if second.MailboxCreated {
		t.Errorf("second send to existing mailbox should set mailbox_created=false, got: %+v", second)
	}
}

// TestCLI_MailReadArchiveJSON: read and archive emit their single-object JSON
// shapes (#50).
func TestCLI_MailReadArchiveJSON(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=Subj", "--body=Body text"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(homeFromEnv(env), ".macguffin", "mail", "arch", "new"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 message in new/, got %d (err %v)", len(entries), err)
	}
	msgID := entries[0].Name()

	out, _, err := runMail(t, bin, env, "read", "arch", msgID, "--json")
	if err != nil {
		t.Fatalf("read --json failed: %v\n%s", err, out)
	}
	var rd mailReadJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rd); err != nil {
		t.Fatalf("read --json not valid JSON: %v\n%s", err, out)
	}
	if rd.ID != msgID || rd.Subject != "Subj" || rd.Body != "Body text" || !rd.Read {
		t.Errorf("unexpected read json: %+v", rd)
	}

	out, _, err = runMail(t, bin, env, "archive", "arch", msgID, "--json")
	if err != nil {
		t.Fatalf("archive --json failed: %v\n%s", err, out)
	}
	var ar mailArchiveJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ar); err != nil {
		t.Fatalf("archive --json not valid JSON: %v\n%s", err, out)
	}
	if ar.ID != msgID || ar.Mailbox != "arch" || ar.Subject != "Subj" {
		t.Errorf("unexpected archive json: %+v", ar)
	}
}

// homeFromEnv extracts the HOME value from a mailTestEnv slice.
func homeFromEnv(env []string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			return strings.TrimPrefix(kv, "HOME=")
		}
	}
	return ""
}

// TestCLI_MailListMalformedWarning: a truncated file in new/ must produce a
// visible warning in mg mail list output, not a silent skip (mg-9696).
func TestCLI_MailListMalformedWarning(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := mailTestEnv(tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "mail", "send", "arch", "--from=mayor", "--subject=Good one", "--body=intact")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg mail send failed: %v\n%s", err, out)
	}

	// Simulate a truncated transfer: headers cut off mid-line, no separator.
	newDir := filepath.Join(tmpHome, ".macguffin", "mail", "arch", "new")
	if err := os.WriteFile(filepath.Join(newDir, "777.1.777"), []byte("From: mayor\nSubj"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command(bin, "mail", "list", "arch")
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg mail list failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "Good one") {
		t.Errorf("list should still show the intact message, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "1 malformed message(s) skipped") {
		t.Errorf("list output should surface the malformed count, got: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "skipping malformed message") {
		t.Errorf("stderr should carry the per-file warning, got: %s", stderr.String())
	}

	// The skip must also be recorded in the event log.
	cmd = exec.Command(bin, "event", "list", "--type=mail.malformed")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg event list failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "777.1.777") {
		t.Errorf("expected mail.malformed event for 777.1.777, got: %s", out)
	}
}

// TestCLI_MailLifecycleEvents: send/read/archive must each land a structured
// event in <workspace>/events.jsonl (mg-9696).
func TestCLI_MailLifecycleEvents(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := mailTestEnv(tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "mail", "send", "arch", "--from=mayor", "--subject=Trace me", "--body=body")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg mail send failed: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(filepath.Join(tmpHome, ".macguffin", "mail", "arch", "new"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 message in new/, got %d (err %v)", len(entries), err)
	}
	msgID := entries[0].Name()

	for _, verb := range []string{"read", "archive"} {
		cmd = exec.Command(bin, "mail", verb, "arch", msgID)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mg mail %s failed: %v\n%s", verb, err, out)
		}
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".macguffin", "events.jsonl"))
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}
	for _, eventType := range []string{"mail.sent", "mail.read", "mail.archived"} {
		if !strings.Contains(string(data), `"type":"`+eventType+`"`) {
			t.Errorf("events.jsonl missing %s event; contents:\n%s", eventType, data)
		}
	}
	if !strings.Contains(string(data), msgID) {
		t.Errorf("events.jsonl should reference msg_id %s; contents:\n%s", msgID, data)
	}
}

// TestCLI_MailReadRejectsTraversalMsgID: a crafted MSG-ID must not read a file
// outside the mailbox, in either argument form, and must exit 2 (usage) rather
// than 0. Regression for the mg-ea5a path-traversal surface.
func TestCLI_MailReadRejectsTraversalMsgID(t *testing.T) {
	bin, env := mailInit(t)

	// Plant a parseable message file in the workspace root, one level above the
	// mail root. Unsanitized, filepath.Join(<ws>/mail, "arch", "new",
	// "../../../secret") resolves straight onto it.
	var home string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	secret := filepath.Join(home, ".macguffin", "secret")
	if err := os.WriteFile(secret, []byte("From: x\nSubject: s\nDate: d\n\ntop secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give the agent a real mailbox so only the MSG-ID is at fault.
	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}

	for _, args := range [][]string{
		{"read", "arch", "../../../secret"},
		{"read", "arch/../../../secret"},
		{"read", "arch", ".."},
		{"archive", "arch", "../../../secret"},
	} {
		out, errOut, err := runMail(t, bin, env, args...)
		if err == nil {
			t.Errorf("%v: exited 0, want rejection (stdout=%q)", args, out)
			continue
		}
		if strings.Contains(out, "top secret") {
			t.Errorf("%v: leaked out-of-mailbox file content", args)
		}
		if got := exitCodeOf(err); got != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage); stderr=%q", args, got, errOut)
		}
	}
}

// exitCodeOf extracts the process exit code from an *exec.ExitError.
func exitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// --- correlation headers: Message-Id / --in-reply-to / mail reply ----------

// homeOf extracts HOME from a test env built by mailTestEnv.
func homeOf(t *testing.T, env []string) string {
	t.Helper()
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			return strings.TrimPrefix(kv, "HOME=")
		}
	}
	t.Fatal("no HOME in test env")
	return ""
}

// mailboxDir is the maildir subdirectory for an agent under the test HOME.
func mailboxDir(t *testing.T, env []string, agent, sub string) string {
	t.Helper()
	return filepath.Join(homeOf(t, env), ".macguffin", "mail", agent, sub)
}

// soleMsgID returns the id of the single unread message in agent's mailbox,
// failing if there is not exactly one.
func soleMsgID(t *testing.T, env []string, agent string) string {
	t.Helper()
	entries, err := os.ReadDir(mailboxDir(t, env, agent, "new"))
	if err != nil {
		t.Fatalf("reading %s's new/: %v", agent, err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s has %d unread messages, want exactly 1", agent, len(entries))
	}
	return entries[0].Name()
}

// msgFile reads the raw message file for agent/msgID out of new/ or cur/.
func msgFile(t *testing.T, env []string, agent, msgID string) string {
	t.Helper()
	for _, sub := range []string{"new", "cur"} {
		if data, err := os.ReadFile(filepath.Join(mailboxDir(t, env, agent, sub), msgID)); err == nil {
			return string(data)
		}
	}
	t.Fatalf("message %s/%s not found in new/ or cur/", agent, msgID)
	return ""
}

// asAgent returns env with POGO_AGENT_NAME set, so cross-box guards see the
// caller as that agent.
func asAgent(env []string, agent string) []string {
	return append(append([]string{}, env...), "POGO_AGENT_NAME="+agent)
}

// TestCLI_MailSendStampsMessageID: the Message-Id header on a delivered message
// equals its MSG-ID, which is its file name.
func TestCLI_MailSendStampsMessageID(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	id := soleMsgID(t, env, "arch")
	if want := "Message-Id: " + id + "\n"; !strings.Contains(msgFile(t, env, "arch", id), want) {
		t.Errorf("message file missing %q:\n%s", want, msgFile(t, env, "arch", id))
	}
}

// TestCLI_MailSendRejectsHeaderInjection is the CLI-level regression test for
// the CR/LF injection. A newline in --subject or --from used to append
// attacker-chosen headers (an In-Reply-To among them, i.e. thread hijacking).
// It must now exit 2 (usage) with no message written and no mailbox created.
func TestCLI_MailSendRejectsHeaderInjection(t *testing.T) {
	injected := "pwned\nIn-Reply-To: attacker.id.1\nX-Evil: yes"

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"subject", []string{"send", "victim", "--from=mayor", "--subject=" + injected, "--body=b"}},
		{"from", []string{"send", "victim", "--from=" + injected, "--subject=s", "--body=b"}},
		{"in-reply-to", []string{"send", "victim", "--from=mayor", "--subject=s", "--body=b", "--in-reply-to=a\nX-Evil: yes"}},
		{"in-reply-to traversal", []string{"send", "victim", "--from=mayor", "--subject=s", "--body=b", "--in-reply-to=../../../etc/passwd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, env := mailInit(t)

			out, errOut, err := runMail(t, bin, env, tc.args...)
			if err == nil {
				t.Fatalf("send accepted an injected header value (stdout=%q)", out)
			}
			if got := exitCodeOf(err); got != 2 {
				t.Errorf("exit = %d, want 2 (usage); stderr=%q", got, errOut)
			}
			if !strings.Contains(errOut, "invalid") {
				t.Errorf("stderr does not explain the rejection: %q", errOut)
			}

			// Nothing may reach disk — not the message, not the lazily
			// created mailbox.
			if _, err := os.Stat(filepath.Join(homeOf(t, env), ".macguffin", "mail", "victim")); !os.IsNotExist(err) {
				t.Errorf("rejected send created the recipient mailbox (stat err = %v)", err)
			}
		})
	}
}

// TestCLI_MailSendInReplyTo: the explicit primitive stamps In-Reply-To and
// seeds References, and --json still carries msg_id (plus the new in_reply_to).
func TestCLI_MailSendInReplyTo(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=first", "--body=b"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}
	parent := soleMsgID(t, env, "arch")

	out, _, err := runMail(t, bin, env, "send", "mayor", "--from=arch", "--subject=answer", "--body=b2",
		"--in-reply-to="+parent, "--json")
	if err != nil {
		t.Fatalf("send --in-reply-to failed: %v\n%s", err, out)
	}

	var got mailSendJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	if got.MsgID == "" {
		t.Error("--json dropped msg_id")
	}
	if got.InReplyTo != parent {
		t.Errorf("in_reply_to = %q, want %q", got.InReplyTo, parent)
	}

	file := msgFile(t, env, "mayor", got.MsgID)
	for _, want := range []string{
		"In-Reply-To: " + parent + "\n",
		"References: " + parent + "\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("message file missing %q:\n%s", want, file)
		}
	}
}

// TestCLI_MailReply: the ergonomic wrapper resolves recipient, subject and
// correlation headers from the original, marks the original read, and leaves it
// un-archived.
func TestCLI_MailReply(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=Review needed", "--body=please review"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}
	orig := soleMsgID(t, env, "arch")

	out, _, err := runMail(t, bin, asAgent(env, "arch"), "reply", "arch/"+orig, "--body=on it", "--json")
	if err != nil {
		t.Fatalf("reply failed: %v\n%s", err, out)
	}

	var got mailSendJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	if got.To != "mayor" {
		t.Errorf("to = %q, want mayor (the original's From)", got.To)
	}
	if got.From != "arch" {
		t.Errorf("from = %q, want arch (defaults to the replying mailbox)", got.From)
	}
	if got.InReplyTo != orig {
		t.Errorf("in_reply_to = %q, want %q", got.InReplyTo, orig)
	}

	file := msgFile(t, env, "mayor", got.MsgID)
	for _, want := range []string{
		"From: arch\n",
		"Subject: Re: Review needed\n",
		"In-Reply-To: " + orig + "\n",
		"References: " + orig + "\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("reply missing %q:\n%s", want, file)
		}
	}

	// The original is marked read (new/ -> cur/) but NOT archived.
	if _, err := os.Stat(filepath.Join(mailboxDir(t, env, "arch", "cur"), orig)); err != nil {
		t.Errorf("reply did not mark the original read: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mailboxDir(t, env, "arch", "new"), orig)); !os.IsNotExist(err) {
		t.Errorf("original still in new/ after reply")
	}
	if entries, err := os.ReadDir(mailboxDir(t, env, "arch", "archive")); err == nil && len(entries) > 0 {
		t.Errorf("reply auto-archived the original (%d file(s)); it must not", len(entries))
	}
}

// TestCLI_MailReplyRejectsCrossBox: reply marks the original read for its
// owner, exactly as 'mail read' does, so it inherits the same --force guard.
func TestCLI_MailReplyRejectsCrossBox(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}
	orig := soleMsgID(t, env, "arch")

	_, errOut, err := runMail(t, bin, asAgent(env, "intruder"), "reply", "arch/"+orig, "--body=mine now")
	if err == nil {
		t.Fatal("cross-box reply succeeded; want refusal")
	}
	if !strings.Contains(errOut, "--force") {
		t.Errorf("refusal does not mention --force: %q", errOut)
	}
	// Refused before touching the message: still unread.
	if _, err := os.Stat(filepath.Join(mailboxDir(t, env, "arch", "new"), orig)); err != nil {
		t.Errorf("refused reply still marked the original read: %v", err)
	}

	// With --force it goes through.
	if _, _, err := runMail(t, bin, asAgent(env, "intruder"), "reply", "arch/"+orig, "--body=ok", "--force"); err != nil {
		t.Fatalf("forced cross-box reply failed: %v", err)
	}
}

// TestCLI_MailReplyThreadCapsReferences: a thread longer than the References
// cap keeps only the most recent ids, so message files stay small. The parent
// is always retained — that is what threading reads.
func TestCLI_MailReplyThreadCapsReferences(t *testing.T) {
	bin, env := mailInit(t)

	// Open the thread, then ping-pong replies between arch and mayor. Each
	// reply extends References by exactly one id (its parent).
	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=thread", "--body=0"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}

	const rounds = 25 // comfortably past the cap of 20
	box, peer := "arch", "mayor"
	var lastID, lastBox string
	for i := 0; i < rounds; i++ {
		parent := soleMsgID(t, env, box)
		if _, _, err := runMail(t, bin, asAgent(env, box), "reply", box+"/"+parent, "--body=r"); err != nil {
			t.Fatalf("round %d reply from %s failed: %v", i, box, err)
		}
		lastBox, lastID = peer, soleMsgID(t, env, peer)
		box, peer = peer, box
	}

	file := msgFile(t, env, lastBox, lastID)
	var refs []string
	for _, line := range strings.Split(file, "\n") {
		if v, ok := strings.CutPrefix(line, "References: "); ok {
			refs = strings.Fields(v)
		}
	}
	if len(refs) != 20 {
		t.Fatalf("References carries %d ids, want the cap of 20:\n%s", len(refs), file)
	}
	// The newest reference is the message this one replies to.
	if want := "In-Reply-To: " + refs[len(refs)-1] + "\n"; !strings.Contains(file, want) {
		t.Errorf("newest reference is not the parent; missing %q:\n%s", want, file)
	}
}

// TestCLI_MailReadJSONExposesCorrelation: `mail read --json` surfaces the
// correlation headers so a scripted consumer can rebuild a thread. They are
// empty/[] for an unthreaded message.
func TestCLI_MailReadJSONExposesCorrelation(t *testing.T) {
	bin, env := mailInit(t)

	if _, _, err := runMail(t, bin, env, "send", "arch", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}
	orig := soleMsgID(t, env, "arch")

	if _, _, err := runMail(t, bin, asAgent(env, "arch"), "reply", "arch/"+orig, "--body=r"); err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	replyID := soleMsgID(t, env, "mayor")

	out, _, err := runMail(t, bin, asAgent(env, "mayor"), "read", "mayor/"+replyID, "--json")
	if err != nil {
		t.Fatalf("read --json failed: %v\n%s", err, out)
	}
	var got mailReadJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	if got.InReplyTo != orig {
		t.Errorf("in_reply_to = %q, want %q", got.InReplyTo, orig)
	}
	if len(got.References) != 1 || got.References[0] != orig {
		t.Errorf("references = %v, want [%s]", got.References, orig)
	}

	// An unthreaded message reports empty correlation, and references is [] —
	// never null — so consumers can index it without a nil check.
	if _, _, err := runMail(t, bin, env, "send", "solo", "--from=mayor", "--subject=s", "--body=b"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	soloID := soleMsgID(t, env, "solo")
	out, _, err = runMail(t, bin, asAgent(env, "solo"), "read", "solo/"+soloID, "--json")
	if err != nil {
		t.Fatalf("read --json failed: %v", err)
	}
	if !strings.Contains(out, `"references":[]`) {
		t.Errorf("unthreaded message should emit references:[], got %q", out)
	}
}

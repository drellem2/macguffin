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

package mgcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hazardBody carries exactly the constructs that the shell eats inside
// --body="...": backticked terms (the 14/17 Jul incidents), $VAR, and $(cmd).
// Every incident in mg-7850's ledger lost precisely this kind of content —
// figures, commands, identifiers — because that is what contains
// metacharacters.
const hazardBody = "pa's feed poller is `com.pogo.pa-heyfeed`, running `poll-hey.sh` as a loop.\n" +
	"Ask at or below $60k. Set $HOME/$UNSET_VAR and run $(date) plus `nudge \"1\"`.\n"

func writeBodyFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// runSh runs a command line through a REAL shell, which is the only way to
// exercise the hazard this ticket exists for: exec.Command never invokes a
// shell, so a test that used it would pass whether or not --body-file works.
func runSh(t *testing.T, env []string, line string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// readBodyJSON reads a delivered message and returns its body via --json, which
// round-trips the stored bytes without the human formatter in the way.
func readBodyJSON(t *testing.T, bin string, env []string, agent, msgID string) string {
	t.Helper()
	out, stderr, err := runMail(t, bin, env, "read", agent+"/"+msgID, "--json", "--force")
	if err != nil {
		t.Fatalf("mail read failed: %v\n%s\n%s", err, out, stderr)
	}
	var got mailReadJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding mail read --json: %v\n%s", err, out)
	}
	return got.Body
}

// TestCLI_MailSendBodyFileVerbatimThroughShell is the ticket's central claim:
// a body full of shell metacharacters, sent with --body-file THROUGH A REAL
// SHELL, arrives unexpanded and byte-identical.
//
// The --body control below is what gives this test teeth. It proves the shell
// really does mangle this body on the inline path, so the --body-file assertion
// is passing because --body-file bypasses the shell — not because the fixture
// happened to be inert. Remove the file-reading path and this test goes red.
func TestCLI_MailSendBodyFileVerbatimThroughShell(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")
	dir := t.TempDir()
	path := writeBodyFile(t, dir, "msg.md", hazardBody)

	// --body-file: no shell in the body's path at all.
	out, stderr, err := runSh(t, env, bin+" mail send mayor --from=me --subject=s --body-file "+path)
	if err != nil {
		t.Fatalf("send --body-file failed: %v\n%s\n%s", err, out, stderr)
	}
	fileMsgID := lastMsgID(t, out)

	// --body: the same bytes handed to the shell inline, as callers reach for.
	out, stderr, err = runSh(t, env, bin+` mail send mayor --from=me --subject=s --body="`+hazardBody+`"`)
	if err != nil {
		t.Fatalf("send --body failed: %v\n%s\n%s", err, out, stderr)
	}
	inlineMsgID := lastMsgID(t, out)

	// Body is stored trimmed (see bodyMetrics in mail.go), so compare against
	// the trimmed fixture: the trailing newline is the store's normalization,
	// not expansion.
	want := strings.TrimSpace(hazardBody)
	if got := readBodyJSON(t, bin, env, "mayor", fileMsgID); got != want {
		t.Errorf("--body-file must deliver the file's bytes verbatim.\n want: %q\n  got: %q", want, got)
	}

	// The control: assert the inline path DID get mangled by the shell. If this
	// ever fails, the fixture stopped being hazardous and the check above is no
	// longer proving anything — fix the fixture, do not delete this.
	if got := readBodyJSON(t, bin, env, "mayor", inlineMsgID); got == want {
		t.Fatalf("control failed: the shell did not mangle --body, so this test cannot prove --body-file bypasses it")
	}
}

// lastMsgID pulls the MSG-ID out of a "Delivered: me → mayor/new/<id>" line.
func lastMsgID(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "/new/")
	if i < 0 {
		t.Fatalf("no delivered msg id in output: %q", out)
	}
	id := out[i+len("/new/"):]
	return strings.TrimSpace(strings.SplitN(id, " ", 2)[0])
}

// TestCLI_BodyFileMissingPathFailsLoudly: an unreadable --body-file must be a
// loud error, never a silent empty body. An empty body that exits 0 is exactly
// the disease this ticket exists to cure.
func TestCLI_BodyFileMissingPathFailsLoudly(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")
	missing := filepath.Join(t.TempDir(), "nope.md")

	cases := []struct {
		name string
		args []string
	}{
		{"mail send", []string{"mail", "send", "--create", "mayor", "--from=me", "--subject=s", "--body-file=" + missing}},
		{"new", []string{"new", "--title=t", "--body-file=" + missing}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("%s with a missing --body-file must fail, got exit 0:\n%s", tc.name, out)
			}
			if !strings.Contains(string(out), "nope.md") {
				t.Errorf("error must name the unreadable path, got: %s", out)
			}
		})
	}

	// A directory is unreadable-as-a-file and must fail the same way, rather
	// than yielding an empty body.
	cmd := exec.Command(bin, "mail", "send", "--create", "mayor", "--from=me", "--subject=s", "--body-file="+t.TempDir())
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("--body-file naming a directory must fail, got exit 0:\n%s", out)
	}
}

// TestCLI_BodyFileMutuallyExclusiveWithBody: passing both is a usage error, on
// every command that takes the pair.
func TestCLI_BodyFileMutuallyExclusiveWithBody(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")
	path := writeBodyFile(t, t.TempDir(), "msg.md", "from a file\n")

	cases := [][]string{
		{"mail", "send", "--create", "mayor", "--from=me", "--subject=s", "--body=inline", "--body-file=" + path},
		{"new", "--title=t", "--body=inline", "--body-file=" + path},
	}
	for _, args := range cases {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%v: --body with --body-file must fail, got exit 0:\n%s", args, out)
			continue
		}
		if !strings.Contains(string(out), "--body-file") {
			t.Errorf("%v: error should mention --body-file, got: %s", args, out)
		}
	}
}

// TestCLI_MailSendBodyStillWorksUnchanged: --body is untouched and not
// deprecated. The new path is conditional, not hard-wired: a caller who never
// passes --body-file sees exactly the old behaviour, including the refusal to
// send an empty body.
func TestCLI_MailSendBodyStillWorksUnchanged(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")

	out, _, err := runMail(t, bin, env, "send", "--create", "mayor", "--from=me", "--subject=s", "--body=plain inline body")
	if err != nil {
		t.Fatalf("--body must still work: %v\n%s", err, out)
	}
	if got := readBodyJSON(t, bin, env, "mayor", lastMsgID(t, out)); got != "plain inline body" {
		t.Errorf("--body body = %q, want %q", got, "plain inline body")
	}

	// No body at all still fails, as before.
	if _, _, err := runMail(t, bin, env, "send", "--create", "mayor", "--from=me", "--subject=s"); err == nil {
		t.Error("send with no body must still fail")
	}
	// An EMPTY --body-file must fail the same way --body='' does, so the file
	// path cannot deliver nothing and report Delivered.
	empty := writeBodyFile(t, t.TempDir(), "empty.md", "")
	if _, _, err := runMail(t, bin, env, "send", "--create", "mayor", "--from=me", "--subject=s", "--body-file="+empty); err == nil {
		t.Error("send with an empty --body-file must fail, not deliver an empty body")
	}
}

// TestCLI_NewAndEditBodyFile covers the other two commands in scope. The 14 Jul
// incident was 'mg new', not mail — hence the scope.
func TestCLI_NewAndEditBodyFile(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "mayor")
	path := writeBodyFile(t, t.TempDir(), "body.md", hazardBody)

	// mg new --body-file, through a real shell.
	out, stderr, err := runSh(t, env, bin+" new --title=t --no-repo --body-file "+path)
	if err != nil {
		t.Fatalf("new --body-file failed: %v\n%s\n%s", err, out, stderr)
	}
	id := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(strings.TrimSpace(out), "Created "), ":", 2)[0])
	if id == "" {
		t.Fatalf("could not parse created id from %q", out)
	}

	if got := showBody(t, bin, env, id); !strings.Contains(got, "`com.pogo.pa-heyfeed`") || !strings.Contains(got, "$60k") {
		t.Errorf("mg new --body-file must store the file's bytes verbatim, got: %q", got)
	}

	// mg edit --body-file replaces the body, also verbatim.
	edited := writeBodyFile(t, t.TempDir(), "edit.md", "replaced with `backticks` and $(cmd)\n")
	if out, stderr, err := runSh(t, env, bin+" edit "+id+" --body-file "+edited); err != nil {
		t.Fatalf("edit --body-file failed: %v\n%s\n%s", err, out, stderr)
	}
	if got := showBody(t, bin, env, id); !strings.Contains(got, "`backticks` and $(cmd)") {
		t.Errorf("mg edit --body-file must store the file's bytes verbatim, got: %q", got)
	}
}

// showBody returns a work item's stored body via 'mg show'.
func showBody(t *testing.T, bin string, env []string, id string) string {
	t.Helper()
	cmd := exec.Command(bin, "show", id)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show %s failed: %v\n%s", id, err, out)
	}
	return string(out)
}

// seedReplyTarget delivers one message from mayor into arch's box and returns
// its id, so a reply out of arch/ goes back to mayor.
func seedReplyTarget(t *testing.T, bin string, env []string) string {
	t.Helper()
	mailRegisterBoxes(t, bin, env, "mayor")
	if _, _, err := runMail(t, bin, env, "send", "--create", "arch", "--from=mayor", "--subject=q", "--body=question"); err != nil {
		t.Fatalf("seed send failed: %v", err)
	}
	return soleMsgID(t, env, "arch")
}

// TestCLI_MailReplyBodyFileVerbatimThroughShell: 'mg mail reply --body-file'
// delivers the file's bytes verbatim through a real shell, with the same
// inline control as the send test so the assertion cannot pass on an inert
// fixture (mg-543f8).
func TestCLI_MailReplyBodyFileVerbatimThroughShell(t *testing.T) {
	bin, env := mailInit(t)
	orig := seedReplyTarget(t, bin, env)
	path := writeBodyFile(t, t.TempDir(), "reply.md", hazardBody)
	arch := asAgent(env, "arch")

	out, stderr, err := runSh(t, arch, bin+" mail reply arch/"+orig+" --body-file "+path)
	if err != nil {
		t.Fatalf("reply --body-file failed: %v\n%s\n%s", err, out, stderr)
	}
	fileMsgID := lastMsgID(t, out)

	out, stderr, err = runSh(t, arch, bin+" mail reply arch/"+orig+` --body="`+hazardBody+`"`)
	if err != nil {
		t.Fatalf("reply --body failed: %v\n%s\n%s", err, out, stderr)
	}
	inlineMsgID := lastMsgID(t, out)

	want := strings.TrimSpace(hazardBody)
	if got := readBodyJSON(t, bin, env, "mayor", fileMsgID); got != want {
		t.Errorf("reply --body-file must deliver the file's bytes verbatim.\n want: %q\n  got: %q", want, got)
	}
	if got := readBodyJSON(t, bin, env, "mayor", inlineMsgID); got == want {
		t.Fatalf("control failed: the shell did not mangle --body, so this test cannot prove --body-file bypasses it")
	}

	// Threading is unchanged by the new body path.
	if file := msgFile(t, env, "mayor", fileMsgID); !strings.Contains(file, "In-Reply-To: "+orig+"\n") {
		t.Errorf("reply --body-file lost threading:\n%s", file)
	}
}

// TestCLI_MailReplyBodyFileStdin: "-" reads the body from stdin, fed by a
// quoted heredoc through a real shell — the form the pogo prompts document.
func TestCLI_MailReplyBodyFileStdin(t *testing.T) {
	bin, env := mailInit(t)
	orig := seedReplyTarget(t, bin, env)

	line := bin + " mail reply arch/" + orig + " --body-file - <<'EOF'\n" + hazardBody + "EOF\n"
	out, stderr, err := runSh(t, asAgent(env, "arch"), line)
	if err != nil {
		t.Fatalf("reply --body-file - failed: %v\n%s\n%s", err, out, stderr)
	}
	if got, want := readBodyJSON(t, bin, env, "mayor", lastMsgID(t, out)), strings.TrimSpace(hazardBody); got != want {
		t.Errorf("reply --body-file - must deliver stdin verbatim.\n want: %q\n  got: %q", want, got)
	}
}

// TestCLI_MailReplyBodyFileRefusals: both flags, an unreadable path, an empty
// file, and no body at all are each refused — and none of them marks the
// original read or delivers anything.
func TestCLI_MailReplyBodyFileRefusals(t *testing.T) {
	bin, env := mailInit(t)
	orig := seedReplyTarget(t, bin, env)
	arch := asAgent(env, "arch")
	dir := t.TempDir()
	path := writeBodyFile(t, dir, "msg.md", "from a file\n")
	empty := writeBodyFile(t, dir, "empty.md", "")
	missing := filepath.Join(dir, "nope.md")

	cases := []struct {
		name    string
		args    []string
		mention string
	}{
		{"both flags", []string{"--body=inline", "--body-file=" + path}, "--body-file"},
		{"missing file", []string{"--body-file=" + missing}, "nope.md"},
		{"empty file", []string{"--body-file=" + empty}, "--body-file"},
		{"no body", nil, "--body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"reply", "arch/" + orig}, tc.args...)
			out, errOut, err := runMail(t, bin, arch, args...)
			if err == nil {
				t.Fatalf("reply %v must fail, got exit 0:\n%s", tc.args, out)
			}
			if !strings.Contains(out+errOut, tc.mention) {
				t.Errorf("error should mention %q, got: %s%s", tc.mention, out, errOut)
			}
		})
	}

	// Nothing was delivered and the original is still unread.
	if entries, err := os.ReadDir(mailboxDir(t, env, "mayor", "new")); err == nil && len(entries) > 0 {
		t.Errorf("a refused reply delivered %d message(s) to mayor", len(entries))
	}
	if _, err := os.Stat(filepath.Join(mailboxDir(t, env, "arch", "new"), orig)); err != nil {
		t.Errorf("a refused reply marked the original read: %v", err)
	}
}

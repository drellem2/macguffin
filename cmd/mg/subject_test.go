package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// hazardApostrophes is the TWO-APOSTROPHE case (E2), and it is the whole reason
// this file exists rather than a simpler one.
//
// E1 — a single apostrophe — leaves the shell with an unterminated quote and
// fails loudly; any reasonable test passes on it, and a control built on E1
// alone would certify a remedy that still fails silently. E2 is different: the
// quoting is BALANCED, so the shell succeeds, mg exits 0, and the apostrophes
// are simply gone from the delivered subject. Ordinary English carries
// apostrophes in pairs routinely, which is why single-quoting was refuted as
// the remedy for these fields (mg-2da4).
//
// The specific shape matters. "the rock'n'roll release" single-quoted parses as
// 'the rock' + n + 'roll release' with no unquoted whitespace anywhere, so it
// stays ONE argv word and mg cannot even notice the loss through an arg count.
const hazardApostrophes = "the rock'n'roll release"

// hazardMetachars is the other half of the same field's exposure: single quotes
// protect backticks and $ but not apostrophes, and double quotes protect
// apostrophes but not backticks or $. There is no inline spelling that survives
// both, which is the finding this ticket implements around rather than argues
// with.
const hazardMetachars = "ship `date` when $HOME is $(pwd)"

// readMsgJSON reads a delivered message and returns the whole decoded object,
// so a test can assert on the SUBJECT the store actually holds rather than on
// the human formatter's rendering of it.
func readMsgJSON(t *testing.T, bin string, env []string, agent, msgID string) mailReadJSON {
	t.Helper()
	out, stderr, err := runMail(t, bin, env, "read", agent+"/"+msgID, "--json", "--force")
	if err != nil {
		t.Fatalf("mail read failed: %v\n%s\n%s", err, out, stderr)
	}
	var got mailReadJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding mail read --json: %v\n%s", err, out)
	}
	return got
}

// sendDerived sends a message THROUGH A REAL SHELL with --subject omitted and
// subjectLine as the body's first line, and returns the delivered message.
// exec.Command would never invoke a shell, so a test using it would pass
// whether or not the derived path avoids one.
func sendDerived(t *testing.T, bin string, env []string, subjectLine string) mailReadJSON {
	t.Helper()
	line := bin + " mail send mayor --from=me --body-file - <<'EOF'\n" +
		subjectLine + "\n\nthe body proper, unrelated to the subject\nEOF\n"
	out, stderr, err := runSh(t, env, line)
	if err != nil {
		t.Fatalf("derived send failed: %v\n%s\n%s", err, out, stderr)
	}
	return readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out))
}

// TestCLI_MailSendDerivedSubjectVerbatimThroughShell is this ticket's central
// claim, with the A/B control the acceptance criteria require.
//
// SAFE arm: the subject rides in as the body's first line inside a quoted
// heredoc, so it must arrive byte-identical.
//
// UNSAFE arms: the same text passed inline, in both spellings a careful caller
// would reach for. These are what give the test teeth — they prove the fixtures
// really are hazardous, so the safe assertions pass because derivation bypasses
// the shell and not because the strings happened to be inert. If an unsafe arm
// ever comes back verbatim, the fixture stopped being hazardous: fix the
// fixture, do not delete the arm.
func TestCLI_MailSendDerivedSubjectVerbatimThroughShell(t *testing.T) {
	bin, env := mailInit(t)

	cases := []struct {
		name    string
		subject string
		// unsafe is the inline command line fragment a caller would write.
		unsafe func(bin, s string) string
	}{
		{
			// The two-apostrophe case. Balanced quotes, exit 0, silent loss.
			name:    "single-quoted, two apostrophes",
			subject: hazardApostrophes,
			unsafe: func(bin, s string) string {
				return bin + " mail send mayor --from=me --body=b --subject='" + s + "'"
			},
		},
		{
			// Double quotes survive apostrophes and lose everything else.
			name:    "double-quoted, backticks and expansions",
			subject: hazardMetachars,
			unsafe: func(bin, s string) string {
				return bin + ` mail send mayor --from=me --body=b --subject="` + s + `"`
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// SAFE: derived from the heredoc's first line.
			if got := sendDerived(t, bin, env, tc.subject).Subject; got != tc.subject {
				t.Errorf("a derived subject must be byte-exact.\n want: %q\n  got: %q", tc.subject, got)
			}

			// CONTROL: the same text inline. It must not arrive intact — either
			// the shell mangled it (exit 0, silent) or the invocation broke.
			out, _, err := runSh(t, env, tc.unsafe(bin, tc.subject))
			if err != nil {
				t.Logf("control: inline --subject failed loudly (%v) — still not verbatim", err)
				return
			}
			got := readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out)).Subject
			if got == tc.subject {
				t.Fatalf("control failed: the shell delivered inline --subject intact (%q), "+
					"so this test cannot prove derivation bypasses it", got)
			}
			t.Logf("control: inline --subject exited 0 and delivered %q instead of %q", got, tc.subject)
		})
	}
}

// TestCLI_MailSendDerivedSubjectSilentLossIsExitZero pins the property that
// makes the inline spelling worse than a crash: with an EVEN number of
// apostrophes the shell reports success and mg reports Delivered, on a subject
// that lost characters nobody will be told about.
//
// This is the arm E1 cannot reach, and the reason the acceptance criteria name
// it specifically.
func TestCLI_MailSendDerivedSubjectSilentLossIsExitZero(t *testing.T) {
	bin, env := mailInit(t)

	out, stderr, err := runSh(t, env,
		bin+" mail send mayor --from=me --body=b --subject='"+hazardApostrophes+"'")
	if err != nil {
		t.Skipf("this shell does not reproduce the silent form (%v); the loud form is covered elsewhere", err)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("the silent failure must be silent for this test to mean anything, got stderr: %q", stderr)
	}
	got := readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out)).Subject
	if got == hazardApostrophes {
		t.Fatalf("fixture is no longer hazardous: inline --subject delivered %q intact", got)
	}
	if strings.Contains(got, "'") {
		t.Errorf("expected the apostrophes to be eaten, got %q", got)
	}

	// And the same text, derived, is whole.
	if s := sendDerived(t, bin, env, hazardApostrophes).Subject; s != hazardApostrophes {
		t.Errorf("derived subject = %q, want %q", s, hazardApostrophes)
	}
}

// TestCLI_MailSendDerivedSubjectIsEchoed: a derived subject is READ BACK, in
// both output modes.
//
// The caller never named this field, so nothing else tells them what was taken
// from their body. mg's older first-line derivation — a work item's title from
// the body's first "# " heading — prints nothing, and that silence is exactly
// what let it rename items for four days with the documented remedy not even
// working. Shipping a second silent derivation was ruled out when this one was
// adopted.
func TestCLI_MailSendDerivedSubjectIsEchoed(t *testing.T) {
	bin, env := mailInit(t)

	out, stderr, err := runMail(t, bin, env, "send", "mayor", "--from=me",
		"--body=derived subject line\n\nrest of the body")
	if err != nil {
		t.Fatalf("send failed: %v\n%s\n%s", err, out, stderr)
	}
	if !strings.Contains(out, "Subject: derived subject line") {
		t.Errorf("a derived subject must be echoed back, got:\n%s", out)
	}
	if !strings.Contains(out, "Delivered:") {
		t.Errorf("the echo must not replace the Delivered line, got:\n%s", out)
	}

	// --json carries the same signal for scripted callers.
	out, stderr, err = runMail(t, bin, env, "send", "mayor", "--from=me",
		"--body=json derived line\n\nrest", "--json")
	if err != nil {
		t.Fatalf("send --json failed: %v\n%s\n%s", err, out, stderr)
	}
	var sent mailSendJSON
	if err := json.Unmarshal([]byte(out), &sent); err != nil {
		t.Fatalf("decoding mail send --json: %v\n%s", err, out)
	}
	if sent.Subject != "json derived line" {
		t.Errorf("send --json subject = %q, want %q", sent.Subject, "json derived line")
	}
	if !sent.SubjectDerived {
		t.Error("send --json subject_derived must be true when --subject was omitted")
	}

	// An explicitly supplied subject is NOT echoed — the caller typed it — and
	// is reported as not derived.
	out, stderr, err = runMail(t, bin, env, "send", "mayor", "--from=me",
		"--subject=typed by hand", "--body=first body line\n\nrest", "--json")
	if err != nil {
		t.Fatalf("send with --subject failed: %v\n%s\n%s", err, out, stderr)
	}
	sent = mailSendJSON{}
	if err := json.Unmarshal([]byte(out), &sent); err != nil {
		t.Fatalf("decoding mail send --json: %v\n%s", err, out)
	}
	if sent.Subject != "typed by hand" || sent.SubjectDerived {
		t.Errorf("explicit --subject must win and report subject_derived=false, got %+v", sent)
	}
}

// TestCLI_MailSendExplicitSubjectUnchanged: the omission path is the only thing
// this ticket changed. A supplied --subject still wins over the body's first
// line, and an explicitly EMPTY one is still refused — making omission safe must
// not make the unsafe spelling quieter.
func TestCLI_MailSendExplicitSubjectUnchanged(t *testing.T) {
	bin, env := mailInit(t)

	out, stderr, err := runMail(t, bin, env, "send", "mayor", "--from=me",
		"--subject=explicit wins", "--body=a different first line\n\nbody")
	if err != nil {
		t.Fatalf("send failed: %v\n%s\n%s", err, out, stderr)
	}
	if got := readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out)).Subject; got != "explicit wins" {
		t.Errorf("explicit --subject must win over the body's first line, got %q", got)
	}

	// --subject= (given but empty) stays a refusal, and the error points at the
	// cheaper spelling rather than just complaining.
	cmd := exec.Command(bin, "mail", "send", "mayor", "--from=me", "--subject=", "--body=b")
	cmd.Env = env
	combined, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an explicitly empty --subject must still be refused, got exit 0:\n%s", combined)
	}
	if !strings.Contains(string(combined), "--subject") {
		t.Errorf("the refusal must name --subject, got: %s", combined)
	}

	// A missing --from is still required; only --subject became optional.
	cmd = exec.Command(bin, "mail", "send", "mayor", "--body=b")
	cmd.Env = env
	if combined, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("send with no --from must still fail, got exit 0:\n%s", combined)
	}
	// A missing body is still required, and now that --subject is optional a
	// bare recipient must not become a valid send.
	cmd = exec.Command(bin, "mail", "send", "mayor", "--from=me")
	cmd.Env = env
	if combined, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("send with no body must still fail, got exit 0:\n%s", combined)
	}
}

// TestCLI_MailSendDerivationCopiesTheLine: deriving must not consume the body.
//
// Cutting the first line would make a read operation into a body-MUTATING one,
// and every ticket in this family is about a tool delivering something other
// than what it was handed. A duplicated line is visible and harmless; a deleted
// one is the disease.
func TestCLI_MailSendDerivationCopiesTheLine(t *testing.T) {
	bin, env := mailInit(t)

	body := "the first line\n\nthe second paragraph\nand a third line"
	out, stderr, err := runMail(t, bin, env, "send", "mayor", "--from=me", "--body="+body)
	if err != nil {
		t.Fatalf("send failed: %v\n%s\n%s", err, out, stderr)
	}
	msg := readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out))
	if msg.Subject != "the first line" {
		t.Errorf("subject = %q, want %q", msg.Subject, "the first line")
	}
	if msg.Body != body {
		t.Errorf("the body must be delivered whole, first line included.\n want: %q\n  got: %q", body, msg.Body)
	}
}

// TestCLI_MailSendDerivationRefusesLoudly: a subject that cannot be derived
// cleanly is an error naming --subject, never a header written anyway.
//
// mg-b5d3 is the price of the alternative: a bare CR in a subject silently
// dropped a message for four days.
func TestCLI_MailSendDerivationRefusesLoudly(t *testing.T) {
	bin, env := mailInit(t)

	cases := []struct {
		name string
		args []string
	}{
		// Whitespace-only body: there is no first line to take.
		{"blank body", []string{"mail", "send", "mayor", "--from=me", "--body=   \n\n  "}},
		// A bare CR inside the first line — the mg-b5d3 shape.
		{"control character in the first line", []string{"mail", "send", "mayor", "--from=me", "--body=sub\rject\n\nbody"}},
		// A tab inside the line is a control character too; only the margins
		// are trimmed.
		{"tab inside the first line", []string{"mail", "send", "mayor", "--from=me", "--body=sub\tject\n\nbody"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("must be refused, got exit 0:\n%s", out)
			}
			if !strings.Contains(string(out), "--subject") {
				t.Errorf("the refusal must name --subject as the way out, got: %s", out)
			}
		})
	}

	// Nothing was delivered by any of the refusals.
	out, _, err := runMail(t, bin, env, "list", "mayor")
	if err != nil {
		t.Fatalf("mail list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No mailbox for mayor yet") {
		t.Errorf("a refused derivation must not deliver anything, got:\n%s", out)
	}
}

// TestDeriveSubject pins the normalization directly, where the shell is not in
// the way and every edge is cheap to state.
func TestDeriveSubject(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"plain first line", "hello there\n\nbody", "hello there", false},
		{"single line body", "just this", "just this", false},
		// CRLF: the trailing CR is the other half of the terminator, not
		// content, so a file written on Windows is not refused for its line
		// endings.
		{"crlf terminator", "subject line\r\nbody\r\n", "subject line", false},
		// The store trims the body, so the first line the recipient sees is the
		// first non-blank one; deriving from anything else would echo a subject
		// that is not at the top of the delivered message.
		{"leading blank lines", "\n\n  the real first line\nbody", "the real first line", false},
		{"margins trimmed", "\t  padded line \t\n\nbody", "padded line", false},
		{"whitespace only", "   \n\n\t ", "", true},
		{"empty", "", "", true},
		{"bare CR inside", "sub\rject\nbody", "", true},
		{"tab inside", "sub\tject\nbody", "", true},
		{"DEL inside", "sub\x7fject\nbody", "", true},
		// A "# " heading is NOT stripped. mg's work-item title derivation reads
		// markdown; mail's reads a line. Adding a second, different silent
		// transformation to the same tool is what the ruling warned against —
		// and the echo makes whatever was taken visible either way.
		{"markdown heading is literal", "# Design notes\n\nbody", "# Design notes", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveSubject(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("deriveSubject(%q) = %q, want an error", tc.body, got)
				}
				// The remedy lives in the Hint, which mgerr carries separately
				// from the Message and both renderers print (internal/mgerr).
				var e *mgerr.Error
				if !errors.As(err, &e) {
					t.Fatalf("deriveSubject must return an *mgerr.Error so it renders as usage (exit 2), got %T", err)
				}
				if e.Category != mgerr.CatUsage {
					t.Errorf("category = %q, want usage — the caller passed a body mg cannot take a header from", e.Category)
				}
				if !strings.Contains(e.Hint, "--subject") {
					t.Errorf("the hint must name --subject as the way out, got: %q", e.Hint)
				}
				return
			}
			if err != nil {
				t.Fatalf("deriveSubject(%q) failed: %v", tc.body, err)
			}
			if got != tc.want {
				t.Errorf("deriveSubject(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestCLI_MailSendDerivedSubjectFromBodyFile: derivation works off --body-file
// too, which is the spelling the help teaches. The subject and the body then
// come from the same bytes, read once, with no shell in either path.
func TestCLI_MailSendDerivedSubjectFromBodyFile(t *testing.T) {
	bin, env := mailInit(t)
	content := hazardApostrophes + " " + hazardMetachars + "\n\n" + hazardBody
	path := writeBodyFile(t, t.TempDir(), "msg.md", content)

	out, stderr, err := runSh(t, env, bin+" mail send mayor --from=me --body-file "+path)
	if err != nil {
		t.Fatalf("send --body-file failed: %v\n%s\n%s", err, out, stderr)
	}
	want := hazardApostrophes + " " + hazardMetachars
	msg := readMsgJSON(t, bin, env, "mayor", lastMsgID(t, out))
	if msg.Subject != want {
		t.Errorf("subject from --body-file must be verbatim.\n want: %q\n  got: %q", want, msg.Subject)
	}
	if !strings.Contains(msg.Body, "`com.pogo.pa-heyfeed`") || !strings.Contains(msg.Body, "$60k") {
		t.Errorf("the body must still arrive verbatim alongside the derived subject, got: %q", msg.Body)
	}
}

// TestSubjectHelp_TeachesDerivation: the help must show the derived form as the
// canonical one. The whole mechanism is that the safe path is now the SHORT
// path — a help text that still spells --subject in its example teaches the
// long way round, which is how this family of defects survived documentation
// three times before.
func TestSubjectHelp_TeachesDerivation(t *testing.T) {
	bin := buildBinary(t)
	help := mgHelp(t, bin, "mail", "send")

	if !strings.Contains(help, "--subject") {
		t.Fatalf("mail send --help must still document --subject:\n%s", help)
	}
	// The canonical heredoc example must NOT carry --subject.
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "<<'EOF'") && strings.Contains(line, "--subject") {
			t.Errorf("the canonical example still passes --subject inline, teaching the long way round:\n%s", line)
		}
	}
	for _, want := range []string{"--subject is optional", "the body's first line"} {
		if !strings.Contains(help, want) {
			t.Errorf("mail send --help must mention %q, got:\n%s", want, help)
		}
	}
}

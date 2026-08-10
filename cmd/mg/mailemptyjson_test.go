package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// `mg mail list AGENT --json` used to emit TWO SCHEMAS from one invocation,
// selected by state: message objects when the mailbox had mail, and a
// mailbox-summary object when it did not. The summary was introduced (mg-d639)
// so a scripted consumer could tell a quiet inbox from a misdelivery, and it
// answered that question by breaking a more basic one — an empty mailbox
// emitted one row, so
//
//	mg mail list mayor --json | jq -s 'length'
//
// returned 1 for a mailbox holding nothing. Every consumer that answers "do I
// have mail?" by counting rows got a false positive, forever, on exactly the
// case where the answer was no; and `.from` on the summary is null, so field
// selectors printed "null: null", which reads as a corrupt message rather than
// an empty box. The reporter misdiagnosed it as a jq quirk twice in one shift
// before checking the bytes with od -c (mg-4d34).
//
// The tests below are about the STREAM, not the values, so they must never use
// CombinedOutput: merging stdout and stderr is precisely the blindness that let
// the defect ship past a suite that already covered the summary's contents.

// ndjsonRows counts JSON values in an NDJSON stream — the Go equivalent of
// `jq -s 'length'`, which is the measurement the bug report is written in and
// the one every "is there mail?" script performs. It deliberately counts rows
// rather than inspecting them: a check that discriminates on a field cannot
// observe this defect, because a counter never looks at a field.
func ndjsonRows(t *testing.T, stream string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("NDJSON stream carried a non-JSON line %q: %v", line, err)
		}
		n++
	}
	return n
}

// TestCLI_MailListJSONEmptyMailboxEmitsZeroRows is the ticket's assertion, with
// its positive control in the same test: a non-empty mailbox must still produce
// one row per message, so this cannot pass by `mg mail list --json` being
// broken into silence.
func TestCLI_MailListJSONEmptyMailboxEmitsZeroRows(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "quietbox", "busybox")

	// (1) The reported case: a real mailbox with nothing in it.
	out, errOut, err := runMail(t, bin, env, "list", "quietbox", "--json")
	if err != nil {
		t.Fatalf("listing an empty mailbox should exit 0: %v\n%s", err, errOut)
	}
	if n := ndjsonRows(t, out); n != 0 {
		t.Errorf("an EMPTY mailbox must emit 0 rows on stdout, got %d — "+
			"`jq -s length` is how scripts ask \"do I have mail?\" and this answers yes:\n%q", n, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("an empty mailbox must emit zero bytes on stdout, got %q", out)
	}

	// (2) The positive control, same command, same test: rows appear when
	// there is mail, and the count is exactly the message count — not the
	// message count plus a summary.
	for _, subj := range []string{"first", "second", "third"} {
		if o, e, err := runMail(t, bin, env, "send", "busybox",
			"--from=mayor", "--subject="+subj, "--body=b"); err != nil {
			t.Fatalf("send failed: %v\n%s%s", err, o, e)
		}
	}
	out, errOut, err = runMail(t, bin, env, "list", "busybox", "--json")
	if err != nil {
		t.Fatalf("listing a populated mailbox failed: %v\n%s", err, errOut)
	}
	if n := ndjsonRows(t, out); n != 3 {
		t.Errorf("a mailbox holding 3 messages must emit exactly 3 rows, got %d:\n%s", n, out)
	}

	// (3) Every row is a message, with the documented fields actually
	// populated. A summary row satisfies a count of 3 only by coincidence;
	// this is what makes the count above mean "messages".
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var msg mailMsgJSON
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("row is not a message object: %v\n%s", err, line)
		}
		if msg.ID == "" || msg.From == "" || msg.Subject == "" {
			t.Errorf("row has empty documented fields %+v — a field selector on this "+
				"stream printed \"null: null\", which reads as a corrupt message:\n%s", msg, line)
		}
	}
}

// TestCLI_MailListJSONMissingVsEmptyStillDistinguishable: the guarantee mg-d639
// bought is not paid for by the fix. Silencing stdout must not silence the
// answer — a human who reads "No mailbox for X yet" as "X has no new mail" is
// how a stalled review stayed invisible for forty minutes, and the scripted
// form of that question still has to be answerable. It moved to stderr.
func TestCLI_MailListJSONMissingVsEmptyStillDistinguishable(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "realbox")

	status := func(box string) (stdout string, got mailboxJSON) {
		t.Helper()
		out, errOut, err := runMail(t, bin, env, "list", box, "--json")
		if err != nil {
			t.Fatalf("list %s --json should exit 0: %v\n%s", box, err, errOut)
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(errOut)), &got); err != nil {
			t.Fatalf("list %s --json must put one status object on stderr, got %q (%v)", box, errOut, err)
		}
		return out, got
	}

	missingOut, missing := status("9ecf")
	if missing.Mailbox != "9ecf" || missing.Exists || missing.Unread != 0 {
		t.Errorf("missing mailbox status = %+v, want {9ecf 0 false}", missing)
	}

	emptyOut, empty := status("realbox")
	if empty.Mailbox != "realbox" || !empty.Exists || empty.Unread != 0 {
		t.Errorf("empty mailbox status = %+v, want {realbox 0 true}", empty)
	}

	if missing.Exists == empty.Exists {
		t.Errorf("a mailbox that never existed and one that is merely quiet must remain "+
			"distinguishable under --json: %+v vs %+v", missing, empty)
	}

	// Both are still zero rows of mail, which is the whole point: the
	// distinction lives beside the message stream, never inside it.
	if n := ndjsonRows(t, missingOut); n != 0 {
		t.Errorf("a missing mailbox must emit 0 stdout rows, got %d:\n%s", n, missingOut)
	}
	if n := ndjsonRows(t, emptyOut); n != 0 {
		t.Errorf("an empty mailbox must emit 0 stdout rows, got %d:\n%s", n, emptyOut)
	}
}

// TestCLI_MailListJSONRowCountIsMessageCountInEveryMode: the defect is a row
// count that disagrees with the message count, so it is worth checking on every
// path that can reach the summary — including through a flag. Fixing only the
// case in the report would leave the reported behaviour one --exclude-from away,
// which is the remedy reproducing the defect it remedies.
func TestCLI_MailListJSONRowCountIsMessageCountInEveryMode(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "box")

	send := func(from, subject string) {
		t.Helper()
		if o, e, err := runMail(t, bin, env, "send", "box",
			"--from="+from, "--subject="+subject, "--body=b"); err != nil {
			t.Fatalf("send failed: %v\n%s%s", err, o, e)
		}
	}
	send("scheduler", "noise one")
	send("scheduler", "noise two")
	send("mayor", "the real one")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unread", []string{"list", "box", "--json"}, 3},
		{"--all", []string{"list", "box", "--all", "--json"}, 3},
		// An archive nobody has put anything in is the empty case again,
		// reached by a different flag.
		{"--archived (empty)", []string{"list", "box", "--archived", "--json"}, 0},
		{"--exclude-from", []string{"list", "box", "--exclude-from=scheduler", "--json"}, 1},
		{"--from", []string{"list", "box", "--from=scheduler", "--json"}, 2},
		// A predicate that matches nothing: the case where a count of 1
		// would have said "you have mail" about a listing with none.
		{"--from (matches nothing)", []string{"list", "box", "--from=ghost", "--json"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runMail(t, bin, env, tc.args...)
			if err != nil {
				t.Fatalf("%v failed: %v\n%s", tc.args, err, errOut)
			}
			if n := ndjsonRows(t, out); n != tc.want {
				t.Errorf("stdout rows = %d, want %d (the message count):\n%s", n, tc.want, out)
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var obj map[string]any
				if err := json.Unmarshal([]byte(line), &obj); err != nil {
					t.Fatalf("non-JSON stdout line %q: %v", line, err)
				}
				if _, hasID := obj["id"]; !hasID {
					t.Errorf("stdout row is not a message (no \"id\"): %s", line)
				}
			}
		})
	}
}

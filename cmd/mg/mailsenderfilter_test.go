package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mail"
)

// mailNoisyBox builds the shape this flag exists for: a mailbox whose unread
// list is overwhelmingly one automated sender, with a small number of real
// messages buried in it. It deliberately includes the two collisions that a
// text filter over the rendered line gets wrong — a real message whose SUBJECT
// says "scheduler", and a distinct sender whose NAME contains "scheduler".
func mailNoisyBox(t *testing.T, bin string, env []string, box string) {
	t.Helper()
	mailRegisterBoxes(t, bin, env, box)

	send := func(from, subject string) {
		t.Helper()
		if out, _, err := runMail(t, bin, env, "send", box,
			"--from="+from, "--subject="+subject, "--body=b"); err != nil {
			t.Fatalf("send from %s failed: %v\n%s", from, err, out)
		}
	}

	send("scheduler", "scheduler: mail-check-"+box)
	send("scheduler", "scheduler: mail-check-"+box)
	// The message the retracted `grep -v scheduler` remedy would eat: a real
	// sender writing ABOUT the scheduler noise.
	send("pm-pogo", "scheduler wake-up mail is 99% of every low-traffic mailbox")
	send("scheduler", "scheduler: mail-check-"+box)
	// A different sender whose name merely contains the excluded one.
	send("scheduler-v2", "second-generation fire")
}

// TestCLI_MailListSenderFilterExcludesBySenderField: --exclude-from removes the
// noise sender's rows and nothing else.
func TestCLI_MailListSenderFilterExcludesBySenderField(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	out, _, err := runMail(t, bin, env, "list", "arch", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("filtered list failed: %v\n%s", err, out)
	}

	if strings.Contains(out, "  scheduler   ") {
		t.Errorf("--exclude-from=scheduler left a scheduler row in the listing:\n%s", out)
	}
	if !strings.Contains(out, "pm-pogo") {
		t.Errorf("--exclude-from=scheduler dropped the real message:\n%s", out)
	}
	if !strings.Contains(out, "scheduler-v2") {
		t.Errorf("--exclude-from=scheduler must not hide the distinct sender scheduler-v2 "+
			"(exact field match, not a prefix or substring):\n%s", out)
	}
}

// TestCLI_MailListSenderFilterIsNotASubstringMatch is the retraction test
// (mg-5168, 2026-08-09). `grep -v scheduler` was recommended as the escape and
// then withdrawn by name: it matches the rendered LINE, so it discards a real
// message whose SUBJECT mentions the scheduler — precisely the correspondence
// about the defect. A sender predicate must not be able to make that mistake,
// so the property is asserted rather than left to the implementation's shape.
func TestCLI_MailListSenderFilterIsNotASubstringMatch(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	out, _, err := runMail(t, bin, env, "list", "arch", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("filtered list failed: %v\n%s", err, out)
	}

	// The subject collision: a real sender, the excluded word in the subject.
	if !strings.Contains(out, "scheduler wake-up mail is 99%") {
		t.Errorf("a message from pm-pogo whose SUBJECT says \"scheduler\" was hidden by "+
			"--exclude-from=scheduler; the predicate must bind to the From field only:\n%s", out)
	}

	// The sender collision, in the selecting direction: a partial name selects
	// nothing rather than everything it is a prefix of.
	out, _, err = runMail(t, bin, env, "list", "arch", "--from=sched")
	if err != nil {
		t.Fatalf("--from=sched failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "scheduler") && !strings.Contains(out, "sender filter") {
		t.Errorf("--from=sched must match no sender exactly, got:\n%s", out)
	}
	if !strings.Contains(out, "0 of 5 shown") {
		t.Errorf("--from=sched should select nothing out of 5 messages, got:\n%s", out)
	}
}

// TestCLI_MailListSenderFilterReportsWhatItHid: a filter is another bounded
// read, so a filtered listing that said nothing about its own narrowing would
// reproduce the defect it remedies. Both the non-empty and the emptied-by-the-
// filter cases must carry the count, and the emptied case must NOT borrow the
// wording of a quiet mailbox.
func TestCLI_MailListSenderFilterReportsWhatItHid(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	// Non-empty result: the header names the flag and the counts.
	out, _, err := runMail(t, bin, env, "list", "arch", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("filtered list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sender filter: --exclude-from=scheduler") {
		t.Errorf("filtered listing must name the predicate it applied, got:\n%s", out)
	}
	if !strings.Contains(out, "2 of 5 shown, 3 hidden") {
		t.Errorf("filtered listing must report what it hid, got:\n%s", out)
	}
	// The report is a HEADER, so a bounded read from the top keeps it.
	if firstLine := strings.SplitN(out, "\n", 2)[0]; !strings.Contains(firstLine, "sender filter") {
		t.Errorf("the suppression report must be the first line, got first line: %q", firstLine)
	}

	// Emptied by the filter: this is the false-negative case.
	out, _, err = runMail(t, bin, env, "list", "arch", "--from=nobody-at-all")
	if err != nil {
		t.Fatalf("filtered-to-empty list should exit 0, got: %v\n%s", err, out)
	}
	if strings.Contains(out, "No unread messages for arch (mailbox exists)") {
		t.Errorf("a listing emptied BY THE FILTER must not read like a quiet mailbox, got:\n%s", out)
	}
	if !strings.Contains(out, "the mailbox is NOT empty") {
		t.Errorf("a listing emptied by the filter must say the mailbox is not empty, got:\n%s", out)
	}
	if !strings.Contains(out, "all 5 unread messages were hidden") {
		t.Errorf("a listing emptied by the filter must count what it hid, got:\n%s", out)
	}
}

// TestCLI_MailListSenderFilterNeverExistedStillDistinct: the mg-d639
// missing-vs-empty distinction survives a predicate. A ghost mailbox is still
// reported as missing, not as something the filter emptied.
func TestCLI_MailListSenderFilterNeverExistedStillDistinct(t *testing.T) {
	bin, env := mailInit(t)

	out, _, err := runMail(t, bin, env, "list", "ghost", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("filtered list of never-existed mailbox should exit 0, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No such mailbox: ghost") {
		t.Errorf("a never-existed mailbox must stay distinct under a filter, got:\n%s", out)
	}
	if strings.Contains(out, "the mailbox is NOT empty") {
		t.Errorf("a never-existed mailbox must not be described as filtered, got:\n%s", out)
	}
}

// TestCLI_MailListSenderFilterJSONSummary: the trailing summary object carries
// the counts, is emitted whether or not messages matched, and has no "id" — so
// the documented consumer guard `select(.id and ...)` skips it exactly as it
// already skips the empty-mailbox sentinel.
func TestCLI_MailListSenderFilterJSONSummary(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	decode := func(out string) (msgs []map[string]any, summary map[string]any) {
		t.Helper()
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Fatalf("non-JSON line %q: %v", line, err)
			}
			// This is the guard the docs hand to consumers.
			if _, hasID := obj["id"]; hasID {
				msgs = append(msgs, obj)
				continue
			}
			if summary != nil {
				t.Fatalf("more than one non-message object in the stream:\n%s", out)
			}
			summary = obj
		}
		return msgs, summary
	}

	out, _, err := runMail(t, bin, env, "list", "arch", "--exclude-from=scheduler", "--json")
	if err != nil {
		t.Fatalf("filtered json list failed: %v\n%s", err, out)
	}
	msgs, summary := decode(out)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 surviving messages, got %d:\n%s", len(msgs), out)
	}
	if summary == nil {
		t.Fatalf("a filtered stream must carry a summary object:\n%s", out)
	}
	if got := summary["suppressed"]; got != float64(3) {
		t.Errorf("suppressed = %v, want 3:\n%s", got, out)
	}
	if got := summary["listed"]; got != float64(2) {
		t.Errorf("listed = %v, want 2:\n%s", got, out)
	}
	// unread is the mailbox's TRUE unread count, not the filtered one: a
	// summary that reported the narrowed figure would manufacture the same
	// absence the listing does.
	if got := summary["unread"]; got != float64(5) {
		t.Errorf("unread = %v, want the mailbox's true 5:\n%s", got, out)
	}
	if got := summary["exists"]; got != true {
		t.Errorf("exists = %v, want true:\n%s", got, out)
	}
	if got, _ := summary["exclude_from"].([]any); len(got) != 1 || got[0] != "scheduler" {
		t.Errorf("exclude_from = %v, want [scheduler]:\n%s", summary["exclude_from"], out)
	}
	if got, _ := summary["from"].([]any); got == nil {
		t.Errorf("from must be [] rather than null when unset, got %v", summary["from"])
	}

	// Filtered to nothing: still exactly one summary object, still counted.
	out, _, err = runMail(t, bin, env, "list", "arch", "--from=nobody-at-all", "--json")
	if err != nil {
		t.Fatalf("filtered-to-empty json list failed: %v\n%s", err, out)
	}
	msgs, summary = decode(out)
	if len(msgs) != 0 {
		t.Fatalf("expected no messages, got %d:\n%s", len(msgs), out)
	}
	if summary == nil || summary["suppressed"] != float64(5) || summary["unread"] != float64(5) {
		t.Fatalf("emptied stream must report 5 suppressed over 5 unread, got:\n%s", out)
	}
}

// TestCLI_MailListUnfilteredJSONUnchanged: with no predicate, the stream is
// byte-for-byte what it was — no summary object appears. The mg-d639
// empty-mailbox sentinel is untouched.
func TestCLI_MailListUnfilteredJSONUnchanged(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	out, _, err := runMail(t, bin, env, "list", "arch", "--json")
	if err != nil {
		t.Fatalf("json list failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 {
		t.Fatalf("unfiltered stream should be 5 message objects and nothing else, got %d lines:\n%s", len(lines), out)
	}
	for _, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("non-JSON line %q: %v", line, err)
		}
		if _, hasID := obj["id"]; !hasID {
			t.Errorf("unfiltered stream must contain only message objects, got: %s", line)
		}
	}

	// The empty-mailbox sentinel still stands alone.
	out, _, err = runMail(t, bin, env, "list", "arch", "--archived", "--json")
	if err != nil {
		t.Fatalf("archived json list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"exists":true`) || strings.Contains(out, `"suppressed"`) {
		t.Errorf("an unfiltered empty listing must emit the plain sentinel, got:\n%s", out)
	}
}

// TestCLI_MailListSenderFilterCanonicalizesNames: the predicate strips the same
// mg-/cat- harness prefixes every mailbox argument gets, and is case
// insensitive, so a polecat can be named by any of its aliases.
func TestCLI_MailListSenderFilterCanonicalizesNames(t *testing.T) {
	bin, env := mailInit(t)
	mailRegisterBoxes(t, bin, env, "arch")
	if out, _, err := runMail(t, bin, env, "send", "arch", "--from=5168", "--subject=real", "--body=b"); err != nil {
		t.Fatalf("send failed: %v\n%s", err, out)
	}
	if out, _, err := runMail(t, bin, env, "send", "arch", "--from=scheduler", "--subject=noise", "--body=b"); err != nil {
		t.Fatalf("send failed: %v\n%s", err, out)
	}

	for _, alias := range []string{"5168", "mg-5168", "cat-mg-5168", "MG-5168"} {
		out, _, err := runMail(t, bin, env, "list", "arch", "--from="+alias)
		if err != nil {
			t.Fatalf("--from=%s failed: %v\n%s", alias, err, out)
		}
		if !strings.Contains(out, "real") {
			t.Errorf("--from=%s should match the sender written as \"5168\", got:\n%s", alias, out)
		}
		if strings.Contains(out, "noise") {
			t.Errorf("--from=%s must not select the scheduler, got:\n%s", alias, out)
		}
	}
}

// TestCLI_MailListSenderFilterAppliesToAllAndArchived: the predicate is a
// property of the listing, not of one mode, so it composes with --all and
// --archived rather than silently applying to the inbox.
func TestCLI_MailListSenderFilterAppliesToAllAndArchived(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	// Archive one scheduler row and the real one.
	listOut, _, _ := runMail(t, bin, env, "list", "arch", "--json")
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(listOut), "\n") {
		var obj mailMsgJSON
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		if obj.From == "scheduler" && len(ids) == 0 {
			ids = append(ids, obj.ID)
		}
		if obj.From == "pm-pogo" {
			ids = append(ids, obj.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected to pick 2 ids to archive, got %v", ids)
	}
	for _, id := range ids {
		if out, _, err := runMail(t, bin, env, "archive", "arch/"+id); err != nil {
			t.Fatalf("archive %s failed: %v\n%s", id, err, out)
		}
	}

	out, _, err := runMail(t, bin, env, "list", "arch", "--archived", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("archived filtered list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 2 shown, 1 hidden") {
		t.Errorf("--archived listing should be filtered and counted, got:\n%s", out)
	}
	if !strings.Contains(out, "pm-pogo") {
		t.Errorf("--archived listing lost the real message, got:\n%s", out)
	}

	// --all covers read messages too; nothing here has been read, so it sees
	// the three remaining unread rows.
	out, _, err = runMail(t, bin, env, "list", "arch", "--all", "--exclude-from=scheduler")
	if err != nil {
		t.Fatalf("--all filtered list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 3 shown, 2 hidden") {
		t.Errorf("--all listing should be filtered and counted, got:\n%s", out)
	}
}

// TestCLI_MailListSenderFilterRefusals: the spellings that can only produce an
// empty listing are refused at the callsite, rather than reporting an absence
// they manufactured.
func TestCLI_MailListSenderFilterRefusals(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "same name on both sides",
			args: []string{"list", "arch", "--from=scheduler", "--exclude-from=scheduler"},
			want: "both name",
		},
		{
			name: "same name via an alias",
			args: []string{"list", "arch", "--from=mg-5168", "--exclude-from=5168"},
			want: "both name",
		},
		{
			name: "empty sender name",
			args: []string{"list", "arch", "--exclude-from="},
			want: "empty sender name",
		},
		{
			name: "no AGENT",
			args: []string{"list", "--exclude-from=scheduler"},
			want: "require an AGENT",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runMail(t, bin, env, tc.args...)
			if err == nil {
				t.Fatalf("expected a refusal, got exit 0:\n%s", out)
			}
			if !strings.Contains(stderr+out, tc.want) {
				t.Errorf("refusal should mention %q, got:\n%s%s", tc.want, out, stderr)
			}
		})
	}
}

// TestCLI_MailListSenderFilterCombines: --from and --exclude-from compose,
// include first then exclude, and a repeated / comma-separated flag names
// several senders.
func TestCLI_MailListSenderFilterCombines(t *testing.T) {
	bin, env := mailInit(t)
	mailNoisyBox(t, bin, env, "arch")

	out, _, err := runMail(t, bin, env, "list", "arch", "--exclude-from=scheduler,scheduler-v2")
	if err != nil {
		t.Fatalf("comma-separated exclude failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 5 shown, 4 hidden") {
		t.Errorf("two excluded senders should leave one row, got:\n%s", out)
	}

	out, _, err = runMail(t, bin, env, "list", "arch",
		"--exclude-from=scheduler", "--exclude-from=scheduler-v2")
	if err != nil {
		t.Fatalf("repeated exclude failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 5 shown, 4 hidden") {
		t.Errorf("a repeated flag should behave as the comma-separated form, got:\n%s", out)
	}

	out, _, err = runMail(t, bin, env, "list", "arch",
		"--from=scheduler,pm-pogo", "--exclude-from=scheduler-v2")
	if err != nil {
		t.Fatalf("combined filter failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "4 of 5 shown, 1 hidden") {
		t.Errorf("include-then-exclude should leave four rows, got:\n%s", out)
	}
}

// TestSenderFilterUnattributedMailIsNeverHidden: a message with no From cannot
// be classified, and a filter must not suppress what it cannot classify — the
// unattributed message is the one least likely to be watched by anything else.
// Exercised at the type, because 'mg mail send' requires --from and so cannot
// produce one; such messages exist on disk from before the header was enforced.
func TestSenderFilterUnattributedMailIsNeverHidden(t *testing.T) {
	f, err := newSenderFilter(nil, []string{"scheduler"}, false, true)
	if err != nil {
		t.Fatalf("building filter: %v", err)
	}
	if !f.keep(mail.Message{ID: "x", From: "", Subject: "no header"}) {
		t.Error("--exclude-from hid a message with no From; an unclassifiable message must survive")
	}
	if !f.keep(mail.Message{ID: "y", From: "mayor"}) {
		t.Error("--exclude-from=scheduler hid a message from mayor")
	}
	if f.keep(mail.Message{ID: "z", From: "scheduler"}) {
		t.Error("--exclude-from=scheduler failed to hide the scheduler")
	}

	// --from is an explicit allowlist, so an unattributed message is simply
	// not in it. That is a selection, not a suppression.
	f, err = newSenderFilter([]string{"mayor"}, nil, true, false)
	if err != nil {
		t.Fatalf("building filter: %v", err)
	}
	if f.keep(mail.Message{ID: "x", From: ""}) {
		t.Error("--from=mayor selected a message with no From")
	}
}

// TestSenderFilterRefusesControlCharacters: the report echoes the name verbatim
// directly above the rows, so a newline in one would print a line among them
// that no message backs. Forging a row is the mirror of hiding one.
func TestSenderFilterRefusesControlCharacters(t *testing.T) {
	for _, bad := range []string{"sched\nuler", "a\tb", "x\x7f"} {
		if _, err := newSenderFilter(nil, []string{bad}, false, true); err == nil {
			t.Errorf("a sender name containing a control character (%q) should be refused", bad)
		}
	}
	if _, err := newSenderFilter(nil, []string{"scheduler"}, false, true); err != nil {
		t.Errorf("a plain sender name should be accepted, got: %v", err)
	}
}

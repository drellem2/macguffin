package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// reclaimFixture builds the shape the command exists for: a mailbox drowning in
// scheduler fallback copies with real correspondence buried inside it, plus a
// second mailbox so the fleet-wide (no-AGENT) form has more than one box to walk.
//
// The real messages are chosen to be exactly what a volume-based or text-based
// sweep would take: they are OLDER in the listing than most of the noise, they
// are duplicated (so "superseded" alone would select the older one), and one of
// them is a message ABOUT the scheduler.
func reclaimFixture(t *testing.T, bin string, env []string) {
	t.Helper()
	mailRegisterBoxes(t, bin, env, "architect", "pm-pogo", "mayor")

	send := func(box, from, subject string) {
		t.Helper()
		if out, _, err := runMail(t, bin, env, "send", box,
			"--from="+from, "--subject="+subject, "--body=b"); err != nil {
			t.Fatalf("send %s -> %s failed: %v\n%s", from, box, err, out)
		}
	}

	// Two real messages first, so they sit at the top of the pile the way the
	// 32-hour-old message did at row ~108 of 265.
	send("architect", "pm-pogo", "triage packet for the fleet")
	send("architect", "pm-pogo", "triage packet for the fleet")

	sched := "scheduler: mail-check-architect (cron */10 * * * *)"
	for i := 0; i < 8; i++ {
		send("architect", "scheduler", sched)
	}
	// A different sender whose name merely contains the reclaimed one.
	send("architect", "scheduler-v2", "second-generation fire")
	send("architect", "scheduler-v2", "second-generation fire")

	for i := 0; i < 5; i++ {
		send("mayor", "scheduler", "scheduler: mail-check-mayor (cron */10 * * * *)")
	}
	send("mayor", "pa", "fleet-wide notify report")
}

// runReclaim always disables the window: the CLI sends stamp Date=now, so any
// non-zero --older-than would retain everything and test nothing. The window
// itself is covered in internal/mail/reclaim_test.go against a fixed clock.
func runReclaim(t *testing.T, bin string, env []string, args ...string) (string, string, error) {
	t.Helper()
	return runMail(t, bin, env, append([]string{"reclaim", "--older-than=0"}, args...)...)
}

// TestCLI_MailReclaimDrainsSchedulerAndKeepsEverythingElse is the headline
// behaviour: the pile leaves the active mailbox, one live pointer per schedule
// stays, and no message from any other sender moves.
func TestCLI_MailReclaimDrainsSchedulerAndKeepsEverythingElse(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, _, err := runReclaim(t, bin, env, "architect")
	if err != nil {
		t.Fatalf("reclaim failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "7 of 8") {
		t.Errorf("expected 7 of 8 scheduler copies reclaimed, got:\n%s", out)
	}
	if !strings.Contains(out, "Nothing was deleted") {
		t.Errorf("report must say the mail was moved, not deleted:\n%s", out)
	}

	list, _, err := runMail(t, bin, env, "list", "architect")
	if err != nil {
		t.Fatalf("list failed: %v\n%s", err, list)
	}
	if n := strings.Count(list, "triage packet for the fleet"); n != 2 {
		t.Errorf("reclaim took a real message: %d of 2 triage packets left\n%s", n, list)
	}
	if n := strings.Count(list, "second-generation fire"); n != 2 {
		t.Errorf("reclaim took mail from scheduler-v2, a distinct sender: %d of 2 left\n%s", n, list)
	}
	if n := strings.Count(list, "mail-check-architect"); n != 1 {
		t.Errorf("want exactly 1 surviving scheduler copy (the newest), got %d\n%s", n, list)
	}

	arch, _, err := runMail(t, bin, env, "list", "architect", "--archived")
	if err != nil {
		t.Fatalf("archived list failed: %v\n%s", err, arch)
	}
	if n := strings.Count(arch, "mail-check-architect"); n != 7 {
		t.Errorf("want 7 reclaimed copies recoverable in archive/, got %d\n%s", n, arch)
	}
	if strings.Contains(arch, "triage packet") {
		t.Errorf("a real message reached the archive:\n%s", arch)
	}
}

// TestCLI_MailReclaimFleetWideDefaultsToScheduler: the no-AGENT form sweeps
// every box, and the default sender is the one generator.
func TestCLI_MailReclaimFleetWideDefaultsToScheduler(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, _, err := runReclaim(t, bin, env)
	if err != nil {
		t.Fatalf("fleet reclaim failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "architect") || !strings.Contains(out, "mayor") {
		t.Errorf("fleet sweep should name both touched mailboxes:\n%s", out)
	}
	// 7 from architect + 4 from mayor.
	if !strings.Contains(out, "11 of 13") {
		t.Errorf("expected 11 of 13 reclaimed fleet-wide, got:\n%s", out)
	}
	// pm-pogo's box holds nothing reclaimable; it must not get a row of its own.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  pm-pogo ") {
			t.Errorf("untouched mailbox got a row, which is the wall-of-text shape this replaces:\n%s", out)
		}
	}

	mayor, _, err := runMail(t, bin, env, "list", "mayor")
	if err != nil {
		t.Fatalf("mayor list failed: %v\n%s", err, mayor)
	}
	if !strings.Contains(mayor, "fleet-wide notify report") {
		t.Errorf("fleet sweep took pa's notify report — the exact message the manual sweep nearly lost:\n%s", mayor)
	}
}

// TestCLI_MailReclaimRefusesACorrespondent: --from naming a sender that has a
// mailbox of its own is refused, because the supersession argument is about
// recurring machine notifications and not about a thread.
func TestCLI_MailReclaimRefusesACorrespondent(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, stderr, err := runReclaim(t, bin, env, "architect", "--from=pm-pogo")
	if err == nil {
		t.Fatalf("reclaiming a correspondent's mail must be refused, got success:\n%s", out)
	}
	if got := exitCodeOf(err); got != 4 {
		t.Errorf("exit code = %d, want 4 (conflict)\n%s", got, stderr)
	}
	if !strings.Contains(stderr, "correspondent") {
		t.Errorf("refusal should say why: %s", stderr)
	}

	list, _, _ := runMail(t, bin, env, "list", "architect")
	if n := strings.Count(list, "triage packet for the fleet"); n != 2 {
		t.Errorf("the refused run moved mail anyway: %d of 2 left\n%s", n, list)
	}

	// --force is the deliberate override.
	out, _, err = runReclaim(t, bin, env, "architect", "--from=pm-pogo", "--force")
	if err != nil {
		t.Fatalf("--force should allow it: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 2") {
		t.Errorf("--force run should reclaim the superseded copy, got:\n%s", out)
	}
}

// TestCLI_MailReclaimDryRunMovesNothing.
func TestCLI_MailReclaimDryRunMovesNothing(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, _, err := runReclaim(t, bin, env, "architect", "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Would reclaim") || !strings.Contains(out, "would be reclaimed") {
		t.Errorf("dry run must not report in the past tense:\n%s", out)
	}
	if strings.Contains(out, "Nothing was deleted") {
		t.Errorf("dry run should not claim it archived anything:\n%s", out)
	}

	list, _, _ := runMail(t, bin, env, "list", "architect")
	if n := strings.Count(list, "mail-check-architect"); n != 8 {
		t.Errorf("dry run moved mail: %d of 8 scheduler copies left\n%s", n, list)
	}
}

// TestCLI_MailReclaimJSONShape: one object per touched mailbox, then a summary
// object that is always emitted and carries no "mailbox" field.
func TestCLI_MailReclaimJSONShape(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, _, err := runReclaim(t, bin, env, "--json", "--dry-run")
	if err != nil {
		t.Fatalf("json reclaim failed: %v\n%s", err, out)
	}

	var boxes []map[string]any
	var summary map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line is not JSON: %q (%v)", line, err)
		}
		if _, ok := obj["mailbox"]; ok {
			boxes = append(boxes, obj)
		} else {
			summary = obj
		}
	}
	if len(boxes) != 2 {
		t.Errorf("want 2 per-mailbox objects (architect, mayor), got %d:\n%s", len(boxes), out)
	}
	if summary == nil {
		t.Fatalf("no summary object emitted:\n%s", out)
	}
	if got := summary["reclaimed"].(float64); got != 11 {
		t.Errorf("summary reclaimed = %v, want 11", got)
	}
	if got := summary["dry_run"].(bool); !got {
		t.Errorf("summary dry_run = %v, want true", got)
	}
	from, _ := summary["from"].([]any)
	if len(from) != 1 || from[0] != "scheduler" {
		t.Errorf("summary from = %v, want [scheduler]", summary["from"])
	}

	// A summary is emitted even when nothing matched, so no consumer is handed
	// an empty stream to interpret.
	out, _, err = runReclaim(t, bin, env, "pm-pogo", "--json")
	if err != nil {
		t.Fatalf("empty-box json reclaim failed: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &summary); err != nil {
		t.Fatalf("empty run should emit exactly one summary object, got %q", out)
	}
	if summary["reclaimed"].(float64) != 0 {
		t.Errorf("empty run summary reclaimed = %v, want 0", summary["reclaimed"])
	}
}

// TestCLI_MailReclaimRefusesUnusableFlagValues: the spellings that could only
// destroy more than the caller meant are refused at the callsite.
func TestCLI_MailReclaimRefusesUnusableFlagValues(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"keep below 1", []string{"architect", "--keep=0"}, "--keep must be at least 1"},
		{"negative window", []string{"architect", "--older-than=-1h"}, "must not be negative"},
		{"unparseable window", []string{"architect", "--older-than=soon"}, "--older-than"},
		{"empty sender", []string{"architect", "--from="}, "--from"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runMail(t, bin, env, append([]string{"reclaim"}, tc.args...)...)
			if err == nil {
				t.Fatalf("expected refusal, got success:\n%s", out)
			}
			if got := exitCodeOf(err); got != 2 && got != 4 {
				t.Errorf("exit code = %d, want a usage/conflict refusal\n%s", got, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr should mention %q, got: %s", tc.want, stderr)
			}
		})
	}

	list, _, _ := runMail(t, bin, env, "list", "architect")
	if n := strings.Count(list, "mail-check-architect"); n != 8 {
		t.Errorf("a refused run moved mail: %d of 8 left\n%s", n, list)
	}
}

// TestCLI_MailReclaimReportIsBoundedAndKeepsItsTotals is the enumeration check
// on the remedy: a command that drains a wall of rows must not print one. With
// more touched mailboxes than the cap, the rows are truncated with a named
// remainder and the totals still land.
func TestCLI_MailReclaimReportIsBoundedAndKeepsItsTotals(t *testing.T) {
	bin, env := mailInit(t)

	const boxes = maxReclaimRows + 4
	for i := 0; i < boxes; i++ {
		box := fmt.Sprintf("box%02d", i)
		for j := 0; j < 2; j++ {
			if out, _, err := runMail(t, bin, env, "send", box, "--create",
				"--from=scheduler", "--subject=scheduler: mail-check-"+box, "--body=b"); err != nil {
				t.Fatalf("send to %s failed: %v\n%s", box, err, out)
			}
		}
	}

	out, _, err := runReclaim(t, bin, env)
	if err != nil {
		t.Fatalf("fleet reclaim failed: %v\n%s", err, out)
	}

	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  box") {
			rows++
		}
	}
	if rows != maxReclaimRows {
		t.Errorf("printed %d mailbox rows, want the cap of %d", rows, maxReclaimRows)
	}
	if !strings.Contains(out, "4 more mailbox(es)") {
		t.Errorf("truncated rows must be counted, not dropped silently:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d of %d", boxes, boxes*2)) {
		t.Errorf("totals must survive the truncation, want %d of %d:\n%s", boxes, boxes*2, out)
	}

	// --json is the unbounded form, so nothing is only ever summarized.
	jsonOut, _, err := runReclaim(t, bin, env, "--json", "--dry-run")
	if err != nil {
		t.Fatalf("json reclaim failed: %v\n%s", err, jsonOut)
	}
	if n := strings.Count(jsonOut, `"mailbox"`); n != 0 {
		t.Errorf("second run should have nothing left to report, got %d rows", n)
	}
}

// TestCLI_MailReclaimNeverExistedIsNotNothingToReclaim: "Nothing to reclaim"
// reads as "this box is clean", which is the wrong answer for a box that is not
// a box (mg-d639's distinction, in a command that acts on mailboxes).
func TestCLI_MailReclaimNeverExistedIsNotNothingToReclaim(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	out, _, err := runReclaim(t, bin, env, "architekt")
	if err != nil {
		t.Fatalf("asking after a missing mailbox should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No such mailbox: architekt") {
		t.Errorf("want a missing-mailbox report, got:\n%s", out)
	}
	if strings.Contains(out, "Nothing to reclaim") {
		t.Errorf("a nonexistent mailbox must not read as a clean one:\n%s", out)
	}
	if !strings.Contains(out, "architect") {
		t.Errorf("want the near neighbour suggested, got:\n%s", out)
	}

	out, _, err = runReclaim(t, bin, env, "architekt", "--json")
	if err != nil {
		t.Fatalf("json form failed: %v\n%s", err, out)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &summary); err != nil {
		t.Fatalf("want one summary object, got %q", out)
	}
	if summary["mailboxes"].(float64) != 0 {
		t.Errorf("mailboxes = %v for a nonexistent box, want 0", summary["mailboxes"])
	}

	// An existing but reclaim-free box is the other side of the distinction.
	out, _, err = runReclaim(t, bin, env, "pm-pogo", "--json")
	if err != nil {
		t.Fatalf("existing-box json form failed: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &summary); err != nil {
		t.Fatalf("want one summary object, got %q", out)
	}
	if summary["mailboxes"].(float64) != 1 {
		t.Errorf("mailboxes = %v for an existing box, want 1", summary["mailboxes"])
	}
}

// TestCLI_MailReclaimIsIdempotent backs the schema's idempotent hint.
func TestCLI_MailReclaimIsIdempotent(t *testing.T) {
	bin, env := mailInit(t)
	reclaimFixture(t, bin, env)

	if out, _, err := runReclaim(t, bin, env, "architect"); err != nil {
		t.Fatalf("first reclaim failed: %v\n%s", err, out)
	}
	out, _, err := runReclaim(t, bin, env, "architect")
	if err != nil {
		t.Fatalf("second reclaim failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to reclaim") {
		t.Errorf("second run should find nothing, got:\n%s", out)
	}
}

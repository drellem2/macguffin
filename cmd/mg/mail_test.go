package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_MailListMalformedWarning: a truncated file in new/ must produce a
// visible warning in mg mail list output, not a silent skip (mg-9696).
func TestCLI_MailListMalformedWarning(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

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
	env := append(os.Environ(), "HOME="+tmpHome)

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

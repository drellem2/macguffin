package mail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
)

// eventTestRoots returns a workspace root and its mail root laid out the way
// mg does it (<workspace>/mail), so mail events land in <workspace>/events.jsonl.
func eventTestRoots(t *testing.T) (workRoot, mailRoot string) {
	t.Helper()
	workRoot = t.TempDir()
	return workRoot, filepath.Join(workRoot, "mail")
}

func eventsOfType(t *testing.T, workRoot, eventType string) []event.Entry {
	t.Helper()
	entries, err := event.List(workRoot, event.ListOpts{Type: eventType})
	if err != nil {
		t.Fatalf("listing %s events: %v", eventType, err)
	}
	return entries
}

func TestEnsureMaildir(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatalf("EnsureMaildir failed: %v", err)
	}

	for _, sub := range []string{"tmp", "new", "cur"} {
		path := filepath.Join(root, "arch", sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}
}

func TestSend_AtomicDelivery(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Review needed", "Please review the auth refactor.")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if msgID == "" {
		t.Fatal("Send returned empty message ID")
	}

	// Message should be in new/, not in tmp/
	newPath := filepath.Join(root, "arch", "new", msgID)
	tmpPath := filepath.Join(root, "arch", "tmp", msgID)

	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("message should exist in new/: %v", err)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("message should NOT exist in tmp/ after delivery")
	}

	// Verify content
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading message: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "From: mayor") {
		t.Error("message should contain From header")
	}
	if !strings.Contains(content, "Subject: Review needed") {
		t.Error("message should contain Subject header")
	}
	if !strings.Contains(content, "Please review the auth refactor.") {
		t.Error("message should contain body")
	}
}

func TestList_ReturnsUnreadMessages(t *testing.T) {
	root := t.TempDir()

	// Send two messages
	_, err := Send(root, "arch", "mayor", "First", "body1")
	if err != nil {
		t.Fatalf("Send 1 failed: %v", err)
	}
	_, err = Send(root, "arch", "witness", "Second", "body2")
	if err != nil {
		t.Fatalf("Send 2 failed: %v", err)
	}

	msgs, _, err := List(root, "arch")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestList_EmptyMailbox(t *testing.T) {
	root := t.TempDir()
	msgs, _, err := List(root, "nobody")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestRead_MovesToCur(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Review needed", "Please review.")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify in new/ before read
	newEntries, _ := os.ReadDir(filepath.Join(root, "arch", "new"))
	if len(newEntries) != 1 {
		t.Fatalf("expected 1 message in new/, got %d", len(newEntries))
	}

	// Read the message
	msg, err := Read(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if msg.From != "mayor" {
		t.Errorf("From = %q, want %q", msg.From, "mayor")
	}
	if msg.Subject != "Review needed" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Review needed")
	}
	if msg.Body != "Please review." {
		t.Errorf("Body = %q, want %q", msg.Body, "Please review.")
	}

	// After read: should be in cur/, not in new/
	newEntries, _ = os.ReadDir(filepath.Join(root, "arch", "new"))
	if len(newEntries) != 0 {
		t.Errorf("expected 0 messages in new/ after read, got %d", len(newEntries))
	}
	curEntries, _ := os.ReadDir(filepath.Join(root, "arch", "cur"))
	if len(curEntries) != 1 {
		t.Errorf("expected 1 message in cur/ after read, got %d", len(curEntries))
	}
}

func TestRead_AlreadyInCur(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Already read", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Read once (moves to cur/)
	_, err = Read(root, "arch", msgID)
	if err != nil {
		t.Fatalf("first Read failed: %v", err)
	}

	// Read again (should still work from cur/)
	msg, err := Read(root, "arch", msgID)
	if err != nil {
		t.Fatalf("second Read failed: %v", err)
	}
	if msg.Subject != "Already read" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Already read")
	}
}

func TestRead_NotFound(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}

	_, err := Read(root, "arch", "nonexistent")
	if err == nil {
		t.Error("expected error reading nonexistent message")
	}
}

func TestListAll_IncludesReadAndUnread(t *testing.T) {
	root := t.TempDir()

	// Send two messages
	msgID1, err := Send(root, "arch", "mayor", "First", "body1")
	if err != nil {
		t.Fatalf("Send 1 failed: %v", err)
	}
	_, err = Send(root, "arch", "witness", "Second", "body2")
	if err != nil {
		t.Fatalf("Send 2 failed: %v", err)
	}

	// Read the first message (moves to cur/)
	_, err = Read(root, "arch", msgID1)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// List (unread only) should return 1
	unread, _, err := List(root, "arch")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected 1 unread message, got %d", len(unread))
	}

	// ListAll should return 2
	all, _, err := ListAll(root, "arch")
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 total messages, got %d", len(all))
	}

	// Check read status
	readCount := 0
	unreadCount := 0
	for _, m := range all {
		if m.Read {
			readCount++
		} else {
			unreadCount++
		}
	}
	if readCount != 1 {
		t.Errorf("expected 1 read message, got %d", readCount)
	}
	if unreadCount != 1 {
		t.Errorf("expected 1 unread message, got %d", unreadCount)
	}
}

func TestArchive_FromCur(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Done", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Read moves it to cur/ (read mail), the common archive case.
	if _, err := Read(root, "arch", msgID); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	msg, err := Archive(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Archive failed: %v", err)
	}
	if msg.Subject != "Done" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Done")
	}

	// Gone from new/ and cur/, present in archive/.
	if entries, _ := os.ReadDir(filepath.Join(root, "arch", "cur")); len(entries) != 0 {
		t.Errorf("expected 0 messages in cur/ after archive, got %d", len(entries))
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "arch", "archive")); len(entries) != 1 {
		t.Errorf("expected 1 message in archive/, got %d", len(entries))
	}

	// No longer surfaced by List or ListAll.
	if msgs, _, _ := List(root, "arch"); len(msgs) != 0 {
		t.Errorf("expected 0 unread after archive, got %d", len(msgs))
	}
	if msgs, _, _ := ListAll(root, "arch"); len(msgs) != 0 {
		t.Errorf("expected 0 in ListAll after archive, got %d", len(msgs))
	}
}

func TestArchive_FromNew(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Unread", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Archive an unread message directly from new/.
	if _, err := Archive(root, "arch", msgID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	if entries, _ := os.ReadDir(filepath.Join(root, "arch", "new")); len(entries) != 0 {
		t.Errorf("expected 0 messages in new/ after archive, got %d", len(entries))
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "arch", "archive")); len(entries) != 1 {
		t.Errorf("expected 1 message in archive/, got %d", len(entries))
	}
}

func TestArchive_Idempotent(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "Twice", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if _, err := Archive(root, "arch", msgID); err != nil {
		t.Fatalf("first Archive failed: %v", err)
	}
	// Archiving again should succeed and return the already-archived message.
	msg, err := Archive(root, "arch", msgID)
	if err != nil {
		t.Fatalf("second Archive failed: %v", err)
	}
	if msg.Subject != "Twice" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Twice")
	}
}

func TestArchive_NotFound(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(root, "arch", "nonexistent"); err == nil {
		t.Error("expected error archiving nonexistent message")
	}
}

func TestListArchived(t *testing.T) {
	root := t.TempDir()

	// No archive/ dir yet → empty, no error.
	if msgs, _, err := ListArchived(root, "arch"); err != nil {
		t.Fatalf("ListArchived on empty mailbox failed: %v", err)
	} else if len(msgs) != 0 {
		t.Errorf("expected 0 archived messages, got %d", len(msgs))
	}

	msgID, err := Send(root, "arch", "mayor", "Stale", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Archive(root, "arch", msgID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	msgs, _, err := ListArchived(root, "arch")
	if err != nil {
		t.Fatalf("ListArchived failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 archived message, got %d", len(msgs))
	}
	if msgs[0].Subject != "Stale" {
		t.Errorf("Subject = %q, want %q", msgs[0].Subject, "Stale")
	}
	if !msgs[0].Read {
		t.Errorf("archived message should be marked read")
	}

	// Archived mail must not leak into the active-mailbox listings.
	if active, _, _ := ListAll(root, "arch"); len(active) != 0 {
		t.Errorf("expected 0 messages in ListAll, got %d", len(active))
	}
}

func TestE2E_SendListReadLifecycle(t *testing.T) {
	root := t.TempDir()

	// Send a message
	msgID, err := Send(root, "arch", "mayor", "Review needed", "Please review the auth refactor.")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify file in new/
	newDir := filepath.Join(root, "arch", "new")
	entries, err := os.ReadDir(newDir)
	if err != nil {
		t.Fatalf("reading new/: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in new/, got %d", len(entries))
	}

	// List shows message
	msgs, _, err := List(root, "arch")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in list, got %d", len(msgs))
	}
	if msgs[0].Subject != "Review needed" {
		t.Errorf("Subject = %q, want %q", msgs[0].Subject, "Review needed")
	}

	// Read moves to cur/
	msg, err := Read(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if msg.From != "mayor" || msg.Subject != "Review needed" {
		t.Errorf("unexpected message content: %+v", msg)
	}

	// Verify: new/ is empty, cur/ has the message
	entries, _ = os.ReadDir(newDir)
	if len(entries) != 0 {
		t.Errorf("expected 0 files in new/ after read, got %d", len(entries))
	}
	curDir := filepath.Join(root, "arch", "cur")
	entries, _ = os.ReadDir(curDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file in cur/ after read, got %d", len(entries))
	}
}

func TestSendReadArchive_EmitEvents(t *testing.T) {
	workRoot, mailRoot := eventTestRoots(t)

	msgID, err := Send(mailRoot, "arch", "mayor", "Traced", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Read(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if _, err := Archive(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	sent := eventsOfType(t, workRoot, "mail.sent")
	if len(sent) != 1 {
		t.Fatalf("expected 1 mail.sent event, got %d", len(sent))
	}
	if sent[0].Extra["msg_id"] != msgID || sent[0].Extra["from"] != "mayor" || sent[0].Extra["to"] != "arch" {
		t.Errorf("mail.sent fields = %v, want msg_id=%s from=mayor to=arch", sent[0].Extra, msgID)
	}

	read := eventsOfType(t, workRoot, "mail.read")
	if len(read) != 1 {
		t.Fatalf("expected 1 mail.read event, got %d", len(read))
	}
	if read[0].Extra["msg_id"] != msgID || read[0].Extra["mailbox"] != "arch" {
		t.Errorf("mail.read fields = %v, want msg_id=%s mailbox=arch", read[0].Extra, msgID)
	}

	archived := eventsOfType(t, workRoot, "mail.archived")
	if len(archived) != 1 {
		t.Fatalf("expected 1 mail.archived event, got %d", len(archived))
	}
	if archived[0].Extra["msg_id"] != msgID || archived[0].Extra["mailbox"] != "arch" {
		t.Errorf("mail.archived fields = %v, want msg_id=%s mailbox=arch", archived[0].Extra, msgID)
	}
}

func TestArchive_IdempotentRepeatEmitsNoSecondEvent(t *testing.T) {
	workRoot, mailRoot := eventTestRoots(t)

	msgID, err := Send(mailRoot, "arch", "mayor", "Once", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Archive(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("first Archive failed: %v", err)
	}
	if _, err := Archive(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("second Archive failed: %v", err)
	}

	archived := eventsOfType(t, workRoot, "mail.archived")
	if len(archived) != 1 {
		t.Errorf("expected 1 mail.archived event after idempotent re-archive, got %d", len(archived))
	}
}

func TestList_MalformedCountedAndLogged(t *testing.T) {
	workRoot, mailRoot := eventTestRoots(t)

	if _, err := Send(mailRoot, "arch", "mayor", "Good", "body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	// A truncated transfer: headers cut off, no header/body separator.
	badPath := filepath.Join(mailRoot, "arch", "new", "9999.1.9999")
	if err := os.WriteFile(badPath, []byte("From: mayor\nSubj"), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, malformed, err := List(mailRoot, "arch")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 parseable message, got %d", len(msgs))
	}
	if malformed != 1 {
		t.Errorf("expected malformed count 1, got %d", malformed)
	}

	events := eventsOfType(t, workRoot, "mail.malformed")
	if len(events) != 1 {
		t.Fatalf("expected 1 mail.malformed event, got %d", len(events))
	}
	e := events[0].Extra
	if e["msg_id"] != "9999.1.9999" || e["mailbox"] != "arch" || e["dir"] != "new" {
		t.Errorf("mail.malformed fields = %v", e)
	}
	if !strings.Contains(e["error"], "malformed") {
		t.Errorf("mail.malformed error = %q, want it to mention malformed", e["error"])
	}
}

func TestListAll_CountsMalformedAcrossNewAndCur(t *testing.T) {
	_, mailRoot := eventTestRoots(t)

	if err := EnsureMaildir(mailRoot, "arch"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"new", "cur"} {
		p := filepath.Join(mailRoot, "arch", sub, "bad-"+sub)
		if err := os.WriteFile(p, []byte("no separator here"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	msgs, malformed, err := ListAll(mailRoot, "arch")
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 parseable messages, got %d", len(msgs))
	}
	if malformed != 2 {
		t.Errorf("expected malformed count 2, got %d", malformed)
	}
}

func TestRead_MalformedFailsLoud(t *testing.T) {
	_, mailRoot := eventTestRoots(t)

	if err := EnsureMaildir(mailRoot, "arch"); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(mailRoot, "arch", "new", "trunc.1.1")
	if err := os.WriteFile(badPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Read(mailRoot, "arch", "trunc.1.1")
	if err == nil {
		t.Fatal("expected error reading malformed message")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("error = %v, want it to wrap ErrMalformed", err)
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v; a malformed message must not be reported as not found", err)
	}
}

func auditLines(t *testing.T, mailRoot string) []string {
	t.Helper()
	data, err := os.ReadFile(AuditLogPath(mailRoot))
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestAudit_LifecycleLines(t *testing.T) {
	t.Setenv("POGO_AGENT_NAME", "auditor")
	workRoot, mailRoot := eventTestRoots(t)

	msgID, err := Send(mailRoot, "arch", "mayor", "Traced", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Read(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	// Re-read from cur/ is audited too, with from_dir=cur.
	if _, err := Read(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("second Read failed: %v", err)
	}
	if _, err := Archive(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	wantPath := filepath.Join(workRoot, "log", "mail-audit.log")
	if got := AuditLogPath(mailRoot); got != wantPath {
		t.Errorf("AuditLogPath = %q, want %q", got, wantPath)
	}

	lines := auditLines(t, mailRoot)
	if len(lines) != 4 {
		t.Fatalf("expected 4 audit lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	wantOps := []struct {
		op    string
		extra string
	}{
		{"send", "from=mayor"},
		{"read", "from_dir=new"},
		{"read", "from_dir=cur"},
		{"archive", "from_dir=cur"},
	}
	pid := fmt.Sprintf("pid=%d", os.Getpid())
	for i, want := range wantOps {
		line := lines[i]
		for _, frag := range []string{"ts=", "op=" + want.op, "box=arch", "msg=" + msgID, pid, "caller=auditor", want.extra} {
			if !strings.Contains(line, frag) {
				t.Errorf("audit line %d = %q, missing %q", i, line, frag)
			}
		}
	}
}

func TestAudit_CallerUnsetLogsDash(t *testing.T) {
	t.Setenv("POGO_AGENT_NAME", "")
	_, mailRoot := eventTestRoots(t)

	if _, err := Send(mailRoot, "arch", "mayor", "Anon", "body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	lines := auditLines(t, mailRoot)
	if len(lines) != 1 {
		t.Fatalf("expected 1 audit line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "caller=-") {
		t.Errorf("audit line = %q, want caller=- when POGO_AGENT_NAME is unset", lines[0])
	}
}

func TestAudit_ArchiveFromNew(t *testing.T) {
	_, mailRoot := eventTestRoots(t)

	msgID, err := Send(mailRoot, "arch", "mayor", "Unread", "body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Archive(mailRoot, "arch", msgID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	lines := auditLines(t, mailRoot)
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}
	if !strings.Contains(lines[1], "op=archive") || !strings.Contains(lines[1], "from_dir=new") {
		t.Errorf("archive audit line = %q, want op=archive from_dir=new", lines[1])
	}
}

func TestParseMessageFile_Malformed(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"no separator", "From: mayor\nSubject: cut off"},
		{"no known headers", "garbage line\n\nsome body"},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, tc.name)
		if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := parseMessageFile(p, tc.name)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", tc.name, err)
		}
	}
}

package mail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
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

func TestMailboxExists(t *testing.T) {
	root := t.TempDir()

	if MailboxExists(root, "ghost") {
		t.Error("MailboxExists should be false for a never-created mailbox")
	}

	// A delivered message creates the mailbox lazily.
	if _, err := Send(root, "arch", "mayor", "Hi", "body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !MailboxExists(root, "arch") {
		t.Error("MailboxExists should be true after first delivery")
	}

	// An existing-but-empty mailbox (all mail read/archived) still exists.
	if EnsureMaildir(root, "empty") != nil {
		t.Fatal("EnsureMaildir failed")
	}
	if !MailboxExists(root, "empty") {
		t.Error("MailboxExists should be true for an existing empty mailbox")
	}
}

func TestListMailboxes(t *testing.T) {
	root := t.TempDir()

	// Missing mail root -> empty, not an error.
	boxes, err := ListMailboxes(root)
	if err != nil {
		t.Fatalf("ListMailboxes on missing root failed: %v", err)
	}
	if len(boxes) != 0 {
		t.Errorf("expected 0 mailboxes on missing root, got %d", len(boxes))
	}

	// Two messages to arch, one to witness; read one of arch's.
	msgID, err := Send(root, "arch", "mayor", "First", "b1")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Send(root, "arch", "mayor", "Second", "b2"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Send(root, "witness", "mayor", "Only", "b3"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := Read(root, "arch", msgID); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// An existing-but-empty mailbox must still be enumerated (unread 0).
	if err := EnsureMaildir(root, "aardvark"); err != nil {
		t.Fatal(err)
	}

	boxes, err = ListMailboxes(root)
	if err != nil {
		t.Fatalf("ListMailboxes failed: %v", err)
	}

	// Sorted by name: aardvark, arch, witness.
	want := []Mailbox{
		{Name: "aardvark", Unread: 0},
		{Name: "arch", Unread: 1}, // one of two read
		{Name: "witness", Unread: 1},
	}
	if len(boxes) != len(want) {
		t.Fatalf("expected %d mailboxes, got %d: %+v", len(want), len(boxes), boxes)
	}
	for i, w := range want {
		if boxes[i] != w {
			t.Errorf("mailbox %d = %+v, want %+v", i, boxes[i], w)
		}
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

// --- mg-ea5a: path-traversal + collision hardening ---------------------------

// traversalTokens are the msgID / mailbox values a caller must never be able
// to path-join into the mail root.
var traversalTokens = []string{
	"..",
	"../../../etc/passwd",
	"../cur/other",
	"sub/dir",
	`..\..\windows`,
	".",
	"",
}

// TestRead_RejectsTraversalMsgID: 'mg mail read AGENT MSG-ID' must not let a
// crafted MSG-ID escape the mailbox directory. The secret file next to the
// mail root stays unreadable and the error is a usage error (exit 2).
func TestRead_RejectsTraversalMsgID(t *testing.T) {
	workRoot, root := eventTestRoots(t)
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(workRoot, "secret")
	if err := os.WriteFile(secret, []byte("From: x\nSubject: s\nDate: d\n\ntop secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unguarded, filepath.Join(<root>, "arch", "new", "../../../secret")
	// resolves to the parseable file above — a read outside the mailbox.
	for _, tok := range append(traversalTokens, "../../../secret") {
		msg, err := Read(root, "arch", tok)
		if err == nil {
			t.Errorf("Read(msgID=%q) succeeded, want rejection (body=%q)", tok, msg.Body)
			continue
		}
		if strings.Contains(err.Error(), "top secret") {
			t.Errorf("Read(msgID=%q) leaked out-of-mailbox content", tok)
		}
		assertUsageError(t, err, tok)
	}
}

// TestRead_RejectsTraversalMailbox: the agent name is path-joined too, so it
// gets the same treatment as the msgID.
func TestRead_RejectsTraversalMailbox(t *testing.T) {
	root := t.TempDir()
	for _, tok := range traversalTokens {
		if _, err := Read(root, tok, "someid"); err == nil {
			t.Errorf("Read(agent=%q) succeeded, want rejection", tok)
		} else {
			assertUsageError(t, err, tok)
		}
	}
}

// TestArchive_RejectsTraversal: Archive moves files, so a traversing msgID
// would be a write outside the mailbox, not just a read.
func TestArchive_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range traversalTokens {
		if _, err := Archive(root, "arch", tok); err == nil {
			t.Errorf("Archive(msgID=%q) succeeded, want rejection", tok)
		} else {
			assertUsageError(t, err, tok)
		}
	}
}

// TestEnsureMaildir_RejectsTraversalMailbox: delivery to a traversing
// recipient must not create directories outside the mail root.
func TestEnsureMaildir_RejectsTraversalMailbox(t *testing.T) {
	workRoot, root := eventTestRoots(t)
	for _, tok := range traversalTokens {
		if err := EnsureMaildir(root, tok); err == nil {
			t.Errorf("EnsureMaildir(agent=%q) succeeded, want rejection", tok)
		} else {
			assertUsageError(t, err, tok)
		}
		if _, err := Send(root, tok, "mayor", "s", "b"); err == nil {
			t.Errorf("Send(recipient=%q) succeeded, want rejection", tok)
		} else {
			assertUsageError(t, err, tok)
		}
	}
	// Nothing escaped: <workRoot>/new must not exist ("../new" would land there).
	if _, err := os.Stat(filepath.Join(workRoot, "new")); !os.IsNotExist(err) {
		t.Errorf("traversing recipient created a directory outside the mail root")
	}
}

// TestList_RejectsTraversalMailbox pins the same guard on the listing path.
func TestList_RejectsTraversalMailbox(t *testing.T) {
	root := t.TempDir()
	if _, _, err := List(root, "../.."); err == nil {
		t.Error("List with traversing agent succeeded, want rejection")
	}
	if MailboxExists(root, "../..") {
		t.Error("MailboxExists reported true for a traversing agent name")
	}
}

// controlCharTokens are mailbox / msgID values that are legal unix path
// components but illegal audit-log field values: the audit record is
// newline-delimited "key=value" text, so a newline in a component forges a
// record. CR, NUL and DEL ride along on the same rule.
var controlCharTokens = []string{
	"box\nts=2020-01-01T00:00:00Z op=send box=mayor msg=forged pid=1 caller=root",
	"box\r\nop=forged",
	"box\rmayor",
	"box\x00mayor",
	"box\x7fmayor",
	"box\tmayor",
	"\nleading",
	"trailing\n",
}

// TestSend_RejectsControlCharsInMailbox is the regression for the audit-log
// forgery: Audit interpolates the mailbox name into "... box=<mailbox> ...",
// one line per record, so a newline-bearing recipient used to append a second,
// fully attacker-controlled record — arbitrary ts, op, msg, pid and caller.
// The name must be refused as a usage error BEFORE anything is written, so the
// audit log is never created at all.
func TestSend_RejectsControlCharsInMailbox(t *testing.T) {
	for _, tok := range controlCharTokens {
		t.Run(fmt.Sprintf("%q", tok), func(t *testing.T) {
			workRoot, root := eventTestRoots(t)

			if _, err := Send(root, tok, "attacker", "s", "b"); err == nil {
				t.Fatalf("Send(recipient=%q) succeeded, want rejection", tok)
			} else {
				assertUsageError(t, err, tok)
			}

			// The forged record must never reach disk: no audit write means no
			// audit file, since Audit creates it lazily on first append.
			if data, err := os.ReadFile(AuditLogPath(root)); err == nil {
				t.Errorf("rejected send wrote the audit log: %q", data)
			} else if !os.IsNotExist(err) {
				t.Errorf("stat audit log: %v", err)
			}

			// Nor may the rejection have created the mailbox it named.
			if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
				t.Errorf("rejected send created %d entries under the mail root", len(entries))
			}
			if _, err := os.Stat(filepath.Join(workRoot, "log")); !os.IsNotExist(err) {
				t.Errorf("rejected send created the log directory")
			}
		})
	}
}

// TestControlCharsRejectedOnEveryComponentPath: the mailbox name reaches Audit
// from the read and archive paths too, and a msgID is interpolated into the
// same record as msg=<msgID>. Every entry point that path-joins a component
// must apply the same rule.
func TestControlCharsRejectedOnEveryComponentPath(t *testing.T) {
	for _, tok := range controlCharTokens {
		t.Run(fmt.Sprintf("%q", tok), func(t *testing.T) {
			root := t.TempDir()
			if err := EnsureMaildir(root, "arch"); err != nil {
				t.Fatal(err)
			}

			// As a mailbox name.
			if err := EnsureMaildir(root, tok); err == nil {
				t.Errorf("EnsureMaildir(agent=%q) succeeded, want rejection", tok)
			} else {
				assertUsageError(t, err, tok)
			}
			if _, err := Read(root, tok, "someid"); err == nil {
				t.Errorf("Read(agent=%q) succeeded, want rejection", tok)
			}
			if _, _, err := List(root, tok); err == nil {
				t.Errorf("List(agent=%q) succeeded, want rejection", tok)
			}
			if MailboxExists(root, tok) {
				t.Errorf("MailboxExists(%q) reported true", tok)
			}

			// As a msgID.
			if _, err := Read(root, "arch", tok); err == nil {
				t.Errorf("Read(msgID=%q) succeeded, want rejection", tok)
			} else {
				assertUsageError(t, err, tok)
			}
			if _, err := Archive(root, "arch", tok); err == nil {
				t.Errorf("Archive(msgID=%q) succeeded, want rejection", tok)
			} else {
				assertUsageError(t, err, tok)
			}
			if _, err := Peek(root, "arch", tok); err == nil {
				t.Errorf("Peek(msgID=%q) succeeded, want rejection", tok)
			} else {
				assertUsageError(t, err, tok)
			}
		})
	}
}

// TestSend_AcceptsOrdinaryMailboxNames guards the tightened rule from
// overreach: agent names are ASCII-ish identifiers, and the ones macguffin
// actually mints (mayor, human, mg-21a6, pm-pogo) must keep working.
func TestSend_AcceptsOrdinaryMailboxNames(t *testing.T) {
	for _, name := range []string{"mayor", "human", "mg-21a6", "pm-pogo", "a.b_c", "café"} {
		root := t.TempDir()
		if _, err := Send(root, name, "mayor", "s", "b"); err != nil {
			t.Errorf("Send(recipient=%q) rejected an ordinary mailbox name: %v", name, err)
		}
	}
}

// assertUsageError checks the rejection is a typed usage (exit 2) error rather
// than a bare fs error, so the CLI renders it as caller misuse.
func assertUsageError(t *testing.T, err error, tok string) {
	t.Helper()
	var me *mgerr.Error
	if !errors.As(err, &me) {
		t.Errorf("token %q: err = %v (%T), want *mgerr.Error", tok, err, err)
		return
	}
	if me.Category != mgerr.CatUsage {
		t.Errorf("token %q: category = %v, want usage", tok, me.Category)
	}
}

// TestDeliverOnce_CollisionDoesNotOverwrite is the regression for the silent
// mail drop: delivering a second message under an already-taken msgID must
// fail rather than replace the delivered message.
func TestDeliverOnce_CollisionDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	const id = "1234.5678.9"
	first := "From: mayor\nSubject: first\nDate: d\n\nkeep me\n"
	second := "From: attacker\nSubject: second\nDate: d\n\nclobbered\n"

	if err := deliverOnce(root, "arch", id, first); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	err := deliverOnce(root, "arch", id, second)
	if !errors.Is(err, errCollision) {
		t.Fatalf("second delivery err = %v, want errCollision", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "arch", "new", id))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != first {
		t.Errorf("colliding delivery overwrote the message:\n got %q\nwant %q", got, first)
	}

	// The tmp/ spool is left clean after both the success and the collision.
	entries, err := os.ReadDir(filepath.Join(root, "arch", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("tmp/ not cleaned up: %d leftover file(s)", len(entries))
	}
}

// squatterBody is the content of the already-delivered message the collision
// tests protect.
const squatterBody = "From: mayor\nSubject: squatter\nDate: d\n\nkeep me\n"

// stubMint replaces the msgID minter for the duration of the test, handing out
// each id in turn and repeating the last one forever after, so a collision the
// real clock makes vanishingly rare becomes deterministic.
func stubMint(t *testing.T, ids ...string) {
	t.Helper()
	orig := mintMsgID
	t.Cleanup(func() { mintMsgID = orig })
	i := 0
	mintMsgID = func() string {
		id := ids[i]
		if i < len(ids)-1 {
			i++
		}
		return id
	}
}

// TestSend_ReMintsOnCollision: a taken msgID costs Send a re-mint, not the
// delivery. The pre-existing message survives untouched and the new one lands
// under the next id.
func TestSend_ReMintsOnCollision(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	const taken, fresh = "taken.id.1", "fresh.id.2"
	if err := deliverOnce(root, "arch", taken, squatterBody); err != nil {
		t.Fatal(err)
	}
	stubMint(t, taken, taken, fresh)

	msgID, err := Send(root, "arch", "mayor", "second", "body")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msgID != fresh {
		t.Errorf("msgID = %q, want the re-minted %q", msgID, fresh)
	}

	got, err := os.ReadFile(filepath.Join(root, "arch", "new", taken))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != squatterBody {
		t.Errorf("Send overwrote the message holding the taken id:\n got %q\nwant %q", got, squatterBody)
	}

	msgs, malformed, err := List(root, "arch")
	if err != nil {
		t.Fatal(err)
	}
	if malformed != 0 {
		t.Errorf("malformed = %d, want 0", malformed)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (the squatter must survive)", len(msgs))
	}
}

// TestSend_FailsLoudlyWhenCollisionPersists: if every re-mint collides, Send
// reports an error instead of overwriting. A failed send is recoverable;
// clobbering delivered mail is not.
func TestSend_FailsLoudlyWhenCollisionPersists(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMaildir(root, "arch"); err != nil {
		t.Fatal(err)
	}
	const taken = "taken.id.1"
	if err := deliverOnce(root, "arch", taken, squatterBody); err != nil {
		t.Fatal(err)
	}
	stubMint(t, taken)

	if _, err := Send(root, "arch", "mayor", "second", "body"); err == nil {
		t.Fatal("Send succeeded despite a permanently colliding msgID")
	}

	got, err := os.ReadFile(filepath.Join(root, "arch", "new", taken))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != squatterBody {
		t.Errorf("failed Send still overwrote the delivered message: got %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(root, "arch", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("tmp/ not cleaned up after failed delivery: %d leftover file(s)", len(entries))
	}
}

// TestSend_ManyDeliveriesAreUnique: no delivery in a tight loop clobbers a
// prior one. Before the collision guard a same-nanosecond mint silently
// replaced an already-delivered message.
func TestSend_ManyDeliveriesAreUnique(t *testing.T) {
	root := t.TempDir()
	const n = 200
	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id, err := Send(root, "arch", "mayor", fmt.Sprintf("msg %d", i), "body")
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if ids[id] {
			t.Fatalf("send %d: duplicate msgID %q", i, id)
		}
		ids[id] = true
	}
	msgs, _, err := List(root, "arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != n {
		t.Errorf("delivered %d messages, mailbox holds %d — mail was dropped", n, len(msgs))
	}
}

// --- correlation headers (Message-Id / In-Reply-To / References) -----------

// TestSend_StampsMessageIDMatchingFilename: every delivered message carries a
// Message-Id header, and it is the msgID — which is the maildir file name. The
// file name IS the message's identity; there is no second id space.
func TestSend_StampsMessageIDMatchingFilename(t *testing.T) {
	root := t.TempDir()

	msgID, err := Send(root, "arch", "mayor", "hello", "body")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "arch", "new", msgID))
	if err != nil {
		t.Fatalf("delivered file not named by msgID: %v", err)
	}
	if want := "Message-Id: " + msgID + "\n"; !strings.Contains(string(data), want) {
		t.Errorf("message file missing %q:\n%s", want, data)
	}

	// ...and it round-trips back out through the parser.
	msg, err := Peek(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if msg.MessageID != msgID {
		t.Errorf("parsed MessageID = %q, want %q", msg.MessageID, msgID)
	}
	if msg.ID != msg.MessageID {
		t.Errorf("ID %q and MessageID %q disagree — they are the same identity", msg.ID, msg.MessageID)
	}
	if msg.InReplyTo != "" || len(msg.References) != 0 {
		t.Errorf("unthreaded message carries correlation headers: in-reply-to=%q refs=%v", msg.InReplyTo, msg.References)
	}
}

// TestSendWithOpts_CorrelationHeadersRoundTrip: In-Reply-To and References are
// written when set and parse back to the same values.
func TestSendWithOpts_CorrelationHeadersRoundTrip(t *testing.T) {
	root := t.TempDir()
	opts := SendOpts{InReplyTo: "parent.id.1", References: []string{"root.id.0", "parent.id.1"}}

	msgID, err := SendWithOpts(root, "arch", "mayor", "re: hello", "body", opts)
	if err != nil {
		t.Fatalf("SendWithOpts: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "arch", "new", msgID))
	if err != nil {
		t.Fatal(err)
	}
	// References is a single space-separated header line, oldest id first.
	if want := "References: root.id.0 parent.id.1\n"; !strings.Contains(string(data), want) {
		t.Errorf("missing %q:\n%s", want, data)
	}

	msg, err := Peek(root, "arch", msgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.InReplyTo != "parent.id.1" {
		t.Errorf("InReplyTo = %q, want parent.id.1", msg.InReplyTo)
	}
	if got := strings.Join(msg.References, ","); got != "root.id.0,parent.id.1" {
		t.Errorf("References = %v, want [root.id.0 parent.id.1]", msg.References)
	}
}

// TestSendWithOpts_ReferencesCappedKeepingNewest: a long thread keeps only the
// most recent maxReferences ids. The nearest ancestry — what a reader threads
// on — survives; the oldest ids fall off the front.
func TestSendWithOpts_ReferencesCappedKeepingNewest(t *testing.T) {
	root := t.TempDir()

	var refs []string
	for i := 0; i < maxReferences+5; i++ {
		refs = append(refs, fmt.Sprintf("id.%02d", i))
	}

	msgID, err := SendWithOpts(root, "arch", "mayor", "deep thread", "body", SendOpts{References: refs})
	if err != nil {
		t.Fatalf("SendWithOpts: %v", err)
	}

	msg, err := Peek(root, "arch", msgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.References) != maxReferences {
		t.Fatalf("References length = %d, want cap of %d", len(msg.References), maxReferences)
	}
	if first, last := msg.References[0], msg.References[maxReferences-1]; first != "id.05" || last != "id.24" {
		t.Errorf("cap dropped the wrong end: kept %s..%s, want id.05..id.24", first, last)
	}
}

// TestSend_RejectsHeaderInjection is the regression test for the CR/LF header
// injection: before the fix, a newline in --subject or --from ended the header
// it filled and started an attacker-chosen one. With In-Reply-To routing mail
// into threads, an injected In-Reply-To is thread hijacking.
//
// The assertion is twofold: the send is refused as a usage error (exit 2), and
// NOTHING lands on disk — no message, no tmp file, not even the lazily created
// mailbox.
func TestSend_RejectsHeaderInjection(t *testing.T) {
	inject := "pwned\nIn-Reply-To: attacker.id.1\nX-Evil: yes"

	cases := []struct {
		name          string
		from, subject string
		opts          SendOpts
	}{
		{name: "LF in subject", from: "mayor", subject: inject},
		{name: "LF in from", from: inject, subject: "s"},
		{name: "CR in subject", from: "mayor", subject: "a\rb"},
		{name: "CRLF in subject", from: "mayor", subject: "a\r\nX-Evil: yes"},
		{name: "NUL in from", from: "may\x00or", subject: "s"},
		{name: "DEL in subject", from: "mayor", subject: "a\x7fb"},
		{name: "tab in subject", from: "mayor", subject: "a\tb"},
		{name: "LF in in-reply-to", from: "mayor", subject: "s", opts: SendOpts{InReplyTo: "a\nX-Evil: yes"}},
		{name: "separator in in-reply-to", from: "mayor", subject: "s", opts: SendOpts{InReplyTo: "../../etc/passwd"}},
		{name: "space in reference", from: "mayor", subject: "s", opts: SendOpts{References: []string{"a b"}}},
		{name: "traversal in reference", from: "mayor", subject: "s", opts: SendOpts{References: []string{"ok.id.1", ".."}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()

			_, err := SendWithOpts(root, "arch", tc.from, tc.subject, "body", tc.opts)
			if err == nil {
				t.Fatal("Send accepted a header value carrying an injection")
			}

			var me *mgerr.Error
			if !errors.As(err, &me) {
				t.Fatalf("error is not a *mgerr.Error: %v", err)
			}
			if me.Category != mgerr.CatUsage {
				t.Errorf("category = %v, want CatUsage (exit 2)", me.Category)
			}
			if me.Code != "invalid_header_value" {
				t.Errorf("code = %q, want invalid_header_value", me.Code)
			}

			// A rejected send must leave no trace: the mailbox is created
			// lazily by Send, so a refusal should not have created it at all.
			if _, err := os.Stat(filepath.Join(root, "arch")); !os.IsNotExist(err) {
				t.Errorf("rejected send created the mailbox (stat err = %v)", err)
			}
		})
	}
}

// TestSend_AcceptsBodyNewlines: only HEADER values are line-constrained. A
// multi-line body is ordinary mail and must keep working.
func TestSend_AcceptsBodyNewlines(t *testing.T) {
	root := t.TempDir()
	body := "line one\nline two\n\nline four"

	msgID, err := Send(root, "arch", "mayor", "multiline", body)
	if err != nil {
		t.Fatalf("Send rejected a multi-line body: %v", err)
	}
	msg, err := Peek(root, "arch", msgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != body {
		t.Errorf("Body = %q, want %q", msg.Body, body)
	}
}

// TestPeek_DoesNotMarkRead: Peek is the non-destructive lookup Reply relies on
// to inspect a message before committing to send. Unlike Read it leaves the
// message in new/.
func TestPeek_DoesNotMarkRead(t *testing.T) {
	root := t.TempDir()
	msgID, err := Send(root, "arch", "mayor", "s", "b")
	if err != nil {
		t.Fatal(err)
	}

	msg, err := Peek(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if msg.Read {
		t.Error("Peek reported an unread message as read")
	}
	if _, err := os.Stat(filepath.Join(root, "arch", "new", msgID)); err != nil {
		t.Errorf("Peek moved the message out of new/: %v", err)
	}

	// After a real Read it is found in cur/ and reported read.
	if _, err := Read(root, "arch", msgID); err != nil {
		t.Fatal(err)
	}
	msg, err = Peek(root, "arch", msgID)
	if err != nil {
		t.Fatalf("Peek after Read: %v", err)
	}
	if !msg.Read {
		t.Error("Peek reported a cur/ message as unread")
	}
}

// TestPeek_RejectsTraversalAndMissing: Peek shares the read path's msgID
// validation and reports an absent message as not_found.
func TestPeek_RejectsTraversalAndMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := Send(root, "arch", "mayor", "s", "b"); err != nil {
		t.Fatal(err)
	}

	if _, err := Peek(root, "arch", "../../../etc/passwd"); err == nil {
		t.Error("Peek accepted a traversing msgID")
	}

	_, err := Peek(root, "arch", "nope.id.1")
	var me *mgerr.Error
	if !errors.As(err, &me) || me.Category != mgerr.CatNotFound {
		t.Errorf("Peek of a missing message: err = %v, want CatNotFound", err)
	}
}

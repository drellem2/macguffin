package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	msgs, err := List(root, "arch")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestList_EmptyMailbox(t *testing.T) {
	root := t.TempDir()
	msgs, err := List(root, "nobody")
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
	msgs, err := List(root, "arch")
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

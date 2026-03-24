package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Message represents a mail message in Maildir format.
type Message struct {
	ID      string
	From    string
	Subject string
	Date    string
	Body    string
	Read    bool
}

// EnsureMaildir creates the Maildir subdirectories (tmp, new, cur) for an agent.
func EnsureMaildir(mailRoot, agent string) error {
	for _, sub := range []string{"tmp", "new", "cur"} {
		dir := filepath.Join(mailRoot, agent, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return nil
}

// Send delivers a message to the recipient's mailbox using Maildir-style
// atomic delivery: write to tmp/, then rename to new/.
func Send(mailRoot, recipient, from, subject, body string) (string, error) {
	if err := EnsureMaildir(mailRoot, recipient); err != nil {
		return "", err
	}

	msgID := fmt.Sprintf("%d.%d.%d", time.Now().UnixNano(), os.Getpid(), time.Now().UnixNano()%10000)

	content := fmt.Sprintf("From: %s\nSubject: %s\nDate: %s\n\n%s\n",
		from, subject, time.Now().UTC().Format(time.RFC3339), body)

	tmpPath := filepath.Join(mailRoot, recipient, "tmp", msgID)
	newPath := filepath.Join(mailRoot, recipient, "new", msgID)

	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing to tmp: %w", err)
	}

	if err := os.Rename(tmpPath, newPath); err != nil {
		os.Remove(tmpPath) // best-effort cleanup
		return "", fmt.Errorf("atomic move tmp→new: %w", err)
	}

	return msgID, nil
}

// List returns all unread messages (in new/) for the given agent.
func List(mailRoot, agent string) ([]Message, error) {
	return listDir(mailRoot, agent, "new", false)
}

// ListAll returns all messages (both new/ and cur/) for the given agent.
func ListAll(mailRoot, agent string) ([]Message, error) {
	unread, err := listDir(mailRoot, agent, "new", false)
	if err != nil {
		return nil, err
	}
	read, err := listDir(mailRoot, agent, "cur", true)
	if err != nil {
		return nil, err
	}
	msgs := append(unread, read...)
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Date < msgs[j].Date
	})
	return msgs, nil
}

func listDir(mailRoot, agent, subdir string, read bool) ([]Message, error) {
	dir := filepath.Join(mailRoot, agent, subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s/: %w", subdir, err)
	}

	var msgs []Message
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		msg, err := parseMessageFile(filepath.Join(dir, e.Name()), e.Name())
		if err != nil {
			continue // skip malformed messages
		}
		msg.Read = read
		msgs = append(msgs, msg)
	}

	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Date < msgs[j].Date
	})

	return msgs, nil
}

// Read reads a message by ID from new/ and moves it to cur/ (marks as read).
func Read(mailRoot, agent, msgID string) (*Message, error) {
	newPath := filepath.Join(mailRoot, agent, "new", msgID)
	curDir := filepath.Join(mailRoot, agent, "cur")
	curPath := filepath.Join(curDir, msgID)

	// Try new/ first
	msg, err := parseMessageFile(newPath, msgID)
	if err != nil {
		// Maybe already in cur/?
		msg, err2 := parseMessageFile(curPath, msgID)
		if err2 != nil {
			return nil, fmt.Errorf("message %q not found: %w", msgID, err)
		}
		return &msg, nil
	}

	// Ensure cur/ exists
	if err := os.MkdirAll(curDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating cur/: %w", err)
	}

	// Move from new/ to cur/ (mark as read)
	if err := os.Rename(newPath, curPath); err != nil {
		return nil, fmt.Errorf("moving to cur/: %w", err)
	}

	return &msg, nil
}

// parseMessageFile reads and parses a Maildir message file.
func parseMessageFile(path, id string) (Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}

	msg := Message{ID: id}
	content := string(data)

	// Split headers from body at blank line
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) == 2 {
		msg.Body = strings.TrimSpace(parts[1])
	}

	// Parse headers
	for _, line := range strings.Split(parts[0], "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			switch k {
			case "From":
				msg.From = v
			case "Subject":
				msg.Subject = v
			case "Date":
				msg.Date = v
			}
		}
	}

	return msg, nil
}

package mail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// ErrMalformed marks a message file that exists but cannot be parsed as a
// mail message (truncated headers, missing header/body separator, ...).
// Check with errors.Is.
var ErrMalformed = errors.New("malformed message")

// errCollision reports that the target msgID is already taken in new/. It is
// internal to the delivery retry loop and never escapes Send.
var errCollision = errors.New("msgID collision")

// maxDeliveryAttempts bounds Send's msgID re-mint loop. A collision means two
// deliveries minted the same nanosecond-derived id; a fresh mint resolves it.
// Rather than overwrite the existing message (a silent mail drop) we re-mint,
// and after this many consecutive collisions we fail loudly.
const maxDeliveryAttempts = 5

// validComponent reports whether s is safe to path-join as a single mailbox or
// message-ID path component. Every mail function joins the agent name and the
// msgID straight onto the mail root, and filepath.Join resolves "..", so an
// unchecked msgID of "../../etc/passwd" reads outside the mailbox entirely.
// A component must be non-empty, must not be a directory-traversal token, and
// must contain no separator (either flavour, so a Windows-style "..\\" cannot
// slip through a unix build's checks) or NUL.
func validComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, `/\`+"\x00")
}

// checkMailbox validates an agent/mailbox name before it is path-joined.
func checkMailbox(agent string) error {
	if !validComponent(agent) {
		return mgerr.Usage("invalid_value", fmt.Sprintf("invalid mailbox name %q: must be a single path component with no separators or %q", agent, ".."), "")
	}
	return nil
}

// checkMsgID validates a message ID before it is path-joined. MSG-IDs come
// straight from the command line ('mg mail read AGENT MSG-ID'), so this is the
// boundary that keeps a crafted id from escaping the mailbox directory.
func checkMsgID(agent, msgID string) error {
	if err := checkMailbox(agent); err != nil {
		return err
	}
	if !validComponent(msgID) {
		return mgerr.Usage("invalid_value", fmt.Sprintf("invalid message id %q: must be a single path component with no separators or %q", msgID, ".."), "run 'mg mail list "+agent+"' to see valid message ids")
	}
	return nil
}

// eventsRoot returns the workspace root owning the events log for a mail
// root. By convention the mail root is <workspace>/mail, so mail events land
// in <workspace>/events.jsonl alongside the work-item events.
func eventsRoot(mailRoot string) string {
	return filepath.Dir(mailRoot)
}

// fsErrText extracts the underlying cause from an os.Rename/os.WriteFile style
// error (an *os.LinkError or *fs.PathError) WITHOUT the operation name or the
// file paths it embeds — those leak the maildir layout. For other errors it
// returns the error's own text.
func fsErrText(err error) string {
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// Maildir layout note: message files in new/, cur/, and archive/ are named
// by bare msgID only. Standard maildir ":2,S"-style flag suffixes are NOT
// used — read state is encoded purely by directory (new/ = unread,
// cur/ = read). A file carrying a foreign ":2,..." suffix is invisible to
// Read/Archive, which look files up by exact msgID; do not import
// flag-suffixed files from other maildir tooling.

// Message represents a mail message in Maildir format.
type Message struct {
	ID      string
	From    string
	Subject string
	Date    string
	Body    string
	Read    bool
}

// Mailbox summarizes one agent mailbox under the mail root: the agent name and
// the number of unread messages (parseable files in new/).
type Mailbox struct {
	Name   string
	Unread int
}

// MailboxExists reports whether a mailbox directory already exists under the
// mail root for the given agent. It underpins the first-delivery signal: mail
// is still delivered to (and the mailbox created for) a never-before-seen
// recipient, but callers can note that the recipient did not previously exist —
// the likely-typo case — instead of masking it. A false return means no mail
// has ever been delivered to that exact agent name.
func MailboxExists(mailRoot, agent string) bool {
	if !validComponent(agent) {
		return false
	}
	info, err := os.Stat(filepath.Join(mailRoot, agent))
	return err == nil && info.IsDir()
}

// ListMailboxes enumerates every mailbox directory under the mail root together
// with its unread count, sorted by name. This is the de-facto agent-identity
// list: macguffin has no separate registry, so the set of mailbox directories
// is the set of agents that have ever sent or received mail. A missing mail
// root yields an empty list (not an error) — nothing has been delivered yet.
func ListMailboxes(mailRoot string) ([]Mailbox, error) {
	entries, err := os.ReadDir(mailRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading mail root: %w", err)
	}

	boxes := make([]Mailbox, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Reuse List so the per-box unread count matches exactly what
		// `mg mail list <agent>` reports (malformed files excluded).
		msgs, _, err := List(mailRoot, e.Name())
		if err != nil {
			return nil, err
		}
		boxes = append(boxes, Mailbox{Name: e.Name(), Unread: len(msgs)})
	}

	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	return boxes, nil
}

// EnsureMaildir creates the Maildir subdirectories (tmp, new, cur) for an agent.
func EnsureMaildir(mailRoot, agent string) error {
	if err := checkMailbox(agent); err != nil {
		return err
	}
	for _, sub := range []string{"tmp", "new", "cur"} {
		dir := filepath.Join(mailRoot, agent, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return nil
}

// Send delivers a message to the recipient's mailbox using Maildir-style
// atomic delivery: write to tmp/, then link into new/. The returned msgID is
// the id the message was actually delivered under, which is not necessarily
// the first one minted — see deliverOnce.
func Send(mailRoot, recipient, from, subject, body string) (string, error) {
	// Checked before EnsureMaildir so a bad recipient surfaces as the usage
	// error it is, rather than being re-wrapped below as an internal io_error.
	if err := checkMailbox(recipient); err != nil {
		return "", err
	}
	if err := EnsureMaildir(mailRoot, recipient); err != nil {
		return "", mgerr.Wrap(mgerr.CatInternal, "io_error", fmt.Errorf("could not deliver message to %s: %s", recipient, fsErrText(err)), "")
	}

	content := fmt.Sprintf("From: %s\nSubject: %s\nDate: %s\n\n%s\n",
		from, subject, time.Now().UTC().Format(time.RFC3339), body)

	// Deliver under a freshly minted msgID, re-minting on collision. Delivery
	// never overwrites an existing message (see deliverOnce), so a collision
	// costs a retry rather than silently dropping already-delivered mail.
	var msgID string
	for attempt := 1; ; attempt++ {
		msgID = mintMsgID()
		err := deliverOnce(mailRoot, recipient, msgID, content)
		if err == nil {
			break
		}
		if !errors.Is(err, errCollision) {
			return "", mgerr.Wrap(mgerr.CatInternal, "io_error", fmt.Errorf("could not deliver message to %s: %s", recipient, fsErrText(err)), "")
		}
		if attempt == maxDeliveryAttempts {
			return "", mgerr.Wrap(mgerr.CatInternal, "io_error", fmt.Errorf("could not deliver message to %s: message id still colliding after %d attempts", recipient, maxDeliveryAttempts), "")
		}
	}

	event.Emit(eventsRoot(mailRoot), "mail.sent", map[string]string{
		"msg_id": msgID,
		"from":   from,
		"to":     recipient,
	})
	Audit(mailRoot, "send", recipient, msgID, map[string]string{"from": from})

	return msgID, nil
}

// mintMsgID generates a message ID from the wall clock and the sending pid.
// Uniqueness is best-effort only — deliverOnce, not this function, is what
// guarantees a colliding id cannot clobber a delivered message. It is a var so
// tests can force the collision a real clock makes vanishingly rare.
var mintMsgID = func() string {
	return fmt.Sprintf("%d.%d.%d", time.Now().UnixNano(), os.Getpid(), time.Now().UnixNano()%10000)
}

// deliverOnce performs one Maildir delivery attempt for msgID: write the
// content to tmp/, then hard-link it into new/ and unlink the tmp copy.
//
// The link is what makes delivery non-destructive. os.Rename would happily
// replace an existing new/<msgID>, so a msgID collision silently destroyed an
// already-delivered message — the mail-drop class the mail-bridge reliability
// arc exists to eliminate. link(2) instead fails with EEXIST, which we report
// as errCollision so Send can re-mint. The O_EXCL on tmp/ gives the same
// protection against clobbering another sender's in-flight delivery.
func deliverOnce(mailRoot, recipient, msgID, content string) error {
	tmpPath := filepath.Join(mailRoot, recipient, "tmp", msgID)
	newPath := filepath.Join(mailRoot, recipient, "new", msgID)

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errCollision
		}
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Link(tmpPath, newPath); err != nil {
		os.Remove(tmpPath) // best-effort cleanup
		if errors.Is(err, fs.ErrExist) {
			return errCollision
		}
		return err
	}

	os.Remove(tmpPath) // best-effort: the message now lives in new/
	return nil
}

// List returns all unread messages (in new/) for the given agent, plus a
// count of malformed message files that were skipped.
func List(mailRoot, agent string) ([]Message, int, error) {
	return listDir(mailRoot, agent, "new", false)
}

// ListAll returns all messages (both new/ and cur/) for the given agent, plus
// a count of malformed message files that were skipped.
func ListAll(mailRoot, agent string) ([]Message, int, error) {
	unread, badNew, err := listDir(mailRoot, agent, "new", false)
	if err != nil {
		return nil, 0, err
	}
	read, badCur, err := listDir(mailRoot, agent, "cur", true)
	if err != nil {
		return nil, 0, err
	}
	msgs := append(unread, read...)
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Date < msgs[j].Date
	})
	return msgs, badNew + badCur, nil
}

// ListArchived returns all archived messages (in archive/) for the given
// agent, plus a count of malformed message files that were skipped.
func ListArchived(mailRoot, agent string) ([]Message, int, error) {
	return listDir(mailRoot, agent, "archive", true)
}

func listDir(mailRoot, agent, subdir string, read bool) ([]Message, int, error) {
	if err := checkMailbox(agent); err != nil {
		return nil, 0, err
	}
	dir := filepath.Join(mailRoot, agent, subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("reading %s/: %w", subdir, err)
	}

	var msgs []Message
	malformed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		msg, err := parseMessageFile(filepath.Join(dir, e.Name()), e.Name())
		if err != nil {
			if os.IsNotExist(err) {
				// Consumed concurrently (e.g. another process moved it
				// new/ -> cur/ between ReadDir and ReadFile): not corruption.
				continue
			}
			malformed++
			fmt.Fprintf(os.Stderr, "warning: skipping malformed message %s/%s/%s: %s\n",
				agent, subdir, e.Name(), fsErrText(err))
			event.Emit(eventsRoot(mailRoot), "mail.malformed", map[string]string{
				"msg_id":  e.Name(),
				"mailbox": agent,
				"dir":     subdir,
				"error":   fsErrText(err),
			})
			continue
		}
		msg.Read = read
		msgs = append(msgs, msg)
	}

	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Date < msgs[j].Date
	})

	return msgs, malformed, nil
}

// Read reads a message by ID from new/ and moves it to cur/ (marks as read).
func Read(mailRoot, agent, msgID string) (*Message, error) {
	if err := checkMsgID(agent, msgID); err != nil {
		return nil, err
	}
	newPath := filepath.Join(mailRoot, agent, "new", msgID)
	curDir := filepath.Join(mailRoot, agent, "cur")
	curPath := filepath.Join(curDir, msgID)

	// Try new/ first
	msg, err := parseMessageFile(newPath, msgID)
	if err != nil {
		if errors.Is(err, ErrMalformed) {
			// Exists but unparseable → not_found category with the distinct
			// malformed_message slug; the wrapped ErrMalformed keeps errors.Is
			// working for internal callers.
			return nil, mgerr.Wrap(mgerr.CatNotFound, "malformed_message", fmt.Errorf("message %q in %s's mailbox: %w", msgID, agent, err), "")
		}
		// Maybe already in cur/?
		msg, err2 := parseMessageFile(curPath, msgID)
		if err2 != nil {
			if errors.Is(err2, ErrMalformed) {
				return nil, mgerr.Wrap(mgerr.CatNotFound, "malformed_message", fmt.Errorf("message %q in %s's mailbox: %w", msgID, agent, err2), "")
			}
			return nil, mgerr.Wrap(mgerr.CatNotFound, "no_such_message", fmt.Errorf("message %q not found: %w", msgID, err), "")
		}
		event.Emit(eventsRoot(mailRoot), "mail.read", map[string]string{
			"msg_id":  msgID,
			"mailbox": agent,
		})
		Audit(mailRoot, "read", agent, msgID, map[string]string{"from_dir": "cur"})
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

	event.Emit(eventsRoot(mailRoot), "mail.read", map[string]string{
		"msg_id":  msgID,
		"mailbox": agent,
	})
	Audit(mailRoot, "read", agent, msgID, map[string]string{"from_dir": "new"})

	return &msg, nil
}

// Archive moves a message by ID into archive/, removing it from the agent's
// active mailbox (new/ and cur/). The message may be unread (in new/) or read
// (in cur/); both are handled. If the message is already in archive/ it is
// returned without error (idempotent). Mirrors the new/→cur/ Read pattern.
func Archive(mailRoot, agent, msgID string) (*Message, error) {
	if err := checkMsgID(agent, msgID); err != nil {
		return nil, err
	}
	newPath := filepath.Join(mailRoot, agent, "new", msgID)
	curPath := filepath.Join(mailRoot, agent, "cur", msgID)
	archiveDir := filepath.Join(mailRoot, agent, "archive")
	archivePath := filepath.Join(archiveDir, msgID)

	// Locate the message: prefer cur/ (read mail), then new/ (unread).
	srcPath := ""
	if _, err := os.Stat(curPath); err == nil {
		srcPath = curPath
	} else if _, err := os.Stat(newPath); err == nil {
		srcPath = newPath
	}

	if srcPath == "" {
		// Maybe it's already archived?
		if msg, err := parseMessageFile(archivePath, msgID); err == nil {
			msg.Read = true
			return &msg, nil
		}
		return nil, mgerr.NotFound("no_such_message", fmt.Sprintf("message %q not found for %s", msgID, agent), "")
	}

	msg, err := parseMessageFile(srcPath, msgID)
	if err != nil {
		return nil, fmt.Errorf("reading message %q: %w", msgID, err)
	}
	msg.Read = true

	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating archive/: %w", err)
	}

	if err := os.Rename(srcPath, archivePath); err != nil {
		return nil, fmt.Errorf("moving to archive/: %w", err)
	}

	event.Emit(eventsRoot(mailRoot), "mail.archived", map[string]string{
		"msg_id":  msgID,
		"mailbox": agent,
	})
	fromDir := "cur"
	if srcPath == newPath {
		fromDir = "new"
	}
	Audit(mailRoot, "archive", agent, msgID, map[string]string{"from_dir": fromDir})

	return &msg, nil
}

// parseMessageFile reads and parses a Maildir message file. A file that
// exists but lacks the blank-line header/body separator or carries none of
// the known headers (truncated or corrupted in transfer) yields an error
// wrapping ErrMalformed rather than a garbled Message.
func parseMessageFile(path, id string) (Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}

	msg := Message{ID: id}
	content := string(data)

	// Split headers from body at blank line
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) != 2 {
		return Message{}, fmt.Errorf("%w: missing header/body separator", ErrMalformed)
	}
	msg.Body = strings.TrimSpace(parts[1])

	// Parse headers
	sawHeader := false
	for _, line := range strings.Split(parts[0], "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			switch k {
			case "From":
				msg.From = v
				sawHeader = true
			case "Subject":
				msg.Subject = v
				sawHeader = true
			case "Date":
				msg.Date = v
				sawHeader = true
			}
		}
	}
	if !sawHeader {
		return Message{}, fmt.Errorf("%w: no From/Subject/Date headers", ErrMalformed)
	}

	return msg, nil
}

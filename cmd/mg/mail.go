package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

func mailRoot() (string, error) {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mail"), nil
}

var mailCmd = &cobra.Command{
	Use:   "mail",
	Short: "Maildir-style messaging (send, list, read, archive)",
}

// --json contract (frozen + additive-only). Field names are part of the public
// CLI contract and mirror the `mg list --json` precedent (cmd/mg/list.go):
// collections emit NDJSON (one JSON object per line), single items emit one
// JSON object. Fields may be ADDED in later releases but never renamed or
// removed, so scripted consumers can rely on them.
type (
	// mailMsgJSON is one line of `mg mail list AGENT --json` output.
	mailMsgJSON struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Date    string `json:"date"`
		Read    bool   `json:"read"`
	}

	// mailboxJSON is one line of no-arg `mg mail list --json` output. A
	// mailbox present in the stream exists on disk; its absence means the
	// mailbox never existed. Combined with unread this lets a scripted
	// consumer tell existing-but-empty (present, unread 0) from
	// never-existed (absent).
	mailboxJSON struct {
		Mailbox string `json:"mailbox"`
		Unread  int    `json:"unread"`
		Exists  bool   `json:"exists"`
	}

	// mailSendJSON is the single object emitted by `mg mail send --json`.
	// mailbox_created is true when the recipient's mailbox did not exist
	// before this delivery, so a scripted caller can catch a typo'd /
	// unknown recipient (exit still 0 — first delivery is legitimate).
	mailSendJSON struct {
		MsgID          string `json:"msg_id"`
		From           string `json:"from"`
		To             string `json:"to"`
		MailboxCreated bool   `json:"mailbox_created"`
	}

	// mailReadJSON is the single object emitted by `mg mail read --json`.
	mailReadJSON struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Date    string `json:"date"`
		Read    bool   `json:"read"`
		Body    string `json:"body"`
	}

	// mailArchiveJSON is the single object emitted by `mg mail archive --json`.
	mailArchiveJSON struct {
		ID      string `json:"id"`
		Mailbox string `json:"mailbox"`
		From    string `json:"from"`
		Subject string `json:"subject"`
	}
)

// encodeJSON writes v as a single JSON object followed by a newline. Used for
// both single-item output and (called per element) NDJSON collections.
func encodeJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

var (
	mailSendFrom     string
	mailSendSubject  string
	mailSendBody     string
	mailListAll      bool
	mailListArchived bool
	mailReadForce    bool
	mailJSON         bool
)

// canonicalAgent strips the harness prefixes pogo puts on agent names so
// the same polecat is recognized under all its aliases: the mailbox is
// "mg-6ae0", the process-derived POGO_AGENT_NAME may be "6ae0" or
// "cat-mg-6ae0". Crew agents (mayor, architect, ...) pass through unchanged.
func canonicalAgent(name string) string {
	name = strings.TrimPrefix(name, "cat-")
	name = strings.TrimPrefix(name, "mg-")
	return name
}

var mailSendCmd = &cobra.Command{
	Use:   "send AGENT",
	Short: "Send a message to an agent",
	Long: `Send a message to an agent's mailbox.

The recipient's mailbox is created lazily on first delivery, so sending to a
brand-new agent always succeeds (exit 0). When the recipient's mailbox did not
previously exist the message notes "(new mailbox created)" — under --json the
same signal is the boolean "mailbox_created" field — so a typo'd or unknown
recipient is visible rather than silently swallowed.

--from is free-text and intentionally unvalidated: a brand-new agent must be
able to send its first message before it has a mailbox. Run 'mg mail list' with
no arguments to see the existing mailboxes, which is the de-facto list of agent
identities to draw a --from value from.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipient := args[0]

		if mailSendFrom == "" || mailSendSubject == "" || mailSendBody == "" {
			return fmt.Errorf("--from, --subject, and --body are required")
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// Capture existence BEFORE Send creates the mailbox, so we can
		// report first-delivery to a never-before-seen recipient.
		existed := mail.MailboxExists(mr, recipient)

		msgID, err := mail.Send(mr, recipient, mailSendFrom, mailSendSubject, mailSendBody)
		if err != nil {
			return err
		}

		created := !existed

		if mailJSON {
			return encodeJSON(mailSendJSON{
				MsgID:          msgID,
				From:           mailSendFrom,
				To:             recipient,
				MailboxCreated: created,
			})
		}

		// Human output must agree with the json mailbox_created field so
		// the two modes never disagree about first-delivery.
		note := ""
		if created {
			note = "  (new mailbox created)"
		}
		fmt.Printf("Delivered: %s → %s/new/%s%s\n", mailSendFrom, recipient, msgID, note)
		return nil
	},
}

var mailListCmd = &cobra.Command{
	Use:   "list [AGENT]",
	Short: "List mailboxes, or unread messages for an agent",
	Long: `List mail.

With no AGENT, enumerate every mailbox under the mail root with its unread
count — the de-facto list of agent identities, since macguffin has no separate
registry. A mailbox shown with 0 unread exists but is empty; a mailbox that
never existed is simply absent. Under --json each mailbox is one NDJSON object
{mailbox,unread,exists}.

With an AGENT, list that agent's unread messages (or, with --all, read messages
too; with --archived, archived messages). If the agent's mailbox never existed
this is called out distinctly from an existing-but-empty mailbox. Under --json
each message is one NDJSON object {id,from,subject,date,read}; the MSG-ID it
prints is the token 'mg mail read'/'mg mail archive' accept.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// No-arg form: enumerate mailboxes (#53).
		if len(args) == 0 {
			if mailListAll || mailListArchived {
				return fmt.Errorf("--all/--archived require an AGENT; run 'mg mail list AGENT --archived'")
			}
			return runMailboxList(mr)
		}

		agent := args[0]

		if mailListArchived && mailListAll {
			return fmt.Errorf("--archived and --all are mutually exclusive")
		}

		var msgs []mail.Message
		var malformed int
		switch {
		case mailListArchived:
			msgs, malformed, err = mail.ListArchived(mr, agent)
		case mailListAll:
			msgs, malformed, err = mail.ListAll(mr, agent)
		default:
			msgs, malformed, err = mail.List(mr, agent)
		}
		if err != nil {
			return err
		}

		if mailJSON {
			for _, m := range msgs {
				if err := encodeJSON(mailMsgJSON{
					ID:      m.ID,
					From:    m.From,
					Subject: m.Subject,
					Date:    m.Date,
					Read:    m.Read,
				}); err != nil {
					return err
				}
			}
			// malformed count is still surfaced per-file on stderr by the
			// mail package; json stdout stays pure message NDJSON.
			return nil
		}

		if len(msgs) == 0 {
			// Distinguish an existing-but-empty mailbox from one that
			// never existed (#49), keeping exit 0 in both cases.
			switch {
			case mailListArchived:
				fmt.Printf("No archived messages for %s\n", agent)
			case !mail.MailboxExists(mr, agent):
				fmt.Printf("No mailbox for %s yet — no mail has ever been delivered to it\n", agent)
			case mailListAll:
				fmt.Printf("No messages for %s\n", agent)
			default:
				fmt.Printf("No unread messages for %s\n", agent)
			}
		} else {
			for _, m := range msgs {
				status := "●"
				if m.Read {
					status = " "
				}
				fmt.Printf("  %s %s/%s  %-12s  %s\n", status, agent, m.ID, m.From, m.Subject)
			}
		}

		if malformed > 0 {
			fmt.Printf("warning: %d malformed message(s) skipped for %s (see stderr for details)\n", malformed, agent)
		}
		return nil
	},
}

// runMailboxList implements the no-arg `mg mail list` mailbox enumeration (#53).
func runMailboxList(mr string) error {
	boxes, err := mail.ListMailboxes(mr)
	if err != nil {
		return err
	}

	if mailJSON {
		for _, b := range boxes {
			if err := encodeJSON(mailboxJSON{
				Mailbox: b.Name,
				Unread:  b.Unread,
				Exists:  true,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if len(boxes) == 0 {
		fmt.Println("No mailboxes found.")
		return nil
	}

	for _, b := range boxes {
		fmt.Printf("  %-16s  %d unread\n", b.Name, b.Unread)
	}
	return nil
}

var mailReadCmd = &cobra.Command{
	Use:   "read AGENT/MSG-ID",
	Short: "Read a specific message",
	Long: `Read a specific message and mark it read (moves it new/ -> cur/).

The message may be addressed either way:
  mg mail read AGENT/MSG-ID     # single slash-joined argument
  mg mail read AGENT MSG-ID     # two arguments

MSG-ID is exactly the token printed by 'mg mail list AGENT' (the part after
"AGENT/"). Reading another agent's mailbox is refused unless --force, because
it marks the message read and hides it from that agent's unread list.

With --json the message is emitted as a single object {id,from,subject,date,
read,body} instead of the human-formatted headers-and-body.`,
	Args: usageArgs(cobra.RangeArgs(1, 2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, msgID, err := parseAgentMsgID(args)
		if err != nil {
			return err
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// Reading is destructive to the owner's unread state (new/ -> cur/):
		// a cross-box read silently drops the message from the owner's
		// unread list (the mg-6ae0 incident). Refuse unless forced.
		caller := os.Getenv("POGO_AGENT_NAME")
		crossBox := caller != "" && canonicalAgent(caller) != canonicalAgent(agent)
		if crossBox && !mailReadForce {
			mail.Audit(mr, "read-denied", agent, msgID, map[string]string{"reason": "cross-box"})
			return fmt.Errorf("refusing to read %s's mail as agent %q: reading marks the message read and hides it from %s's unread list. Re-run with --force if this cross-box read is intentional", agent, caller, agent)
		}
		if crossBox {
			mail.Audit(mr, "read-forced", agent, msgID, nil)
		}

		msg, err := mail.Read(mr, agent, msgID)
		if err != nil {
			return err
		}

		if mailJSON {
			return encodeJSON(mailReadJSON{
				ID:      msg.ID,
				From:    msg.From,
				Subject: msg.Subject,
				Date:    msg.Date,
				Read:    true,
				Body:    msg.Body,
			})
		}

		fmt.Printf("From: %s\nSubject: %s\nDate: %s\n\n%s\n", msg.From, msg.Subject, msg.Date, msg.Body)
		return nil
	},
}

var mailArchiveCmd = &cobra.Command{
	Use:   "archive AGENT/MSG-ID",
	Short: "Archive a message (move it out of the active mailbox)",
	Long: `Archive a message, moving it out of the active mailbox into archive/.

The message may be addressed either way:
  mg mail archive AGENT/MSG-ID  # single slash-joined argument
  mg mail archive AGENT MSG-ID  # two arguments

MSG-ID is exactly the token printed by 'mg mail list AGENT' (the part after
"AGENT/"). An unread (new/) or read (cur/) message is handled; archiving an
already-archived message is a no-op. Archived mail is inspected with
'mg mail list AGENT --archived'.

With --json the archived message is emitted as a single object
{id,mailbox,from,subject}.`,
	Args: usageArgs(cobra.RangeArgs(1, 2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, msgID, err := parseAgentMsgID(args)
		if err != nil {
			return err
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		msg, err := mail.Archive(mr, agent, msgID)
		if err != nil {
			return err
		}

		if mailJSON {
			return encodeJSON(mailArchiveJSON{
				ID:      msg.ID,
				Mailbox: agent,
				From:    msg.From,
				Subject: msg.Subject,
			})
		}

		fmt.Printf("Archived: %s/%s  (%s: %s)\n", agent, msg.ID, msg.From, msg.Subject)
		return nil
	},
}

// parseAgentMsgID resolves the shared AGENT/MSG-ID | AGENT MSG-ID argument form
// used by mail read and mail archive.
func parseAgentMsgID(args []string) (agent, msgID string, err error) {
	if len(args) == 1 {
		parts := strings.SplitN(args[0], "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("expected AGENT/MSG-ID format, got %q", args[0])
		}
		return parts[0], parts[1], nil
	}
	return args[0], args[1], nil
}

func init() {
	mailSendCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (required)")
	mailSendCmd.Flags().StringVar(&mailSendSubject, "subject", "", "message subject (required)")
	mailSendCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")
	mailSendCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailReadCmd.Flags().BoolVar(&mailReadForce, "force", false, "allow reading another agent's mailbox (marks the message read for its owner)")
	mailReadCmd.Flags().BoolVar(&mailJSON, "json", false, "emit the message as a single JSON object instead of human-formatted output")

	mailArchiveCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailListCmd.Flags().BoolVarP(&mailListAll, "all", "a", false, "include read messages from cur/")
	mailListCmd.Flags().BoolVar(&mailListArchived, "archived", false, "list archived messages instead of the active mailbox")
	mailListCmd.Flags().BoolVar(&mailJSON, "json", false, "emit one JSON object per line (NDJSON) instead of human-formatted output")

	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailListCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailArchiveCmd)
}

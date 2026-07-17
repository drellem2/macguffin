package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/spf13/cobra"
)

func mailRoot() (string, error) {
	root, err := resolveRoot()
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

	// mailSendJSON is the single object emitted by `mg mail send --json` and
	// `mg mail reply --json`. mailbox_created is true when the recipient's
	// mailbox did not exist before this delivery, so a scripted caller can
	// catch a typo'd / unknown recipient (exit still 0 — first delivery is
	// legitimate). in_reply_to is the id this message replies to, "" when it
	// starts a thread; msg_id is the new message's id, unchanged.
	mailSendJSON struct {
		MsgID          string `json:"msg_id"`
		From           string `json:"from"`
		To             string `json:"to"`
		MailboxCreated bool   `json:"mailbox_created"`
		InReplyTo      string `json:"in_reply_to"`
	}

	// mailReadJSON is the single object emitted by `mg mail read --json`.
	// in_reply_to and references carry the correlation headers, so a scripted
	// consumer can reconstruct a thread without re-reading the message file.
	// Both are empty for messages sent before threading existed.
	mailReadJSON struct {
		ID         string   `json:"id"`
		From       string   `json:"from"`
		Subject    string   `json:"subject"`
		Date       string   `json:"date"`
		Read       bool     `json:"read"`
		Body       string   `json:"body"`
		InReplyTo  string   `json:"in_reply_to"`
		References []string `json:"references"`
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
	mailSendFrom      string
	mailSendSubject   string
	mailSendBody      string
	mailSendInReplyTo string
	mailListAll       bool
	mailListArchived  bool
	mailReadForce     bool
	mailReplyForce    bool
	mailMigrateDryRun bool
	mailJSON          bool
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
identities to draw a --from value from. --from and --subject may not contain
newlines or other control characters, which would inject arbitrary headers.

Every delivered message carries a Message-Id equal to its MSG-ID. Pass
--in-reply-to MSG-ID to mark this message as a reply: it writes In-Reply-To and
seeds References. The id is not looked up — this is the explicit, stateless
primitive. To reply to a message in your own mailbox and have the ancestry
filled in for you, use 'mg mail reply' instead.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Canonicalize so a caller addressing the work-item alias "mg-<id>"
		// and one addressing the bare live mailbox "<id>" land in the SAME
		// mailbox, instead of the alias minting a stray box nobody reads.
		recipient := canonicalAgent(args[0])

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

		// send takes an id, not a message: it cannot read the parent's own
		// References (the parent may live in another agent's mailbox, or
		// nowhere at all), so the chain it seeds is just the parent. 'mg mail
		// reply' is the form that carries a full ancestry forward.
		opts := mail.SendOpts{InReplyTo: mailSendInReplyTo}
		if mailSendInReplyTo != "" {
			opts.References = []string{mailSendInReplyTo}
		}

		msgID, err := mail.SendWithOpts(mr, recipient, mailSendFrom, mailSendSubject, mailSendBody, opts)
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
				InReplyTo:      mailSendInReplyTo,
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

		// Canonicalize so listing the alias "mg-<id>" reports the same
		// mailbox a "<id>" send lands in — otherwise a watcher polling the
		// alias sees "No mailbox yet" forever while mail piles up in "<id>".
		agent := canonicalAgent(args[0])

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
"AGENT/"); an id containing a path separator or ".." is refused. Reading
another agent's mailbox is refused unless --force, because it marks the message
read and hides it from that agent's unread list.

The header block carries the body's length beside From/Subject/Date:

  Body: 47 lines / 3165 bytes

The counts cover the body alone, not the headers above it. They sit at the top
so that a reader piping through 'head -N' — as agents do, to protect a finite
context window — can see that the body runs past their view. A body cut at N is
then a visible drop rather than a silent one.

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

		if err := guardCrossBoxRead(mr, agent, msgID, mailReadForce); err != nil {
			return err
		}

		msg, err := mail.Read(mr, agent, msgID)
		if err != nil {
			return err
		}

		if mailJSON {
			return encodeJSON(mailReadJSON{
				ID:         msg.ID,
				From:       msg.From,
				Subject:    msg.Subject,
				Date:       msg.Date,
				Read:       true,
				Body:       msg.Body,
				InReplyTo:  msg.InReplyTo,
				References: jsonRefs(msg.References),
			})
		}

		lines, bytes := bodyMetrics(msg.Body)
		fmt.Printf("From: %s\nSubject: %s\nDate: %s\nBody: %d lines / %d bytes\n\n%s\n",
			msg.From, msg.Subject, msg.Date, lines, bytes, msg.Body)
		return nil
	},
}

// bodyMetrics measures the body exactly as 'mail read' prints it, so a reader
// who pipes the output through 'head -N' can compare N against the line count
// and see that their view stopped short. It belongs in the header block, above
// the body: a footer is cut by the same 'head' it would warn about, so it
// disappears precisely when it is needed (mg-8a44).
//
// The counts describe the body alone, not the file on disk: the header lines
// and the blank separator are what the reader can already see, and a byte count
// that silently included them would not match the body it labels.
//
// Body is stored trimmed, so it never ends in a newline and Printf supplies the
// final one; every line is therefore newline-terminated on the wire and the
// line count is separator count + 1. An empty body prints no line at all.
func bodyMetrics(body string) (lines, bytes int) {
	if body == "" {
		return 0, 0
	}
	return strings.Count(body, "\n") + 1, len(body)
}

// jsonRefs normalizes a nil References slice to an empty one so the --json
// contract emits [] rather than null for an unthreaded message.
func jsonRefs(refs []string) []string {
	if refs == nil {
		return []string{}
	}
	return refs
}

// guardCrossBoxRead refuses an operation that marks another agent's message
// read unless forced. Marking read is destructive to the owner's unread state
// (new/ -> cur/): a cross-box read silently drops the message from the owner's
// unread list (the mg-6ae0 incident). Both 'mail read' and 'mail reply' perform
// that transition, so both go through here.
func guardCrossBoxRead(mr, agent, msgID string, force bool) error {
	caller := os.Getenv("POGO_AGENT_NAME")
	crossBox := caller != "" && canonicalAgent(caller) != canonicalAgent(agent)
	if !crossBox {
		return nil
	}
	if !force {
		mail.Audit(mr, "read-denied", agent, msgID, map[string]string{"reason": "cross-box"})
		return fmt.Errorf("refusing to read %s's mail as agent %q: reading marks the message read and hides it from %s's unread list. Re-run with --force if this cross-box read is intentional", agent, caller, agent)
	}
	mail.Audit(mr, "read-forced", agent, msgID, nil)
	return nil
}

var mailArchiveCmd = &cobra.Command{
	Use:   "archive AGENT/MSG-ID",
	Short: "Archive a message (move it out of the active mailbox)",
	Long: `Archive a message, moving it out of the active mailbox into archive/.

The message may be addressed either way:
  mg mail archive AGENT/MSG-ID  # single slash-joined argument
  mg mail archive AGENT MSG-ID  # two arguments

MSG-ID is exactly the token printed by 'mg mail list AGENT' (the part after
"AGENT/"); an id containing a path separator or ".." is refused. An unread
(new/) or read (cur/) message is handled; archiving an already-archived message
is a no-op. Archived mail is inspected with 'mg mail list AGENT --archived'.

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

var mailReplyCmd = &cobra.Command{
	Use:   "reply AGENT/MSG-ID",
	Short: "Reply to a message, threading it to the original",
	Long: `Reply to a message in AGENT's mailbox.

The message may be addressed either way:
  mg mail reply AGENT/MSG-ID    # single slash-joined argument
  mg mail reply AGENT MSG-ID    # two arguments

reply is a wrapper over 'mg mail send --in-reply-to'. It reads the original to
fill in what you would otherwise retype: the recipient (the original's From),
the subject ("Re: " + the original's, unless --subject overrides), In-Reply-To,
and a References chain extending the original's. Nothing is inferred from
history — the message you name is the only input.

--from defaults to AGENT, the mailbox you are replying from. Only --body is
required.

Like 'mail read', reply marks the original read (new/ -> cur/), so replying out
of another agent's mailbox needs --force. It does NOT archive the original;
archive it yourself with 'mg mail archive' when you are done with it.`,
	Args: usageArgs(cobra.RangeArgs(1, 2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, msgID, err := parseAgentMsgID(args)
		if err != nil {
			return err
		}
		if mailSendBody == "" {
			return mgerr.Usage("missing_required", "--body is required", "")
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// Guard before Peek: a refused cross-box reply must not read the
		// message at all, and the denial is audited either way.
		if err := guardCrossBoxRead(mr, agent, msgID, mailReplyForce); err != nil {
			return err
		}

		// Peek, not Read: inspect the original without marking it read, so a
		// reply that fails to send leaves the unread state untouched.
		orig, err := mail.Peek(mr, agent, msgID)
		if err != nil {
			return err
		}
		if orig.From == "" {
			return mgerr.Usage("invalid_value",
				fmt.Sprintf("cannot reply to %s/%s: it has no From header, so there is no one to reply to", agent, msgID),
				"send to an explicit recipient with 'mg mail send RECIPIENT --in-reply-to "+msgID+"'")
		}

		from := mailSendFrom
		if from == "" {
			from = agent
		}
		subject := mailSendSubject
		if subject == "" {
			subject = replySubject(orig.Subject)
		}

		// Thread on the original's file name, not its Message-Id header:
		// the two agree for messages this version wrote, and the file name is
		// the only id a message delivered before Message-Id existed has.
		opts := mail.SendOpts{
			InReplyTo:  orig.ID,
			References: append(append([]string{}, orig.References...), orig.ID),
		}

		// The reply recipient is the original's From, which may itself be an
		// "mg-<id>" alias; canonicalize it so the reply lands in the live box.
		recipient := canonicalAgent(orig.From)
		existed := mail.MailboxExists(mr, recipient)

		newID, err := mail.SendWithOpts(mr, recipient, from, subject, mailSendBody, opts)
		if err != nil {
			return err
		}

		// The reply is delivered; now mark the original read. Ordering matters:
		// a send that fails above must not consume the original's unread state.
		if _, err := mail.Read(mr, agent, msgID); err != nil {
			return fmt.Errorf("reply delivered as %s/new/%s, but marking %s/%s read failed: %w", recipient, newID, agent, msgID, err)
		}

		created := !existed

		if mailJSON {
			return encodeJSON(mailSendJSON{
				MsgID:          newID,
				From:           from,
				To:             recipient,
				MailboxCreated: created,
				InReplyTo:      orig.ID,
			})
		}

		note := ""
		if created {
			note = "  (new mailbox created)"
		}
		fmt.Printf("Replied: %s → %s/new/%s  (in-reply-to %s/%s)%s\n", from, recipient, newID, agent, orig.ID, note)
		return nil
	},
}

var mailMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Merge stray prefixed mailboxes (mg-<id>) into their canonical (<id>) mailbox",
	Long: `Merge stray prefixed mailboxes into their canonical mailbox.

'mg mail send'/'list' now canonicalize the recipient, so an alias like
"mg-<id>" and the bare live mailbox "<id>" resolve to the same box. Mailboxes
delivered under a prefixed alias BEFORE that fix are left stranded: the alias
name is no longer addressable, so their delivered mail would be unreachable.

This one-shot, idempotent command finds every mailbox whose name carries a
harness prefix ("mg-", "cat-") and moves its messages — unread, read and
archived — into the canonical bare-id mailbox, preserving read state, then
removes the emptied stray directory. Crew mailboxes (mayor, architect, ...)
have no prefix and are left untouched.

Run with --dry-run first to see what would move without touching the store.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		mr, err := mailRoot()
		if err != nil {
			return err
		}

		boxes, err := mail.ListMailboxes(mr)
		if err != nil {
			return err
		}

		migrated := 0
		for _, b := range boxes {
			canon := canonicalAgent(b.Name)
			// Skip boxes that are already canonical, and the degenerate case
			// where stripping the prefix leaves nothing addressable.
			if canon == b.Name || canon == "" {
				continue
			}

			if mailMigrateDryRun {
				fmt.Printf("would merge %s → %s (%d unread)\n", b.Name, canon, b.Unread)
				migrated++
				continue
			}

			res, err := mail.MergeMailbox(mr, b.Name, canon)
			if err != nil {
				return fmt.Errorf("merging %s into %s: %w", b.Name, canon, err)
			}
			fmt.Printf("merged %s → %s (%d message(s) moved)\n", res.From, res.To, res.Moved)
			migrated++
		}

		if migrated == 0 {
			fmt.Println("No stray mailboxes to migrate.")
		}
		return nil
	},
}

// replySubject prefixes "Re: " unless the subject already carries one, so a
// long back-and-forth does not accumulate "Re: Re: Re: ". The check is
// case-insensitive because a human-typed --subject may say "RE:".
func replySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

// parseAgentMsgID resolves the shared AGENT/MSG-ID | AGENT MSG-ID argument form
// used by mail read, mail archive and mail reply. The AGENT is canonicalized so
// the alias "mg-<id>" resolves to the same mailbox as the bare "<id>".
func parseAgentMsgID(args []string) (agent, msgID string, err error) {
	if len(args) == 1 {
		parts := strings.SplitN(args[0], "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("expected AGENT/MSG-ID format, got %q", args[0])
		}
		return canonicalAgent(parts[0]), parts[1], nil
	}
	return canonicalAgent(args[0]), args[1], nil
}

func init() {
	mailSendCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (required)")
	mailSendCmd.Flags().StringVar(&mailSendSubject, "subject", "", "message subject (required)")
	mailSendCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")
	mailSendCmd.Flags().StringVar(&mailSendInReplyTo, "in-reply-to", "", "MSG-ID this message replies to (sets In-Reply-To and seeds References)")
	mailSendCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailReplyCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (defaults to AGENT, the mailbox replied from)")
	mailReplyCmd.Flags().StringVar(&mailSendSubject, "subject", "", `subject (defaults to "Re: " + the original's)`)
	mailReplyCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")
	mailReplyCmd.Flags().BoolVar(&mailReplyForce, "force", false, "allow replying out of another agent's mailbox (marks the original read for its owner)")
	mailReplyCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailReadCmd.Flags().BoolVar(&mailReadForce, "force", false, "allow reading another agent's mailbox (marks the message read for its owner)")
	mailReadCmd.Flags().BoolVar(&mailJSON, "json", false, "emit the message as a single JSON object instead of human-formatted output")

	mailArchiveCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailListCmd.Flags().BoolVarP(&mailListAll, "all", "a", false, "include read messages from cur/")
	mailListCmd.Flags().BoolVar(&mailListArchived, "archived", false, "list archived messages instead of the active mailbox")
	mailListCmd.Flags().BoolVar(&mailJSON, "json", false, "emit one JSON object per line (NDJSON) instead of human-formatted output")

	mailMigrateCmd.Flags().BoolVar(&mailMigrateDryRun, "dry-run", false, "report which stray mailboxes would be merged without moving any mail")

	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailReplyCmd)
	mailCmd.AddCommand(mailListCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailArchiveCmd)
	mailCmd.AddCommand(mailMigrateCmd)
}

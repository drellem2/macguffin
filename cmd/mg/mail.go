package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
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
	//
	// registration is the box's standing (mailregistry.go): "registered" —
	// somebody performed the deliberate act and a record says who and when;
	// "work-item" — no record, but a work item is named that, so the name is
	// derivably legitimate; "unregistered" — neither, so the box exists only
	// because mail was delivered to it. exists and registration are
	// INDEPENDENT: a box can exist unregistered (the `daniel` case, in daily
	// use and never set up) and a box can be absent yet vouched for by a work
	// item, which is exactly the address a send accepts before any mail
	// arrives.
	mailboxJSON struct {
		Mailbox      string `json:"mailbox"`
		Unread       int    `json:"unread"`
		Exists       bool   `json:"exists"`
		Registration string `json:"registration"`
	}

	// mailFilterJSON is the trailing summary object emitted by
	// `mg mail list AGENT --json` when — and only when — a sender predicate
	// (--from / --exclude-from) is active. It is a superset of mailboxJSON, so
	// it also answers the existing-vs-never-existed question, and it carries
	// NO "id" field, which is what lets the documented consumer guard
	//
	//   jq 'select(.id and .from != "scheduler")'
	//
	// skip it exactly as it already skips the empty-mailbox sentinel.
	//
	// It is emitted even when messages were listed, because the scripted
	// reader is the one this has to be safest for: the coordinator's inbox hit
	// 1,582 unread and a bulk archive of 1,451 noise rows came within a
	// re-listing of destroying two real messages. A filtered stream that
	// carried no count of what it removed would let that sweep run against a
	// silently narrowed view. listed + suppressed is the size of the listing
	// the filter was applied to.
	mailFilterJSON struct {
		Mailbox      string   `json:"mailbox"`
		Unread       int      `json:"unread"`
		Exists       bool     `json:"exists"`
		Registration string   `json:"registration"`
		Listed       int      `json:"listed"`
		Suppressed   int      `json:"suppressed"`
		From         []string `json:"from"`
		ExcludeFrom  []string `json:"exclude_from"`
	}

	// mailSendJSON is the single object emitted by `mg mail send --json` and
	// `mg mail reply --json`. mailbox_created is true when the recipient's
	// mailbox did not exist before this delivery, so a scripted caller can
	// catch a typo'd / unknown recipient (exit still 0 — first delivery is
	// legitimate). in_reply_to is the id this message replies to, "" when it
	// starts a thread; msg_id is the new message's id, unchanged.
	//
	// subject is the header as actually written, and subject_derived says where
	// it came from: true when 'mail send' took it from the body's first line
	// because --subject was omitted (mg-158e), false when the caller supplied
	// it — and false on 'mail reply', whose "Re: " default comes from the
	// original message, not from any body. The pair is the scripted half of the
	// echo the human path prints: a derivation nobody can see is the defect the
	// existing title derivation still carries, and shipping a second silent one
	// was ruled out when this one was adopted.
	mailSendJSON struct {
		MsgID          string `json:"msg_id"`
		From           string `json:"from"`
		To             string `json:"to"`
		MailboxCreated bool   `json:"mailbox_created"`
		InReplyTo      string `json:"in_reply_to"`
		Subject        string `json:"subject"`
		SubjectDerived bool   `json:"subject_derived"`
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

	// mailRegisterJSON is the single object emitted by
	// `mg mail register --json`.
	//
	// created reports whether the MAILDIR was made by this call; registered
	// reports whether the RECORD was. They are not the same question and used
	// to be conflated: a box that already existed returned created=false and
	// changed nothing, which reported "already registered" for 1361 boxes
	// nobody had ever registered. adopted is the case that separates them —
	// the box was already there, in use, and this call is the registration it
	// never had. prior_messages is how much mail it inherited, which the record
	// notes but does not vouch for.
	//
	// registered_at / registered_by / via describe the record that is now on
	// disk, whether this call wrote it or an earlier one did, so a caller
	// re-running register is told who actually holds the registration rather
	// than being handed its own name.
	mailRegisterJSON struct {
		Mailbox       string `json:"mailbox"`
		Created       bool   `json:"created"`
		Registered    bool   `json:"registered"`
		Adopted       bool   `json:"adopted"`
		PriorMessages int    `json:"prior_messages"`
		RegisteredAt  string `json:"registered_at,omitempty"`
		RegisteredBy  string `json:"registered_by,omitempty"`
		Via           string `json:"via,omitempty"`
	}
)

// encodeJSON writes v as a single JSON object followed by a newline. Used for
// both single-item output and (called per element) NDJSON collections.
func encodeJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

var (
	mailSendFrom        string
	mailSendSubject     string
	mailSendBody        string
	mailSendBodyFile    string
	mailSendInReplyTo   string
	mailSendCreate      bool
	mailListAll         bool
	mailListArchived    bool
	mailListFrom        []string
	mailListExcludeFrom []string
	mailReadForce       bool
	mailReplyForce      bool
	mailMigrateDryRun   bool
	mailJSON            bool
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

Reach for --body-file first. It reads the body verbatim ("-" for stdin), with
no shell in the path at all. The canonical form is a QUOTED heredoc, and it
answers the SUBJECT too — omit --subject and the first line of the body becomes
it, the RFC822 / git-commit convention:

  mg mail send mayor --from=me --body-file - <<'EOF'
  Daniel's call on Monday's park

  body text with backticks and $VARS and $(cmd), all literal
  EOF

The quotes around 'EOF' are the entire property. <<'EOF' passes the bytes
through untouched; an unquoted <<EOF expands backticks, $VAR and $(cmd)
exactly as --body="..." does, silently reintroducing the bug. A file works the
same way: --body-file ./msg.md.

--body is the inline-only shortcut, and stays correct for the many bodies that
carry no shell metacharacters. When a body does carry them, the shell expands
them before mg ever runs, so those terms are silently gone from the delivered
message and mg still reports Delivered — mg receives the mangled string and
cannot tell it from one you typed.

The two body flags are mutually exclusive and one is required. A --body-file
that cannot be read is an error, never an empty body.

SUBJECT. --subject is optional. Omitted, the subject is the body's first line,
which puts it inside the heredoc where the bytes are already safe — so the
short spelling is now also the correct one. That matters because a subject can
only be answered inline, and inline is where the shell eats things:
--subject="Daniel's call on Monday's park" has an EVEN number of apostrophes,
so the shell delivers "Daniels park" and mg reports Delivered on it. There is
no quoting trick that fixes this and no check mg can make afterwards; the bytes
are gone before mg starts.

A derived subject is ECHOED BACK on send ("Subject: ..."), and marked in --json
by subject_derived, so nothing is taken from your body without being shown. The
first line is COPIED, not consumed: the delivered body still contains it. If
the first line is blank or carries control characters, the send is REFUSED and
names --subject rather than writing a malformed header. Passing --subject is
unchanged, including its refusal of an empty value.

RECIPIENTS. A recipient mg has never seen is REFUSED (exit 3), and the refusal
names the near neighbours it might have meant ("did you mean v9ecf?"). mg has no
agent registry, so a recipient counts as known when either of two things on disk
says so:

  - a mailbox of that name already exists; or
  - a work item of that name exists — polecat mailboxes are named for the work
    item their agent is running, so a brand-new agent is addressable before its
    first mail arrives.

Neither holds for a typo, which is the whole point. Before this, every send
succeeded: a mistyped name minted a dead drop and reported "Delivered", and the
"(new mailbox created)" note could not separate that from the first legitimate
mail to a new agent — it is equally true of both.

Pass --create when the recipient really is new and neither test can see it yet;
it registers the mailbox and delivers. 'mg mail register NAME' does the same
registration without sending. Both leave a durable record of who registered the
name and when: --create is an ASSERTION that this recipient is new, and without
a record that assertion evaporates the moment the box exists — the escape hatch
would quietly become the way every phantom box gets minted from here on. The
record says "via":"send --create", so a box established that way stays findable
as one. See 'mg mail list --help' for what the record is then used to report. The mailbox is still created lazily, and a first
delivery still notes "(new mailbox created)" — under --json the same signal is
the boolean "mailbox_created" field.

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

		body, _, err := bodyFromFlags(cmd, mailSendBody, mailSendBodyFile)
		if err != nil {
			return err
		}

		// An empty body is refused whichever flag supplied it, so an empty
		// --body-file cannot deliver nothing and report Delivered.
		if mailSendFrom == "" || body == "" {
			return fmt.Errorf("--from and a body (--body or --body-file) are required")
		}

		// --subject is OPTIONAL: omitted, the subject is taken from the body's
		// first line (deriveSubject). Given-but-empty keeps the old refusal —
		// the ruling was to make OMISSION safe, not to make the explicit form
		// quieter, and "--subject=" is a caller stating a subject they did not
		// write rather than one letting the body answer.
		subject := mailSendSubject
		subjectDerived := false
		if !cmd.Flags().Changed("subject") {
			subject, err = deriveSubject(body)
			if err != nil {
				return err
			}
			subjectDerived = true
		} else if subject == "" {
			return mgerr.Usage("missing_required",
				"--subject was given but empty",
				"omit --subject entirely to take the subject from the body's first line, or pass a non-empty value")
		}

		root, err := resolveRoot()
		if err != nil {
			return err
		}
		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// Capture existence BEFORE Send creates the mailbox, so we can
		// report first-delivery to a never-before-seen recipient.
		existed := mail.MailboxExists(mr, recipient)

		// Refuse a recipient nothing on disk knows about, unless the caller
		// says explicitly that it is new. Checked BEFORE SendWithOpts, so a
		// refused address leaves no mailbox, no tmp file and no message.
		if !mailSendCreate && !knownRecipient(mr, root, recipient) {
			return unknownRecipientError(mr, root, recipient)
		}

		// send takes an id, not a message: it cannot read the parent's own
		// References (the parent may live in another agent's mailbox, or
		// nowhere at all), so the chain it seeds is just the parent. 'mg mail
		// reply' is the form that carries a full ancestry forward.
		opts := mail.SendOpts{InReplyTo: mailSendInReplyTo}
		if mailSendInReplyTo != "" {
			opts.References = []string{mailSendInReplyTo}
		}

		msgID, err := mail.SendWithOpts(mr, recipient, mailSendFrom, subject, body, opts)
		if err != nil {
			return err
		}

		// --create is an ASSERTION that this recipient is new, so it leaves a
		// record saying who asserted it and when. Without one the assertion
		// evaporates the moment the box exists, and the escape hatch quietly
		// becomes the way every phantom box gets minted from here on.
		//
		// AFTER delivery, not before, and that ordering is load-bearing. Send
		// validates its headers before it touches the filesystem, so a rejected
		// --subject leaves no message and no lazily created mailbox; writing the
		// record first put a mailbox on disk for a send that was then refused —
		// the remedy minting the phantom box it exists to prevent. Registration
		// is bookkeeping about a delivery that happened, so it belongs after the
		// delivery happens.
		if err := registerViaCreate(mr, recipient, existed, "send --create"); err != nil {
			return deliveredButUnrecorded(recipient, msgID, err)
		}

		created := !existed

		if mailJSON {
			return encodeJSON(mailSendJSON{
				MsgID:          msgID,
				From:           mailSendFrom,
				To:             recipient,
				MailboxCreated: created,
				InReplyTo:      mailSendInReplyTo,
				Subject:        subject,
				SubjectDerived: subjectDerived,
			})
		}

		// Echo a DERIVED subject back. The caller never named this field, so
		// nothing else confirms what was taken from their body; mg's older
		// first-line derivation (a work item's title) prints nothing, and that
		// silence is precisely what let it rename items for four days without
		// anyone seeing it. One line, only when derived — a subject the caller
		// typed needs no read-back.
		if subjectDerived {
			fmt.Printf("Subject: %s  (derived from the body's first line — pass --subject to set it explicitly)\n", subject)
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
too; with --archived, archived messages). Under --json each message is one
NDJSON object {id,from,subject,date,read}; the MSG-ID it prints is the token
'mg mail read'/'mg mail archive' accept.

SENDER PREDICATE. --from=NAME lists only mail from those senders;
--exclude-from=NAME hides them. Both are repeatable and comma-separated:

  mg mail list architect --exclude-from=scheduler,stall-watch
  mg mail list mayor --from=pm-pogo

Both match the From FIELD, exactly — case-insensitively, and with the same
mg-/cat- prefix stripping every mailbox argument gets, so --from=mg-5168 matches
a From of "5168". Neither is ever a substring match: --exclude-from=scheduler
does NOT hide a message from "scheduler-v2", and does not hide a message from a
real agent whose SUBJECT happens to say "scheduler".

That last property is the reason these flags exist rather than a documentation
note. The escape agents reached for — 'mg mail list X | grep -v scheduler' — is
RETRACTED, and these flags are its replacement rather than its shorthand. It
matches the rendered LINE and therefore discards any real message whose subject
mentions the scheduler, exactly the correspondence about the noise it was
introduced to escape. The bug is not a bad pattern but a category error: a
text filter over a field-structured listing cannot see which column it landed
in, so no better pattern fixes it. It also self-validates, staying correct until
the first message that mentions the term, which is correlated with the topic and
so arrives when the traffic matters most.

A sender name given to BOTH flags is refused: nothing could match, so the empty
listing would say nothing about the mailbox.

WHAT THE FILTER HID IS ALWAYS REPORTED. A filter is another bounded read, and a
bounded read that says nothing manufactures absence — the defect these flags
address. So whenever a predicate is active the listing is preceded by

  sender filter: --exclude-from=scheduler — 1 of 265 shown, 264 hidden

and a predicate that removes EVERYTHING says outright that the mailbox is not
empty, instead of borrowing the wording of a quiet inbox. Under --json the same
figures arrive as one trailing object {mailbox,unread,exists,listed,suppressed,
from,exclude_from}, emitted whether or not any message matched; like the
empty-mailbox sentinel it carries no "id", so a consumer selecting on .id skips
it. "unread" is always the mailbox's true unread count, never the filtered one.

A mailbox that NEVER EXISTED is reported as "No such mailbox", not as an empty
one, and near neighbours are suggested ("did you mean bf3ae?"). The two used to
differ in prose alone and nothing downstream could use the difference: a human
diagnosing a silent loop read "No mailbox for X yet" as "X has no new mail",
which is how a stalled review stayed invisible for forty minutes.

So the distinction survives into tooling: when there are NO messages to list,
--json emits exactly one object {mailbox,unread,exists} in place of the empty
stream it used to emit, which was byte-identical for a real mailbox and a
fictional one. It is the same shape the no-arg mailbox enumeration emits, and it
carries no "id" field, so a consumer reading messages can tell the two apart.
Exit status stays 0 in both cases: a mailbox that does not exist yet is the
normal state of an agent nobody has mailed, and a poller asking after one is
asking a fair question, not making an error.

STANDING. The no-arg enumeration also says which boxes nobody established. A
mailbox is created by delivering to it, so existence was never evidence that
anyone MEANT the name: the send-time refusal fires once, and a name talked past
it with --create was a good address forever after. Every box now reports one of
three answers, as "registration" under --json:

  registered    a registration record exists — somebody performed the
                deliberate act, and the record names who and when
  work-item     no record, but a work item is called that, so the name is
                derivably legitimate. Most of the store; NOT flagged
  unregistered  neither: the box exists only because mail was delivered to it

Only the last is marked, and the footer counts them and says how many are
holding mail right now — a marker on half the rows discriminates nothing
without that. Nothing is refused on the basis of standing; run
'mg mail register NAME' to adopt a box already in use.

The per-box form stays quiet about standing on the human path, because agents
poll their own box every few minutes and a line there would be a nag on the
healthy path. Under --json the answer is on the per-box status object when
there is nothing to list, and on every mailbox in the no-arg enumeration — so
'mg mail list --json' is where to ask about a box that HAS mail, since that
stream is the messages themselves.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		mr, err := mailRoot()
		if err != nil {
			return err
		}

		filter, err := newSenderFilter(mailListFrom, mailListExcludeFrom,
			cmd.Flags().Changed("from"), cmd.Flags().Changed("exclude-from"))
		if err != nil {
			return err
		}

		// No-arg form: enumerate mailboxes (#53).
		if len(args) == 0 {
			if mailListAll || mailListArchived {
				return fmt.Errorf("--all/--archived require an AGENT; run 'mg mail list AGENT --archived'")
			}
			// The sender predicate filters MESSAGES; the no-arg form lists
			// mailboxes, which have no sender. Refusing says so, where
			// silently ignoring the flag would return a full enumeration that
			// looks filtered.
			if filter.active() {
				return mgerr.Usage("invalid_value",
					"--from/--exclude-from require an AGENT: they filter messages by sender, and the no-arg form lists mailboxes",
					"run 'mg mail list AGENT --exclude-from=NAME'")
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

		exists := mail.MailboxExists(mr, agent)

		// total is the size of the listing the predicate was applied to;
		// shown is what survived it. Both are needed to say what was hidden,
		// and saying that is what keeps a filtered listing from reading like
		// an empty mailbox.
		total := len(msgs)
		shown := filter.apply(msgs)

		if mailJSON {
			for _, m := range shown {
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
			// A trailing summary whenever a predicate is active, so no
			// scripted consumer can be handed a narrowed stream without the
			// count of what was removed from it. It is a superset of the
			// empty-mailbox sentinel, so it replaces rather than joins it.
			if filter.active() {
				standing, err := listStanding(mr, agent)
				if err != nil {
					return err
				}
				return encodeJSON(mailFilterJSON{
					Mailbox:      agent,
					Unread:       unreadCount(mr, agent),
					Exists:       exists,
					Registration: standing,
					Listed:       len(shown),
					Suppressed:   total - len(shown),
					From:         jsonNames(mailListFrom),
					ExcludeFrom:  jsonNames(mailListExcludeFrom),
				})
			}
			// An empty stream used to be the ONLY output for both a mailbox
			// with nothing in it and a mailbox that never existed, so no
			// scripted consumer could tell a quiet inbox from a misdelivery.
			// Emit the box's own state instead of nothing.
			if len(shown) == 0 {
				standing, err := listStanding(mr, agent)
				if err != nil {
					return err
				}
				return encodeJSON(mailboxJSON{
					Mailbox:      agent,
					Unread:       unreadCount(mr, agent),
					Exists:       exists,
					Registration: standing,
				})
			}
			// malformed count is still surfaced per-file on stderr by the
			// mail package; json stdout stays pure message NDJSON.
			return nil
		}

		// Above the rows, not below them: see senderFilter.report.
		if filter.active() {
			fmt.Print(filter.report(len(shown), total))
		}

		if len(shown) == 0 {
			// Distinguish an existing-but-empty mailbox from one that
			// never existed (#49), keeping exit 0 in both cases.
			switch {
			case !exists:
				root, err := resolveRoot()
				if err != nil {
					return err
				}
				fmt.Printf("No such mailbox: %s — it has never existed, so no mail has ever been delivered to it\n%s",
					agent, suggestionLine(mr, root, agent))
			case total > 0:
				// The filter emptied a listing that was NOT empty. Saying
				// "No unread messages (mailbox exists)" here would be the
				// defect this flag exists to remove, reproduced by its own
				// remedy: a bounded read reporting absence it manufactured.
				fmt.Printf("No %s for %s match the sender filter — the mailbox is NOT empty: all %d %s were hidden by %s\n",
					listingNoun(), agent, total, listingNoun(), filter.desc)
			case mailListArchived:
				fmt.Printf("No archived messages for %s (mailbox exists)\n", agent)
			case mailListAll:
				fmt.Printf("No messages for %s (mailbox exists)\n", agent)
			default:
				fmt.Printf("No unread messages for %s (mailbox exists)\n", agent)
			}
		} else {
			for _, m := range shown {
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

// listStanding resolves one mailbox's standing for the --json status objects.
// It resolves the workspace root itself so the caller is not forced to do so on
// the paths that never need it.
func listStanding(mailRootDir, agent string) (string, error) {
	root, err := resolveRoot()
	if err != nil {
		return "", err
	}
	return mailboxStanding(mailRootDir, root, agent), nil
}

// unreadCount reports how many unread messages sit in an agent's mailbox,
// reporting 0 for a mailbox that does not exist. It is the "unread" half of the
// --json status object, and it is computed independently of the listing mode so
// that `mg mail list X --archived --json` still answers "how much unread mail is
// waiting" rather than reporting the archive's emptiness as the inbox's.
func unreadCount(mailRootDir, agent string) int {
	msgs, _, err := mail.List(mailRootDir, agent)
	if err != nil {
		return 0
	}
	return len(msgs)
}

// runMailboxList implements the no-arg `mg mail list` mailbox enumeration (#53).
//
// This is the diagnostic view — the one an operator opens to ask what the mail
// store actually contains — so it is where a box's STANDING belongs. A box
// nothing vouches for is marked, and only that case is: the store is mostly
// polecat boxes named for their work item, and marking those too would bury the
// handful that matter under a thousand rows of the ordinary, which is exactly
// how "(new mailbox created)" stopped being read.
//
// The per-box view (`mg mail list AGENT`) stays silent about standing on
// purpose. Polecats poll it every ten minutes and their own boxes are
// work-item boxes; a line there would be a nag on the healthy path, and the
// signal-to-noise the sender filter (mg-5168) bought back would go straight out
// again. The answer is still available per box under --json.
func runMailboxList(mr string) error {
	boxes, err := mail.ListMailboxes(mr)
	if err != nil {
		return err
	}
	root, err := resolveRoot()
	if err != nil {
		return err
	}
	// One walk of the work store for the whole enumeration; see
	// mailboxStandingFunc.
	standingOf := mailboxStandingFunc(mr, root)

	if mailJSON {
		for _, b := range boxes {
			if err := encodeJSON(mailboxJSON{
				Mailbox:      b.Name,
				Unread:       b.Unread,
				Exists:       true,
				Registration: standingOf(b.Name),
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

	unregistered, inUse, strays := 0, 0, 0
	for _, b := range boxes {
		mark := ""
		if standingOf(b.Name) == standingUnregistered {
			unregistered++
			if b.Unread > 0 {
				inUse++
			}
			// A box whose own name canonicalizes to something else is a
			// STRAY: 'mg mail register' refuses it, because registering it
			// would register the canonical twin and mint an empty box beside
			// it. Counted here so the footer does not send a reader at a
			// guaranteed refusal for 135 of the rows it just marked.
			if b.Name != canonicalAgent(b.Name) {
				strays++
			}
			mark = "  UNREGISTERED"
		}
		fmt.Printf("  %-16s  %d unread%s\n", b.Name, b.Unread, mark)
	}

	// The footer is the part that travels. A marked row is only seen by
	// someone already reading that row; the count is what tells a reader
	// scrolling past that the store has boxes nobody established, and names
	// the one command that closes the gap.
	//
	// The in-use figure is what keeps the mark off the wallpaper. Measured on
	// the live store, 666 of 1361 boxes are unregistered — half the rows — and
	// a marker on half the rows discriminates nothing, which is precisely how
	// "(new mailbox created)" stopped being read. Most of that half is dead:
	// retired agents and the stray prefixed boxes 'mg mail migrate' exists to
	// merge. The ones that MATTER are the boxes taking real mail right now with
	// nothing vouching for them, and that is a number a reader can act on.
	if unregistered > 0 {
		verb := "are"
		if unregistered == 1 {
			verb = "is"
		}
		fmt.Printf("\n%d of %d mailboxes %s UNREGISTERED: the box exists only because mail was delivered to it, and no work item is named for it either.\n",
			unregistered, len(boxes), verb)
		if inUse > 0 {
			fmt.Printf("%d of those %s holding mail: in use right now, with nothing recording who the name belongs to.\n",
				inUse, pluralAre(inUse))
		}
		fmt.Printf("Nothing is refused — mail to them is delivered as always. Run 'mg mail register NAME' to adopt one, which records who vouches for the name.\n")
		if strays > 0 {
			fmt.Printf("%d of them carry a harness prefix (mg-/cat-) and are STRAYS, not names to adopt: their mail belongs in the canonical box. Run 'mg mail migrate --dry-run' for those — 'mg mail register' refuses them.\n", strays)
		}
	}
	return nil
}

// pluralAre renders "is"/"are" for a count.
func pluralAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
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
//
// "Another agent's" is decided by callerOwnsMailbox, not by name equality. A
// mailbox has no registration — it is created by whoever first delivers to it —
// so an agent's inbox is whichever name its senders used, routinely the work
// item it is running rather than its agent name. Comparing names alone fired
// this refusal on people's OWN inboxes, in wording that reads like a permissions
// error; an agent meeting it concludes it may not read its own mail and leaves
// the mail unread, which is the exact outcome the guard exists to prevent.
func guardCrossBoxRead(mr, agent, msgID string, force bool) error {
	caller := os.Getenv("POGO_AGENT_NAME")
	if caller == "" {
		return nil
	}
	root, err := resolveRoot()
	if err != nil {
		return err
	}
	if callerOwnsMailbox(root, caller, agent) {
		return nil
	}
	if !force {
		mail.Audit(mr, "read-denied", agent, msgID, map[string]string{"reason": "cross-box"})
		// When the box is named for a real work item, say so. The guard cannot
		// prove the caller owns it (the names are unrelated), but a work-item
		// box belongs to whoever is running that item, and framing the refusal
		// purely as an intrusion on somebody else is what makes an agent
		// abandon its own mail rather than pass --force.
		hint := "re-run with --force if this cross-box read is intentional"
		if workItemNamed(root, canonicalAgent(agent)) {
			hint = fmt.Sprintf("%q is a WORK ITEM id, so this mailbox belongs to whoever is running that item — if that is you, mail addressed to your work item lands here rather than under your agent name, and --force is the right answer", agent)
		}
		return mgerr.Conflict("cross_box_read",
			fmt.Sprintf("refusing to read %s's mail as agent %q: reading marks the message read and hides it from %s's unread list", agent, caller, agent),
			hint)
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
archive it yourself with 'mg mail archive' when you are done with it.

The recipient is taken from a From header the original's sender wrote, which is
free text mg never validated, so a reply is a send to an unchecked address. It
gets the same treatment: a From naming nobody mg has seen is refused rather than
answered into a phantom mailbox, and --create is the override.`,
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

		// A From header is free text the sender wrote, so a reply is a send to
		// an unvalidated address and carries the same phantom-box risk. Same
		// refusal, same --create escape: replying to mail from a name nobody
		// can receive at should say so rather than answer into a dead drop.
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		if !mailSendCreate && !knownRecipient(mr, root, recipient) {
			return unknownRecipientError(mr, root, recipient)
		}

		newID, err := mail.SendWithOpts(mr, recipient, from, subject, mailSendBody, opts)
		if err != nil {
			return err
		}
		if err := registerViaCreate(mr, recipient, existed, "reply --create"); err != nil {
			return deliveredButUnrecorded(recipient, newID, err)
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
				Subject:        subject,
				// reply's default subject comes from the ORIGINAL message, not
				// from a body, so it is not the first-line derivation the flag
				// names. Reply is untouched by mg-158e.
				SubjectDerived: false,
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

var mailRegisterCmd = &cobra.Command{
	Use:   "register AGENT",
	Short: "Register a mailbox so mail can be addressed to it",
	Long: `Register a mailbox, making the name addressable by 'mg mail send'.

'mg mail send' refuses a recipient mg has never seen, because before that
refusal existed every send succeeded and a typo'd name minted a dead drop that
reported "Delivered". A recipient counts as seen when a mailbox of that name
exists, or when a work item is called that. This command supplies the first of
those directly: it creates the empty Maildir, so a new crew agent can be
addressed before anyone has mailed it.

It also writes a durable REGISTRATION RECORD naming who registered the box and
when. Existence alone could never answer "was this name meant?" — a box is
created by delivering to it, so a name talked past the refusal once is a good
address forever after, identical on disk to one somebody set up deliberately.
The 'daniel' mailbox is the live proof: in daily use, receiving real mail from
several agents, and never registered. It works, and "it works" is exactly the
evidence that was missing.

Registering a box that ALREADY EXISTS adopts it: the record is written, marked
adopted, and stamped with how much mail was already there. This is how a store
full of boxes that predate the record is brought into compliance, one name at a
time, and it is what 'mg mail list' points at when it marks a box unregistered.
Adoption vouches for the NAME going forward; it makes no claim about the mail
already in the box.

A STRAY PREFIXED BOX is refused rather than adopted. Mailbox names are
canonicalized, so registering an existing box named "cat-mg-01ce" would register
"01ce" instead: it mints a new empty mailbox, reports success, and leaves the
stray's mail exactly where it was. That is the phantom-box defect wearing a
fresh hat, produced by following the advice 'mg mail list' prints — so it is a
refusal (exit 4) naming 'mg mail migrate', which is the command that actually
merges a stray into its canonical box.

It is IDEMPOTENT — registering a box that already holds a record is exit 0 and
changes nothing, and the existing record's who/when is reported rather than
overwritten, because the record's value is naming the FIRST deliberate act. It
never touches mail, which is why registering a mailbox someone already uses
cannot lose anything.

'mg mail send AGENT --create' is the inline spelling: it registers and delivers
in one step, and its record says so ("via":"send --create") — a box established
in the act of talking past a refusal is worth being able to find later. Use this
command when the registration should happen ahead of any message — provisioning
an agent, or reserving the name it will be reached by.

With --json the result is a single object {mailbox,created,registered,adopted,
prior_messages,registered_at,registered_by,via}: created is whether the maildir
was made by this call, registered whether the record was, and the rest describe
the record now on disk whoever wrote it.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Canonicalize exactly as send/list do, so registering the alias
		// "mg-<id>" reserves the same box a "<id>" send lands in rather than
		// minting the stray twin 'mg mail migrate' exists to clean up.
		agent := canonicalAgent(args[0])

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		// ...but when the alias is itself a real box on disk, canonicalizing
		// silently retargets the command at a DIFFERENT mailbox. See
		// strayMailboxError: this is the one shape where registering a name
		// that `mg mail list` just marked creates a phantom box instead of
		// accounting for one.
		if raw := args[0]; raw != agent && mail.MailboxExists(mr, raw) {
			return strayMailboxError(mr, raw, agent)
		}

		existed := mail.MailboxExists(mr, agent)
		// Counted BEFORE the record is written, and counted only when there is
		// something to count: this is the size of what an adoption inherits
		// without vouching for it.
		prior := 0
		if existed {
			prior, err = mailboxMessageCount(mr, agent)
			if err != nil {
				return err
			}
		}

		rec := mail.Registration{RegisteredBy: workitem.Actor(), Via: "register"}
		wrote := true
		switch err := mail.Register(mr, agent, rec, existed, prior); {
		case errors.Is(err, mail.ErrAlreadyRegistered):
			// Idempotent, and deliberately not a rewrite: the record already
			// names a first deliberate act, and replacing it would erase the
			// only copy of who and when.
			wrote = false
		case err != nil:
			return err
		}

		// Report the record ON DISK, not the one just built. When this call did
		// not write, the two differ, and printing our own would tell a caller
		// its own name for a registration somebody else holds.
		onDisk, registered := mail.ReadRegistration(mr, agent)

		if mailJSON {
			out := mailRegisterJSON{
				Mailbox:    agent,
				Created:    !existed,
				Registered: wrote,
			}
			if onDisk != nil {
				out.Adopted = onDisk.Adopted
				out.PriorMessages = onDisk.PriorMessages
				out.RegisteredAt = onDisk.RegisteredAt
				out.RegisteredBy = onDisk.RegisteredBy
				out.Via = onDisk.Via
			}
			return encodeJSON(out)
		}

		switch {
		case !wrote:
			fmt.Printf("Mailbox %s is already registered%s\n", agent, registrationDetail(onDisk, registered))
		case existed:
			// The adoption case says outright that the box was in use
			// unregistered. "Registered mailbox: X" would be true and would
			// hide the thing worth knowing — that this name has been
			// receiving mail all along with nothing vouching for it.
			fmt.Printf("Registered existing mailbox: %s — it was in use UNREGISTERED, with %s already delivered; this registration vouches for the name from now on, not for that mail\n",
				agent, plural(prior, "message", "messages"))
		default:
			fmt.Printf("Registered mailbox: %s\n", agent)
		}
		return nil
	},
}

// strayMailboxError refuses to register a name that is BOTH an existing mailbox
// and an alias of a different one.
//
// Every mailbox argument is canonicalized, which is right for send and list —
// mail addressed to "cat-mg-01ce" belongs in the live box "01ce". Applied to
// register it is a trap: the caller names a box they can see in the listing,
// and the command registers a different name, mints a brand-new empty mailbox
// for it, and reports success while the box they pointed at keeps its mail and
// keeps its UNREGISTERED mark.
//
// That is this change's own defect reproduced by its own remedy — the listing
// says "run 'mg mail register NAME'", and for the 135-odd prefixed strays in
// the live store, following that advice mints a phantom box. So it is refused,
// and the refusal names 'mg mail migrate', which is the command that actually
// merges a stray into its canonical box rather than creating a twin beside it.
//
// CatConflict (exit 4), not not_found: both names exist, and the objection is
// to the state they are in, not to a name mg cannot see.
func strayMailboxError(mailRootDir, raw, canonical string) error {
	held := ""
	switch n, err := mailboxMessageCount(mailRootDir, raw); {
	case err != nil || n == 0:
	case n == 1:
		held = ", leaving its 1 message where it is"
	default:
		held = fmt.Sprintf(", leaving its %d messages where they are", n)
	}
	return mgerr.Conflict("stray_mailbox",
		fmt.Sprintf("%q is a stray prefixed mailbox: mailbox names are canonicalized, so registering it would register %q instead — minting a new empty mailbox%s",
			raw, canonical, held),
		fmt.Sprintf("run 'mg mail migrate --dry-run' to see what would move, then 'mg mail migrate' to merge %s into %s. To register the canonical name on purpose, name it directly: 'mg mail register %s'.",
			raw, canonical, canonical))
}

// registrationDetail renders " (registered by X at T, via Y)" for an existing
// record, or a plain statement that the detail is unreadable.
//
// registered is passed separately from rec because the two answer different
// questions and a damaged file separates them: presence is the registration,
// and losing the contents must not be allowed to report the box as unregistered
// — that would turn a corrupted file into a silent retraction of the fact it
// was written to record.
func registrationDetail(rec *mail.Registration, registered bool) string {
	if !registered {
		return ""
	}
	if rec == nil {
		return " (its registration record is unreadable, so who and when are lost; the registration itself stands)"
	}
	detail := ""
	if rec.RegisteredBy != "" {
		detail += " by " + rec.RegisteredBy
	}
	if rec.RegisteredAt != "" {
		detail += " at " + rec.RegisteredAt
	}
	if rec.Via != "" {
		detail += ", via " + rec.Via
	}
	if rec.Adopted {
		detail += " (adopted: the box already existed)"
	}
	if detail == "" {
		return ""
	}
	return " —" + detail
}

// registerViaCreate writes the registration record for a box being established
// by --create. It is a no-op without the flag, and a no-op for a box that
// already exists.
//
// Both no-ops are deliberate. Without --create, nothing was asserted: a first
// delivery to a name a work item vouches for is legitimate precisely BECAUSE
// the work item vouches for it, and stamping a record there would manufacture
// evidence of a step nobody took — the same lie, told the other way round, as
// the "already registered" this change removes.
//
// And --create aimed at a box that already existed established nothing, so it
// cannot vouch for it. The caller asserted the recipient was new and it was
// not; leaving the box unregistered keeps `mg mail list` pointing at it, which
// is the outcome that gets a human to look — and it stops --create silencing
// the marker as well as the refusal.
//
// existedBefore is passed in rather than measured, because by the time this
// runs the delivery has already created the box: asking the filesystem now
// would answer "yes" for every recipient and register none of them.
func registerViaCreate(mailRootDir, recipient string, existedBefore bool, via string) error {
	if !mailSendCreate || existedBefore {
		return nil
	}
	rec := mail.Registration{RegisteredBy: workitem.Actor(), Via: via}
	if err := mail.Register(mailRootDir, recipient, rec, false, 0); err != nil &&
		!errors.Is(err, mail.ErrAlreadyRegistered) {
		return err
	}
	return nil
}

// deliveredButUnrecorded reports a registration that failed AFTER its message
// was delivered. It leads with the delivery, because that is the part the
// caller must not repeat: a sender told only "registration failed" re-sends,
// and the recipient gets the message twice.
//
// The state left behind is degraded but not silent — the box exists, holds the
// mail, and carries no record, so `mg mail list` marks it UNREGISTERED and
// names the command that finishes the job. That is the same shape as the reply
// path's "delivered, but marking read failed": the durable half succeeded, and
// the caller is told exactly which half did not.
func deliveredButUnrecorded(recipient, msgID string, err error) error {
	return mgerr.Wrap(mgerr.CatInternal, "registration_failed", err,
		fmt.Sprintf("the message WAS delivered as %s/new/%s — do not re-send it. Only the registration record failed, so %s will show as UNREGISTERED in 'mg mail list'; run 'mg mail register %s' to record it.",
			recipient, msgID, recipient, recipient))
}

// mailboxMessageCount reports how much mail a box holds ALTOGETHER — unread,
// read and archived. It is the figure an adoption records, so it counts the
// archive too: mail somebody already filed is still mail the registration does
// not vouch for, and a count that quietly excluded it would understate exactly
// the history worth noticing.
func mailboxMessageCount(mailRootDir, agent string) (int, error) {
	msgs, _, err := mail.ListAll(mailRootDir, agent)
	if err != nil {
		return 0, err
	}
	archived, _, err := mail.ListArchived(mailRootDir, agent)
	if err != nil {
		return 0, err
	}
	return len(msgs) + len(archived), nil
}

// plural renders "1 message" / "3 messages".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
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
	mailSendCmd.Flags().StringVar(&mailSendSubject, "subject", "", "message subject (optional; omitted, it is taken from the body's first line and echoed back)")
	mailSendCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required unless --body-file)")
	mailSendCmd.Flags().StringVar(&mailSendBodyFile, "body-file", "", "read the message body verbatim from a file (\"-\" for stdin); mutually exclusive with --body")
	mailSendCmd.Flags().StringVar(&mailSendInReplyTo, "in-reply-to", "", "MSG-ID this message replies to (sets In-Reply-To and seeds References)")
	mailSendCmd.Flags().BoolVar(&mailSendCreate, "create", false, "deliver to a recipient mg has never seen, registering the mailbox (without this, an unknown recipient is refused)")
	mailSendCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailReplyCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (defaults to AGENT, the mailbox replied from)")
	mailReplyCmd.Flags().StringVar(&mailSendSubject, "subject", "", `subject (defaults to "Re: " + the original's)`)
	mailReplyCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")
	mailReplyCmd.Flags().BoolVar(&mailReplyForce, "force", false, "allow replying out of another agent's mailbox (marks the original read for its owner)")
	mailReplyCmd.Flags().BoolVar(&mailSendCreate, "create", false, "deliver to a sender mg has never seen, registering the mailbox (without this, an unknown recipient is refused)")
	mailReplyCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailReadCmd.Flags().BoolVar(&mailReadForce, "force", false, "allow reading another agent's mailbox (marks the message read for its owner)")
	mailReadCmd.Flags().BoolVar(&mailJSON, "json", false, "emit the message as a single JSON object instead of human-formatted output")

	mailArchiveCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailListCmd.Flags().BoolVarP(&mailListAll, "all", "a", false, "include read messages from cur/")
	mailListCmd.Flags().BoolVar(&mailListArchived, "archived", false, "list archived messages instead of the active mailbox")
	mailListCmd.Flags().StringSliceVar(&mailListFrom, "from", nil, "list only mail from these senders (repeatable, comma-separated); exact match on the From field, never a substring")
	mailListCmd.Flags().StringSliceVar(&mailListExcludeFrom, "exclude-from", nil, "hide mail from these senders (repeatable, comma-separated); exact match on the From field, so it never hides a real message whose SUBJECT mentions the name")
	mailListCmd.Flags().BoolVar(&mailJSON, "json", false, "emit one JSON object per line (NDJSON) instead of human-formatted output")

	mailRegisterCmd.Flags().BoolVar(&mailJSON, "json", false, "emit a single JSON object instead of human-formatted output")

	mailMigrateCmd.Flags().BoolVar(&mailMigrateDryRun, "dry-run", false, "report which stray mailboxes would be merged without moving any mail")

	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailRegisterCmd)
	mailCmd.AddCommand(mailReplyCmd)
	mailCmd.AddCommand(mailListCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailArchiveCmd)
	mailCmd.AddCommand(mailMigrateCmd)
}

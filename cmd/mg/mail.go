package main

import (
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
	Short: "Maildir-style messaging (send, list, read)",
}

var (
	mailSendFrom     string
	mailSendSubject  string
	mailSendBody     string
	mailListAll      bool
	mailListArchived bool
	mailReadForce    bool
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
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipient := args[0]

		if mailSendFrom == "" || mailSendSubject == "" || mailSendBody == "" {
			return fmt.Errorf("--from, --subject, and --body are required")
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		msgID, err := mail.Send(mr, recipient, mailSendFrom, mailSendSubject, mailSendBody)
		if err != nil {
			return err
		}

		fmt.Printf("Delivered: %s → %s/new/%s\n", mailSendFrom, recipient, msgID)
		return nil
	},
}

var mailListCmd = &cobra.Command{
	Use:   "list AGENT",
	Short: "List unread messages for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]

		mr, err := mailRoot()
		if err != nil {
			return err
		}

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

		if len(msgs) == 0 {
			switch {
			case mailListArchived:
				fmt.Printf("No archived messages for %s\n", agent)
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

var mailReadCmd = &cobra.Command{
	Use:   "read AGENT/MSG-ID",
	Short: "Read a specific message",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var agent, msgID string
		if len(args) == 1 {
			// agent/msgID format
			parts := strings.SplitN(args[0], "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("expected AGENT/MSG-ID format, got %q", args[0])
			}
			agent, msgID = parts[0], parts[1]
		} else {
			agent = args[0]
			msgID = args[1]
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

		fmt.Printf("From: %s\nSubject: %s\nDate: %s\n\n%s\n", msg.From, msg.Subject, msg.Date, msg.Body)
		return nil
	},
}

var mailArchiveCmd = &cobra.Command{
	Use:   "archive AGENT/MSG-ID",
	Short: "Archive a message (move it out of the active mailbox)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var agent, msgID string
		if len(args) == 1 {
			// agent/msgID format
			parts := strings.SplitN(args[0], "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("expected AGENT/MSG-ID format, got %q", args[0])
			}
			agent, msgID = parts[0], parts[1]
		} else {
			agent = args[0]
			msgID = args[1]
		}

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		msg, err := mail.Archive(mr, agent, msgID)
		if err != nil {
			return err
		}

		fmt.Printf("Archived: %s/%s  (%s: %s)\n", agent, msg.ID, msg.From, msg.Subject)
		return nil
	},
}

func init() {
	mailSendCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (required)")
	mailSendCmd.Flags().StringVar(&mailSendSubject, "subject", "", "message subject (required)")
	mailSendCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")

	mailReadCmd.Flags().BoolVar(&mailReadForce, "force", false, "allow reading another agent's mailbox (marks the message read for its owner)")

	mailListCmd.Flags().BoolVarP(&mailListAll, "all", "a", false, "include read messages from cur/")
	mailListCmd.Flags().BoolVar(&mailListArchived, "archived", false, "list archived messages instead of the active mailbox")

	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailListCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailArchiveCmd)
}

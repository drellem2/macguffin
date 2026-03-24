package main

import (
	"fmt"
	"path/filepath"

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
	mailSendFrom    string
	mailSendSubject string
	mailSendBody    string
	mailListAll     bool
)

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

		var msgs []mail.Message
		if mailListAll {
			msgs, err = mail.ListAll(mr, agent)
		} else {
			msgs, err = mail.List(mr, agent)
		}
		if err != nil {
			return err
		}

		if len(msgs) == 0 {
			if mailListAll {
				fmt.Printf("No messages for %s\n", agent)
			} else {
				fmt.Printf("No unread messages for %s\n", agent)
			}
			return nil
		}

		for _, m := range msgs {
			status := "●"
			if m.Read {
				status = " "
			}
			fmt.Printf("  %s %s  %-12s  %s\n", status, m.ID, m.From, m.Subject)
		}
		return nil
	},
}

var mailReadCmd = &cobra.Command{
	Use:   "read AGENT MSG-ID",
	Short: "Read a specific message",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]
		msgID := args[1]

		mr, err := mailRoot()
		if err != nil {
			return err
		}

		msg, err := mail.Read(mr, agent, msgID)
		if err != nil {
			return err
		}

		fmt.Printf("From: %s\nSubject: %s\nDate: %s\n\n%s\n", msg.From, msg.Subject, msg.Date, msg.Body)
		return nil
	},
}

func init() {
	mailSendCmd.Flags().StringVar(&mailSendFrom, "from", "", "sender name (required)")
	mailSendCmd.Flags().StringVar(&mailSendSubject, "subject", "", "message subject (required)")
	mailSendCmd.Flags().StringVar(&mailSendBody, "body", "", "message body (required)")

	mailListCmd.Flags().BoolVarP(&mailListAll, "all", "a", false, "include read messages from cur/")

	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailListCmd)
	mailCmd.AddCommand(mailReadCmd)
}

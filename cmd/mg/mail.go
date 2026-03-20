package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/workspace"
)

func mailRoot() (string, error) {
	root, err := workspace.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mail"), nil
}

func runMailSend(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mg mail send <agent> --from=X --subject=X --body=X")
	}

	// First positional arg is the recipient; rest are flags.
	recipient := args[0]
	fs := flag.NewFlagSet("mg mail send", flag.ExitOnError)
	from := fs.String("from", "", "sender name (required)")
	subject := fs.String("subject", "", "message subject (required)")
	body := fs.String("body", "", "message body (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if *from == "" || *subject == "" || *body == "" {
		return fmt.Errorf("--from, --subject, and --body are required")
	}

	mr, err := mailRoot()
	if err != nil {
		return err
	}

	msgID, err := mail.Send(mr, recipient, *from, *subject, *body)
	if err != nil {
		return err
	}

	fmt.Printf("Delivered: %s → %s/new/%s\n", *from, recipient, msgID)
	return nil
}

func runMailList(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mg mail list <agent>")
	}
	agent := args[0]

	mr, err := mailRoot()
	if err != nil {
		return err
	}

	msgs, err := mail.List(mr, agent)
	if err != nil {
		return err
	}

	if len(msgs) == 0 {
		fmt.Printf("No unread messages for %s\n", agent)
		return nil
	}

	for _, m := range msgs {
		fmt.Printf("  %s  %-12s  %s\n", m.ID, m.From, m.Subject)
	}
	return nil
}

func runMailRead(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: mg mail read <agent> <msg-id>")
	}
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
}

func runMail() error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: mg mail <send|list|read> ...")
	}

	switch os.Args[2] {
	case "send":
		return runMailSend(os.Args[3:])
	case "list":
		return runMailList(os.Args[3:])
	case "read":
		return runMailRead(os.Args[3:])
	default:
		return fmt.Errorf("mg mail: unknown subcommand %q (use send, list, read)", os.Args[2])
	}
}

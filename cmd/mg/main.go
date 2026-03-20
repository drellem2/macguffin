package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		git := len(os.Args) > 2 && os.Args[2] == "--git"
		if err := runInit(git); err != nil {
			fmt.Fprintf(os.Stderr, "mg init: %v\n", err)
			os.Exit(1)
		}
	case "mail":
		if err := runMail(); err != nil {
			fmt.Fprintf(os.Stderr, "mg mail: %v\n", err)
			os.Exit(1)
		}
	case "snapshot":
		if err := runSnapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "mg snapshot: %v\n", err)
			os.Exit(1)
		}
	case "log":
		if err := runLog(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "mg log: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("mg %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "mg: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: mg <command>

Commands:
  init [--git]   Initialize ~/.macguffin directory tree (--git enables git snapshots)
  mail           Maildir-style messaging (send, list, read)
  snapshot       Create a git snapshot of current state
  log [args]     Show git snapshot history (passes args to git log)
  version        Print version
  help           Show this message
`)
}

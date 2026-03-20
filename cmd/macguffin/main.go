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
		if err := runInit(); err != nil {
			fmt.Fprintf(os.Stderr, "macguffin init: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("macguffin %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "macguffin: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: macguffin <command>

Commands:
  init      Initialize ~/.macguffin directory tree
  version   Print version
  help      Show this message
`)
}

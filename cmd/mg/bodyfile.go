package main

import (
	"fmt"
	"io"
	"os"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/spf13/cobra"
)

// bodyFromFlags resolves the --body / --body-file pair shared by 'mg mail
// send', 'mg new' and 'mg edit'. It returns the body text and whether either
// flag was given at all, so a caller that distinguishes an explicitly empty
// --body (clear the field) from an absent one (leave it alone) — 'mg edit'
// does — keeps that distinction under --body-file too.
//
// WHY --body-file EXISTS. The shell expands `backticks`, $VAR and $(cmd) inside
// --body="..." BEFORE mg is executed, so main() receives an already-mangled
// string and reports Delivered on a body it silently corrupted. mg cannot
// detect this after the fact: the shell-eaten body and a body someone typed
// that way are byte-identical at the only point mg can observe, so any check
// here would be a heuristic guessing at prose — a check that cannot reliably
// fail. The fix is the incentive gradient, not a check. Before, the safe path
// cost more keystrokes AND more knowledge than the dangerous one:
//
//	--body="..."            dangerous, short, natural — what you reach for
//	--body="$(cat file)"    safe, longer, requires knowing the trick
//	--body-file ./msg.md    safe AND short — no shell in the path at all
//
// --body-file makes careful cheaper than careless. It asks nobody to be
// vigilant, which is the point: this hazard has bitten callers who had read the
// warning in the same session. Knowing is not a mechanism.
//
// --body is deliberately untouched and NOT deprecated: it is fine for the many
// bodies that carry no metacharacters, and removing it would break every caller
// for whom it already works.
func bodyFromFlags(cmd *cobra.Command, body, bodyFile string) (string, bool, error) {
	return bodyFromFlagPair(cmd, "body", "body-file", body, bodyFile)
}

// appendBodyFromFlags is bodyFromFlags for the --append-body / --append-body-file
// pair that 'mg edit' adds. Same rules, same verbatim file reader — an append is
// a body just as exposed to the shell as a replacement, so it gets the same
// escape hatch rather than an inline-only flag that would quietly reintroduce
// the expansion hazard on the safer write path.
func appendBodyFromFlags(cmd *cobra.Command, body, bodyFile string) (string, bool, error) {
	return bodyFromFlagPair(cmd, "append-body", "append-body-file", body, bodyFile)
}

// bodyFromFlagPair implements the inline/file pairing for one named pair of
// flags: mutually exclusive, file wins when given, an unreadable file is an
// error rather than an empty body.
func bodyFromFlagPair(cmd *cobra.Command, inlineName, fileName, body, bodyFile string) (string, bool, error) {
	bodyGiven := cmd.Flags().Changed(inlineName)
	fileGiven := cmd.Flags().Changed(fileName)

	if bodyGiven && fileGiven {
		return "", false, mgerr.Usage("mutually_exclusive_flags",
			fmt.Sprintf("cannot use both --%s and --%s", inlineName, fileName),
			fmt.Sprintf("pass the body inline with --%s, or name a file with --%s — not both", inlineName, fileName))
	}
	if !fileGiven {
		return body, bodyGiven, nil
	}

	text, err := readBodyFile(cmd, fileName, bodyFile)
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// readBodyFile reads the body bytes from path verbatim — no shell, no
// expansion, no interpretation. "-" reads stdin instead.
//
// A path that cannot be read is an error, never an empty body: silently
// sending nothing on a typo'd path would be this ticket's own disease
// (an instrument reporting success for work it did not do). The path came off
// the command line, so a bad one is a malformed invocation — exit 2, the same
// class as any other bad flag value.
func readBodyFile(cmd *cobra.Command, flagName, path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", mgerr.Usage("invalid_value",
				fmt.Sprintf("cannot read body from stdin: %v", err), "")
		}
		return string(data), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", mgerr.Usage("invalid_value",
			fmt.Sprintf("cannot read --%s %q: %v", flagName, path, err),
			"check that the path exists and is a readable file")
	}
	return string(data), nil
}

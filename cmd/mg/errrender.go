package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/spf13/cobra"
)

// outputJSON records whether the invoked command ran in --json mode, so the
// exit seam in main() can render a JSON error object to stderr instead of the
// human text. It is set by the root PersistentPreRun below (see
// registerErrorRendering) which reads the executed command's own --json flag —
// there is no single global --json flag; each data command binds its own, so we
// discover it generically at run time. For a pure arg-count/flag error cobra
// fails before PersistentPreRun, leaving this false — a malformed invocation
// getting human text is acceptable (§5).
var outputJSON bool

// jsonError is the FROZEN wire shape of an mg error object (§4). It is emitted
// to STDERR, namespaced under the top-level "error" key so it can never collide
// with a data object streamed to stdout. Field names and the category/code
// slugs are frozen and additive-only, same governance as the data --json
// contract (mg-9be5).
type jsonError struct {
	Error jsonErrorBody `json:"error"`
}

type jsonErrorBody struct {
	Code      string `json:"code"`
	Category  string `json:"category"`
	Exit      int    `json:"exit"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable"` // always present for predictable parsing
}

// writeHumanError renders e in the traditional human form: "Error: <message>"
// plus an indented "  → <hint>" line when a hint exists. This reproduces the
// old withHint UX with the hint on its own line.
func writeHumanError(w io.Writer, e *mgerr.Error) {
	fmt.Fprintf(w, "Error: %s\n", e.Message)
	if e.Hint != "" {
		fmt.Fprintf(w, "  → %s\n", e.Hint)
	}
}

// writeJSONError renders e as the single frozen JSON error object on w.
func writeJSONError(w io.Writer, e *mgerr.Error) {
	obj := jsonError{Error: jsonErrorBody{
		Code:      e.Code,
		Category:  e.Category.String(),
		Exit:      e.ExitCode(),
		Message:   e.Message,
		Hint:      e.Hint,
		Retryable: e.Retryable,
	}}
	// Marshal cannot realistically fail for this fixed struct; if it somehow
	// did, fall back to the human form so the user still sees the message.
	b, err := json.Marshal(obj)
	if err != nil {
		writeHumanError(w, e)
		return
	}
	fmt.Fprintln(w, string(b))
}

// usageArgs wraps a cobra positional-args validator so any arg-count error it
// produces is routed to the usage category (exit 2) instead of falling through
// to the Internal(1) default in Coerce. Mechanical and explicit — each
// validating command's Args field is wrapped with this (§6.2).
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := v(cmd, args); err != nil {
			return mgerr.Usage("usage", err.Error(), cmd.UseLine())
		}
		return nil
	}
}

// registerErrorRendering installs a root PersistentPreRun that records whether
// the command about to execute is in --json mode. Cobra runs only the
// closest-in-chain PersistentPreRun; no mg subcommand defines its own, so this
// one fires for every command. It is additive and side-effect-free beyond
// setting the package flag.
func registerErrorRendering() {
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if f := cmd.Flags().Lookup("json"); f != nil && f.Changed {
			outputJSON = true
		}
	}
}

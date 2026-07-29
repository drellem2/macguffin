package main

import (
	"fmt"
	"os"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/spf13/cobra"
)

// Build metadata, overridden at release time via -ldflags "-X main.version=...".
// version is the release tag (e.g. "v0.1.3"); commit and date carry the git SHA
// and build timestamp so a binary in the wild can be traced back to its source.
// The sentinels below are what a plain `go install`/`go build` (no ldflags)
// produces — a dev build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString renders the human-facing version. For a dev build (no ldflags)
// it is just "dev"; a release build appends the commit and date build metadata,
// e.g. "v0.1.3 (abc1234, 2026-07-07)". Both `mg version` and `mg --version`
// print this same string.
func versionString() string {
	s := version
	if commit != "none" && commit != "" {
		meta := commit
		if date != "unknown" && date != "" {
			meta += ", " + date
		}
		s += " (" + meta + ")"
	}
	return s
}

var rootCmd = &cobra.Command{
	Use:           "mg",
	Short:         "macguffin work-item tracker",
	Version:       versionString(),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetOut(os.Stderr)
		cmd.Println(cmd.UsageString())
		return fmt.Errorf("a command is required")
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("mg %s\n", versionString())
	},
}

func init() {
	// cobra auto-adds --version/-v because rootCmd.Version is set. Override the
	// default "mg version X" template so the flag prints "mg X\n" — identical to
	// the `mg version` subcommand (gh drellem2/pogo#59).
	rootCmd.SetVersionTemplate("mg {{.Version}}\n")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(claimCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(mailCmd)
	rootCmd.AddCommand(unclaimCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(scheduleCmd)
	rootCmd.AddCommand(eventCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(unarchiveCmd)
	rootCmd.AddCommand(editCmd)
	rootCmd.AddCommand(restoreBodyCmd)
	rootCmd.AddCommand(assignCmd)
	rootCmd.AddCommand(reopenCmd)
	rootCmd.AddCommand(shelveCmd)
	rootCmd.AddCommand(unshelveCmd)
	rootCmd.AddCommand(snoozeCmd)
	rootCmd.AddCommand(unsnoozeCmd)
	rootCmd.AddCommand(flowCmd)
	rootCmd.AddCommand(spendCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(sidecarsCmd)

	// Route cobra-generated flag errors (unknown flag, bad flag value) to the
	// usage category (exit 2). Arg-count validators are routed via the
	// usageArgs() wrapper on each command's Args field. See §6.
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return mgerr.Usage("usage", err.Error(), cmd.UseLine())
	})
	registerErrorRendering()
}

// main is the single exit seam for the whole binary. rootCmd sets
// SilenceErrors+SilenceUsage, so EVERY error — RunE returns, cobra usage
// errors, everything — funnels here. It coerces the error into the typed
// taxonomy, renders it (JSON to stderr when --json was requested, otherwise
// human text), and exits with the category's frozen code (§5).
func main() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	e := mgerr.Coerce(err)
	if outputJSON {
		writeJSONError(os.Stderr, e)
	} else {
		writeHumanError(os.Stderr, e)
	}
	os.Exit(e.ExitCode())
}

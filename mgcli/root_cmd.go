package mgcli

import (
	"fmt"
	"os"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
	"github.com/spf13/cobra"
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
	rootCmd.AddCommand(reclaimCmd)
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
	rootCmd.AddCommand(sidecarCmd)

	// Route cobra-generated flag errors (unknown flag, bad flag value) to the
	// usage category (exit 2). Arg-count validators are routed via the
	// usageArgs() wrapper on each command's Args field. See §6.
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return mgerr.Usage("usage", err.Error(), cmd.UseLine())
	})
	registerErrorRendering()
}

// Build is the provenance of the binary calling Main. cmd/mg fills it from its
// -X main.* ldflags (build.sh, .goreleaser.yml); the zero value renders as a dev
// build.
type Build struct {
	Version string // release tag, e.g. "v0.1.3"; "" or "dev" for a dev build
	Commit  string // git SHA; "" or "none" when unknown
	Date    string // build timestamp; "" or "unknown" when unknown
}

// version, commit and date are what versionString renders. Main sets them from
// its Build before any command runs.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Main runs the mg command line against the storage backend open returns for
// the invocation's workspace root, and exits the process. It never returns.
//
// It is the entry point for every build of mg: cmd/mg calls it with the
// filesystem backend (fsstore.Open), and a separate module builds mg against
// its own backend by calling it with that backend's store.Opener. See package
// store for what a backend must provide.
//
// Main is the single exit seam for the whole binary. rootCmd sets
// SilenceErrors+SilenceUsage, so EVERY error — RunE returns, cobra usage
// errors, everything — funnels here. It coerces the error into the typed
// taxonomy, renders it (JSON to stderr when --json was requested, otherwise
// human text), and exits with the category's frozen code (§5).
func Main(open store.Opener, b Build) {
	if b.Version != "" {
		version = b.Version
	}
	if b.Commit != "" {
		commit = b.Commit
	}
	if b.Date != "" {
		date = b.Date
	}
	rootCmd.Version = versionString()
	useBackend(open)

	err := rootCmd.Execute()

	// REPORT the opportunistic snooze promoter here, after the command has
	// written everything it is going to write and before any error rendering.
	// The promoter itself was already joined in PersistentPreRun, in front of
	// the command, so that no command can read a store its own process is still
	// rewriting; what is left for the seam is saying what happened, below the
	// output the caller actually asked for. It cannot change err.
	finishAutoPromote(os.Stderr)

	if err == nil {
		os.Exit(0)
	}
	e := mgerr.Coerce(err)
	if outputJSON {
		writeJSONError(os.Stderr, e)
	} else {
		writeHumanError(os.Stderr, e)
	}
	os.Exit(e.ExitCode())
}

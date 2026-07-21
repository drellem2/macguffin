package main

import (
	"fmt"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	archiveDays      int
	archiveDryRun    bool
	archiveSuccessor string
	archiveForce     bool
)

var archiveCmd = &cobra.Command{
	Use:   "archive [ID]",
	Short: "Archive a done work item, or sweep done items older than N days",
	Long: `Archive moves done work items into archive/<YYYY-MM>/.

With an ID, it archives exactly that one item and nothing else. The item
must be done; any other status is an error. This is the form to use when
you have finished a work item that produced no code change, since the
refinery only archives items it merged.

With no ID, it sweeps done/ and archives every item older than --days.
The sweep is unfiltered: --days=0 archives ALL done items, including any
you are keeping deliberately. Use --dry-run to preview it first.

The two forms are exclusive — passing both an ID and --days is an error
rather than a silent choice between archiving one item and archiving
every item.

Done "design" items are guarded. A design's output IS a recommendation, so
at the moment it is done the thing it recommends is undone by construction,
and archiving it hides that work — an archived item cannot be the tracker
for undone work. Archiving one is therefore refused unless the design names
a successor:

    mg archive <id> --successor <id-of-the-item-that-tracks-it>

which records a "successor:<id>" tag on the design before moving it, so the
archived record names its own tracker. The check is on the item TYPE, not on
any wording in its body, and it is satisfied only by that structured tag —
a body that merely mentions an id proves nothing about who is tracking what.

A design that was abandoned rather than implemented is a legitimate archive:
--force archives it anyway and records a work.archive_forced event naming the
guard that was bypassed. The sweep form never forces; it skips guarded items,
leaves them in done/, and reports them.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		if len(args) == 1 {
			// The targeted form must never be confused with the sweep: if the
			// caller named an item AND a threshold, we cannot know which they
			// meant, and guessing risks archiving the whole of done/.
			if cmd.Flags().Changed("days") {
				return mgerr.Usage("usage",
					"--days sweeps every done item and cannot be combined with an ID, which archives exactly one.",
					"Run 'mg archive "+args[0]+"' to archive just that item, or 'mg archive --days=N' to sweep.")
			}
			return archiveOne(cmd, root, args[0])
		}

		// --successor and --force answer a guard that only the targeted form
		// raises. Accepting them on the sweep would either silently ignore
		// them or turn `mg archive --days=0 --force` into a bulk bypass of the
		// refusal; both are worse than saying so.
		for _, f := range []string{"successor", "force"} {
			if cmd.Flags().Changed(f) {
				return mgerr.Usage("usage",
					"--"+f+" answers the design-successor guard for one named item and cannot be combined with the sweep.",
					"Run 'mg archive <id> --"+f+"' on the item you mean.")
			}
		}

		return archiveSweep(cmd, root)
	},
}

// archiveOne archives exactly the named item. It reports what it did or fails
// loudly; it never falls back to the sweep.
func archiveOne(cmd *cobra.Command, root, id string) error {
	opts := workitem.ArchiveOpts{Successor: archiveSuccessor, Force: archiveForce}

	if archiveDryRun {
		// CheckArchivable, not Read: a preview that says "Would archive" for an
		// item the real run will refuse is a false pass one step removed.
		item, err := workitem.CheckArchivable(root, id, opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Would archive %s: %s\n", item.ID, item.Title)
		return nil
	}

	item, err := workitem.ArchiveItem(root, id, opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Archived %s: %s\n", item.ID, item.Title)
	return nil
}

// archiveSweep archives every done item older than --days.
func archiveSweep(cmd *cobra.Command, root string) error {
	maxAge := time.Duration(archiveDays) * 24 * time.Hour

	if archiveDryRun {
		items, skipped, err := workitem.ArchiveDryRun(root, maxAge)
		if err != nil {
			return err
		}
		if len(items) == 0 && len(skipped) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No items to archive.")
			return nil
		}
		for _, item := range items {
			fmt.Fprintf(cmd.OutOrStdout(), "Would archive %s: %s\n", item.ID, item.Title)
		}
		if len(items) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Would archive %d item(s). Re-run without --dry-run to apply.\n", len(items))
		}
		reportSkipped(cmd, skipped, "would skip")
		return nil
	}

	archived, skipped, err := workitem.Archive(root, maxAge)
	if err != nil {
		return err
	}

	if len(archived) == 0 && len(skipped) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No items to archive.")
		return nil
	}

	for _, item := range archived {
		fmt.Fprintf(cmd.OutOrStdout(), "Archived %s: %s\n", item.ID, item.Title)
	}
	if len(archived) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Archived %d item(s).\n", len(archived))
	}
	reportSkipped(cmd, skipped, "Skipped")
	return nil
}

// reportSkipped names the done design items the sweep left behind. It goes to
// STDERR and names every id: a sweep that quietly declined to archive some of
// its candidates and reported only a count would be indistinguishable from a
// sweep that archived them, which is the silence this guard exists to break.
func reportSkipped(cmd *cobra.Command, skipped []*workitem.Item, verb string) {
	if len(skipped) == 0 {
		return
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "%s %d done design item(s) with no successor:\n", verb, len(skipped))
	for _, item := range skipped {
		fmt.Fprintf(w, "  %s: %s\n", item.ID, item.Title)
	}
	fmt.Fprintln(w, "They stay in done/. File the item that tracks each recommendation, then run 'mg archive <id> --successor <id>'.")
}

func init() {
	archiveCmd.Flags().IntVar(&archiveDays, "days", 7, "archive done items older than this many days (sweep form only)")
	archiveCmd.Flags().BoolVar(&archiveDryRun, "dry-run", false, "print what would be archived without archiving it")
	archiveCmd.Flags().StringVar(&archiveSuccessor, "successor", "", "id of the item that tracks what this design recommends; required to archive a done design (ID form only)")
	archiveCmd.Flags().BoolVar(&archiveForce, "force", false, "archive a done design that nothing tracks — for designs abandoned rather than implemented; recorded as a work.archive_forced event (ID form only)")
}

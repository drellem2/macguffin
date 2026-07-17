package main

import (
	"fmt"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	archiveDays   int
	archiveDryRun bool
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
every item.`,
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

		return archiveSweep(cmd, root)
	},
}

// archiveOne archives exactly the named item. It reports what it did or fails
// loudly; it never falls back to the sweep.
func archiveOne(cmd *cobra.Command, root, id string) error {
	if archiveDryRun {
		item, err := workitem.Read(root, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Would archive %s: %s\n", item.ID, item.Title)
		return nil
	}

	item, err := workitem.ArchiveItem(root, id)
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
		items, err := workitem.ArchiveDryRun(root, maxAge)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No items to archive.")
			return nil
		}
		for _, item := range items {
			fmt.Fprintf(cmd.OutOrStdout(), "Would archive %s: %s\n", item.ID, item.Title)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Would archive %d item(s). Re-run without --dry-run to apply.\n", len(items))
		return nil
	}

	archived, err := workitem.Archive(root, maxAge)
	if err != nil {
		return err
	}

	if len(archived) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No items to archive.")
		return nil
	}

	for _, item := range archived {
		fmt.Fprintf(cmd.OutOrStdout(), "Archived %s: %s\n", item.ID, item.Title)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Archived %d item(s).\n", len(archived))
	return nil
}

func init() {
	archiveCmd.Flags().IntVar(&archiveDays, "days", 7, "archive done items older than this many days (sweep form only)")
	archiveCmd.Flags().BoolVar(&archiveDryRun, "dry-run", false, "print what would be archived without archiving it")
}

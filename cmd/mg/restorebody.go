package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	restoreBodyFrom        string
	restoreBodyList        bool
	restoreBodyIfUnchanged string
)

var restoreBodyCmd = &cobra.Command{
	Use:   "restore-body ID [flags]",
	Short: "Restore a work item's body from a saved prior version",
	Long: `Put back a body that a replace-mode edit overwrote.

Every 'mg edit --body' / '--body-file' saves the body it is about to destroy,
under ~/.macguffin/work/.bodybak/<id>/, named by the UTC timestamp of the write
and the first 8 hex of the body's hash. The ten most recent are kept per item.

  mg restore-body mg-1234 --list      # what is saved, newest first
  mg restore-body mg-1234             # put back the most recent
  mg restore-body mg-1234 --from=20260729T161400

--from matches a timestamp PREFIX, so the leading date-and-time is usually
enough. A prefix naming no saved body, or more than one, is refused rather than
resolved to a best guess: picking for you is how you restore the wrong version
onto a body you have just destroyed.

An item with nothing saved is an ERROR (exit 3, no_body_backup), never a quiet
success and never an empty body. mg saves a body only when a replace-mode edit
overwrites it, and only from the point this was installed, so a body destroyed
before then has nothing here — 'mg event list --type=work.edited' still records
that it happened, with the before/after hashes and line counts, but not the
bytes.

RESTORING IS ITSELF A REPLACE-MODE EDIT, so the body it overwrites is saved
first. Restoring the wrong version is undoable; that is what makes trying one
safe. Pass --if-unchanged to guard the restore against a concurrent writer, the
same way you would guard any other full-body write.

The saved bytes are replayed exactly as '--body-file' would write them,
including the '# Title' heading they carried when they were saved.

WHAT IS NOT SAVED. Only the wholesale overwrite path. '--append-body-file'
composes against the body on disk at write time and cannot destroy a section it
never saw, so it is already safe and is not backed up; nor is '--title', which
rewrites the heading line in place and leaves every other byte alone.

WHERE BACKUPS GO ON A TRANSITION. They are keyed by ID and do not move on
claim/unclaim/done/reopen/shelve/unshelve — a shelved item's saved bodies stay
in work/.bodybak/<id>/ and restore normally. 'mg archive' moves them into
work/archive/<partition>/.bodybak/<id>/ with the record, so the archive stays
self-contained and nothing is orphaned in the live tree; 'mg unarchive' brings
them back.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}
		id := args[0]

		if restoreBodyList {
			backups, err := workitem.ListBodyBackups(root, id)
			if err != nil {
				return err
			}
			if len(backups) == 0 {
				// A report, not a gate: "nothing has ever overwritten this
				// body" is a legitimate answer to --list, and exit 0 says so.
				// The refusal belongs to the restore, which cannot proceed.
				fmt.Printf("No saved bodies for %s.\n", id)
				return nil
			}
			fmt.Printf("%d saved body/bodies for %s, newest first:\n\n", len(backups), id)
			for _, b := range backups {
				fmt.Printf("  %s  %s  %d lines, %d bytes\n", b.Stamp, b.Hash, b.Lines, b.Bytes)
				fmt.Printf("      %s\n", b.Path)
			}
			fmt.Printf("\nRestore one with: mg restore-body %s --from=%s\n", id, backups[0].Stamp)
			return nil
		}

		item, restored, err := workitem.RestoreBody(root, id, restoreBodyFrom, restoreBodyIfUnchanged)
		if err != nil {
			return err
		}

		fmt.Printf("Restored %s body from %s (%d lines, hash %s)\n",
			item.ID, restored.Stamp, restored.Lines, restored.Hash)
		// The body this restore overwrote was itself saved. Saying so on the
		// success line is what makes the undo discoverable at the one moment
		// someone might need it — right after restoring the wrong version.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: the body this replaced was saved first; 'mg restore-body %s --list' to undo.\n", item.ID)
		return nil
	},
}

func init() {
	restoreBodyCmd.Flags().StringVar(&restoreBodyFrom, "from", "", "restore the saved body whose timestamp starts with this (default: the most recent)")
	restoreBodyCmd.Flags().BoolVar(&restoreBodyList, "list", false, "list the saved bodies instead of restoring one")
	restoreBodyCmd.Flags().StringVar(&restoreBodyIfUnchanged, "if-unchanged", "", "refuse the restore unless the stored body still hashes to this (from 'mg show ID --body-hash')")
}

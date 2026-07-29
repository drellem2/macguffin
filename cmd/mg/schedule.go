package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Promote pending items whose dependencies are met, and report those that can never be promoted",
	Long: `Promote pending items whose dependencies are met.

A dependency is met once the parent has passed through done — done/ and
archive/ both count, because archiving is a filing decision about completed
work, not a repudiation of the completion.

Items that no completion can ever release are reported rather than skipped
silently: a dependent waiting on a shelved parent, or on an id that does not
exist, is not waiting — it is stranded. It cannot be seen from anywhere else,
because it is not available/ (so stall-watch and priority-wake do not reach it)
and "pending" is exactly what a correctly-waiting item looks like.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		promoted, err := workitem.Schedule(root)
		if err != nil {
			return err
		}

		if len(promoted) == 0 {
			fmt.Println("No items promoted.")
		}
		for _, item := range promoted {
			fmt.Printf("Promoted %s: %s\n", item.ID, item.Title)
		}

		// Report what the sweep could never promote. A pending item waiting on
		// a shelved or nonexistent parent is not waiting — it is stranded, and
		// it looks identical to a correctly-waiting item from every other
		// angle, which is why it goes unnoticed for weeks. This is the only
		// place that distinguishes the two, so it reports rather than stays
		// silent about the items it stepped over.
		stranded, err := workitem.Stranded(root)
		if err != nil {
			return err
		}
		if len(stranded) > 0 {
			fmt.Printf("\n%d pending item(s) can never be promoted:\n", len(stranded))
			for _, s := range stranded {
				fmt.Printf("  %s: %s\n", s.Item.ID, s.Item.Title)
				fmt.Printf("    blocked because %s\n", s.Reason())
			}
			fmt.Printf("\nUnshelve the parent, correct the dependency with `mg edit --rm-depends`,\n")
			fmt.Printf("or shelve the dependent so it stops claiming to be waiting.\n")
		}
		return nil
	},
}

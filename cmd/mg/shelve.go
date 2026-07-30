package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	shelveTag      string
	shelveOverride string
)

var shelveCmd = &cobra.Command{
	Use:   "shelve [ID]",
	Short: "Shelve a work item and its dependents",
	Long: `Shelve hides a work item and all items that depend on it from normal
listing. Shelved items can be listed with 'mg list --status=shelved' and
restored with 'mg unshelve'.

Use --tag to shelve all items with a given tag.

Shelving is guarded, because a shelved item is not a tracker for anything. mg
refuses to shelve an item that:

  * carries a 'blocked-on-*' tag — a named person still owes something here;
  * declares a remainder — it carries the 'declares-remainder' tag, or its type
    is one whose output IS a recommendation (design, scoping, audit, idea), or
    its body's carrier block says 'stage: triage'

unless the item already names a tracker with a successor: tag:

    mg edit <id> --add-tags=successor:<id-of-the-item-that-tracks-it>

The blocked-on arm is not answered by a successor: naming a tracker says nothing
about whether a person still owes something. Settle what the tag names and
remove it:

    mg edit <id> --rm-tags=blocked-on-<who>

Some shelves are legitimate anyway — a design genuinely abandoned rather than
deferred, an obligation discharged out of band. --override shelves it and
records a work.shelve_forced event naming BOTH the guard it bypassed and the
reason given:

    mg shelve <id> --override="superseded by mg-1234, which carries the build"

The reason is required and is a string, not a flag: a bare --force records that
somebody overrode the gate and loses the only thing a later reader needs, which
is what they knew that the gate did not. Whitespace is not a reason.

--override applies to one named item and cannot be combined with --tag: an
override is a claim about an item the operator looked at, and a bulk one is a
claim about items they did not.

Shelving CASCADES: every open item that depends on the target is shelved too,
which is how a shelve can hide an audit or a follow-up filed against it. The
cascade is not refused — it is reported. The ids it hides are printed here and
recorded on the work.shelve event.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		// An override the caller wrote as blank is a mistake worth naming, not
		// a silent no-op that resurfaces as the guard's refusal one line later.
		if cmd.Flags().Changed("override") && strings.TrimSpace(shelveOverride) == "" {
			return mgerr.Usage("empty_override",
				"--override needs a reason: it records what you know that the guard does not.",
				"Run 'mg shelve <id> --override=\"<why this is safe to shelve>\"'.")
		}

		if shelveTag != "" {
			if cmd.Flags().Changed("override") {
				return mgerr.Usage("usage",
					"--override answers a guard for one named item and cannot be combined with --tag.",
					"Run 'mg shelve <id> --override=\"<reason>\"' on the item you mean.")
			}
			return shelveByTag(cmd, root, shelveTag)
		}

		if len(args) == 0 {
			return fmt.Errorf("requires a work item ID or --tag flag")
		}

		var opts []workitem.ShelveOption
		if shelveOverride != "" {
			opts = append(opts, workitem.WithShelveOverride(shelveOverride))
		}

		items, err := workitem.Shelve(root, args[0], opts...)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Shelved %s: %s\n", items[0].ID, items[0].Title)
		reportCascade(cmd, items[1:])
		return nil
	},
}

// shelveByTag runs the bulk form and reports both halves of what it did.
func shelveByTag(cmd *cobra.Command, root, tag string) error {
	items, skipped, err := workitem.ShelveByTag(root, tag)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, item := range items {
		fmt.Fprintf(out, "Shelved %s: %s\n", item.ID, item.Title)
	}
	reportShelveSkipped(cmd, skipped)
	return nil
}

// reportCascade names the items a shelve hid that the operator did not ask
// about.
//
// It goes to STDOUT alongside the item that was named, and it names every id,
// because the cascade is the reason shelve is the most destructive of the three
// exits: 32 of the 175 items on the live shelf on 2026-07-30 got there as a
// dependent, and nothing ever told anyone. A count under a summary line would
// leave the operator to go looking for which ones.
func reportCascade(cmd *cobra.Command, dependents []*workitem.Item) {
	if len(dependents) == 0 {
		return
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Also shelved %d dependent item(s), now hidden from normal listing:\n", len(dependents))
	for _, d := range dependents {
		fmt.Fprintf(w, "  %s: %s\n", d.ID, d.Title)
	}
	fmt.Fprintln(w, "Restore them with 'mg unshelve <id>'.")
}

// reportShelveSkipped names the tagged items a guard refused. It goes to
// STDERR and carries the reason and the remedy for each, mirroring the archive
// sweep's report: the two guards are fixed differently — remove a tag once a
// person has answered, file a tracker for a recommendation — so a bare list of
// ids under one summary line would make the operator guess.
func reportShelveSkipped(cmd *cobra.Command, skipped []workitem.SkippedItem) {
	if len(skipped) == 0 {
		return
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "Skipped %d guarded item(s):\n", len(skipped))
	for _, s := range skipped {
		fmt.Fprintf(w, "  %s: %s\n", s.Item.ID, s.Item.Title)
		fmt.Fprintf(w, "    %s\n", s.Reason)
		if hint := skipHint(s.Reason); hint != "" {
			fmt.Fprintf(w, "    %s\n", hint)
		}
	}
	fmt.Fprintln(w, "They stay where they are, where they remain visible.")
}

func init() {
	shelveCmd.Flags().StringVar(&shelveTag, "tag", "", "shelve all items with this tag")
	shelveCmd.Flags().StringVar(&shelveOverride, "override", "", "why this item is safe to shelve despite a guard; recorded as a work.shelve_forced event naming the guard and this reason (ID form only)")
}

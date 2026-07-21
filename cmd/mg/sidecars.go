package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var sidecarsJSON bool

var sidecarsCmd = &cobra.Command{
	Use:   "sidecars",
	Short: "Report result sidecars that are not beside their work item",
	Long: `Report every <id>.result.json in the store that is not sitting beside its
item's .md file.

Why this exists: reading a result by glob is unsafe. The lifecycle directories
are scanned in ALPHABETICAL order, so

    ls ~/.macguffin/work/*/mg-560d.result.json | head -1

returns a stray copy in available/ or claimed/ AHEAD of the real one in done/,
and the reader cannot tell. To read one item's result, ask where the item is
and use that explicit path:

    mg show <id> --json | jq -r .status     # then <status>/<id>.result.json

DISPOSITION IS NOT MECHANICAL. A stray whose bytes match the authoritative copy
is redundant and safe to delete. A stray whose bytes DIFFER is either a
superseded draft or the last surviving copy of content the authoritative file
overwrote — this store has held both — so a differing stray is reported for a
human to judge and is never presented as safe to remove.

Exit status is 0 whether or not strays are found; this is a report, not a gate.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		strays, err := workitem.FindStraySidecars(root)
		if err != nil {
			return err
		}
		sort.Slice(strays, func(i, j int) bool {
			if strays[i].ID != strays[j].ID {
				return strays[i].ID < strays[j].ID
			}
			return strays[i].Path < strays[j].Path
		})

		if sidecarsJSON {
			// Machine-readable contract on stdout; keep notices on stderr.
			out := make([]map[string]any, 0, len(strays))
			for _, s := range strays {
				out = append(out, map[string]any{
					"id":                   s.ID,
					"path":                 s.Path,
					"location":             s.Location,
					"item_status":          s.ItemStatus,
					"authoritative":        s.Authoritative,
					"authoritative_exists": s.AuthoritativeExists,
					"differs":              s.Differs,
					"redundant":            s.Redundant(),
				})
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		if len(strays) == 0 {
			fmt.Println("No stray result sidecars.")
			return nil
		}

		fmt.Printf("%d stray result sidecar(s):\n\n", len(strays))
		for _, s := range strays {
			fmt.Printf("  %s\n", s.Path)
			switch {
			case s.ItemStatus == "":
				fmt.Printf("      item does not resolve — this is the only record; DO NOT DELETE\n")
			case !s.AuthoritativeExists:
				fmt.Printf("      item is %s, but %s is MISSING\n", s.ItemStatus, s.Authoritative)
				fmt.Printf("      the only copy of this result; DO NOT DELETE\n")
			case s.Differs:
				fmt.Printf("      item is %s; differs from %s\n", s.ItemStatus, s.Authoritative)
				fmt.Printf("      superseded draft OR surviving content — compare both before deciding\n")
			default:
				fmt.Printf("      item is %s; identical to %s\n", s.ItemStatus, s.Authoritative)
				fmt.Printf("      redundant, safe to delete\n")
			}
			fmt.Println()
		}
		fmt.Println("Read a result from the item's own status directory, never from a glob:")
		fmt.Println("    mg show <id> --json | jq -r .status")
		return nil
	},
}

func init() {
	sidecarsCmd.Flags().BoolVar(&sidecarsJSON, "json", false, "Emit JSON")
}

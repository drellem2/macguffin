package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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

DISPOSITION IS NOT MECHANICAL, so each stray is classified by CONTENT and the
verdict names what to do:

    identical   same bytes; redundant, safe to delete
    equivalent  same JSON, re-serialised (key order/whitespace); safe to delete
    subset      one side's keys contain the other's and they agree on the
                overlap; names the SUPERSET and the keys only it holds
    conflict    each side holds something the other lacks, or they disagree on
                a shared key; names every key in disagreement
    opaque      bytes differ and a side is not a JSON object; compare by hand
    unknown     a file could not be READ — this is a failed probe, not a
                finding, and it is never reported as a difference

CLASSIFICATION IS ADVICE; RECONCILIATION STAYS DELIBERATE. Nothing here merges
or deletes anything. A subset verdict is not an instruction to keep the
superset: the subset relation can equally be the artefact of one side having
been TRUNCATED, and only a reader who opens both files can tell those apart.

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
					"differs":              s.Differs(),
					"redundant":            s.Redundant(),
					"relation":             string(s.Comparison.Relation),
					"superset":             s.Comparison.Superset,
					"differing_keys":       keysOrEmpty(s.Comparison.Keys),
					"note":                 s.Comparison.Note,
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
			default:
				fmt.Printf("      item is %s; vs %s\n", s.ItemStatus, s.Authoritative)
				for _, line := range verdictLines(s.Comparison) {
					fmt.Printf("      %s\n", line)
				}
			}
			fmt.Println()
		}
		fmt.Println("Read a result from the item's own status directory, never from a glob:")
		fmt.Println("    mg show <id> --json | jq -r .status")
		return nil
	},
}

// keysOrEmpty keeps the JSON contract stable: differing_keys is always an
// array, never null, so a consumer can iterate it without a nil check.
func keysOrEmpty(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

// verdictLines renders a comparison as the verdict plus the action it implies.
// Every branch says what to DO — a classification an operator has to interpret
// is the binary "differs" again with more words.
func verdictLines(c workitem.SidecarComparison) []string {
	switch c.Relation {
	case workitem.RelationIdentical:
		return []string{"IDENTICAL — same bytes", "redundant, safe to delete"}
	case workitem.RelationEquivalent:
		return []string{
			"EQUIVALENT — same JSON, different key order or whitespace",
			"redundant, safe to delete; no human judgement needed",
		}
	case workitem.RelationSubset:
		super, other := "authoritative copy", "stray"
		if c.Superset == workitem.SideStray {
			super, other = "stray", "authoritative copy"
		}
		return []string{
			fmt.Sprintf("SUBSET — the %s is the superset; it agrees on every shared key", super),
			fmt.Sprintf("only in the %s: %s", super, strings.Join(c.Keys, ", ")),
			fmt.Sprintf("mechanically mergeable: keeping the %s loses nothing the %s holds", super, other),
			"but confirm the subset is not a TRUNCATION before discarding it",
		}
	case workitem.RelationConflict:
		return []string{
			"CONFLICT — each copy holds content the other lacks, or they disagree",
			fmt.Sprintf("keys in disagreement: %s", strings.Join(c.Keys, ", ")),
			"needs a human: a wrong merge loses data silently",
		}
	case workitem.RelationOpaque:
		return []string{
			fmt.Sprintf("OPAQUE — contents differ but cannot be compared by key (%s)", c.Note),
			"needs a human: compare both files by hand",
		}
	case workitem.RelationUnknown:
		return []string{
			fmt.Sprintf("UNKNOWN — could not compare (%s)", c.Note),
			"this is a FAILED PROBE, not a finding; fix the read before judging",
		}
	default:
		return []string{fmt.Sprintf("UNKNOWN — unclassified relation %q", c.Relation)}
	}
}

func init() {
	sidecarsCmd.Flags().BoolVar(&sidecarsJSON, "json", false, "Emit JSON")
}

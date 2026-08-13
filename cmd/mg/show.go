package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/spend"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	showJSON     bool
	showBodyHash bool
	showSections bool
)

// showJSONItem is the stable on-the-wire shape for `mg show ID --json`: the
// full work item as ONE JSON object. It embeds listJSONItem so the fields
// shared with `mg list --json` keep identical names (the frozen, additive-only
// CLI contract), and adds the single-item extras that the list view omits:
// creator, the full body, and budget/spend. Field names are FROZEN — new
// fields may be added, but existing ones are never renamed or removed.
type showJSONItem struct {
	listJSONItem
	Creator string `json:"creator"`
	Body    string `json:"body"`
	// BodyHash is the SHA-256 of the Body field's exact bytes — the version
	// token 'mg edit --if-unchanged' checks against. It rides alongside the
	// body so a reader gets the content and its version in ONE read: any gap
	// between fetching them is a window in which the pair can disagree, which
	// is the same defect at a smaller scale.
	BodyHash string `json:"body_hash"`
	Budget   *int   `json:"budget"` // null when unset
	Spent    int    `json:"spent"`  // total tokens recorded against this item
	// ResultPath is the absolute path of the item's result sidecar, or null
	// when the item has recorded no result. Result is that sidecar decoded.
	//
	// These exist because their absence is what sent every reader to a glob:
	// the object had 21 keys and not one of them was the verdict, so "what did
	// this item conclude" was answerable only by constructing a path by hand —
	// and the hand-built path was `work/*/<id>.result.json`, which is one level
	// deep and therefore blind to the month-nested archive holding most of the
	// store's sidecars. A field cannot be globbed wrong.
	//
	// result_path non-null with result null means the file is there and its
	// bytes are not valid JSON — which is a third state, and distinguishing it
	// from "no result" is the entire discipline this pair is for.
	ResultPath *string         `json:"result_path"`
	Result     json.RawMessage `json:"result"`
}

var showCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show a work item by ID",
	Long: `Show a work item by ID.

When two archived twins share a short ID across different month partitions the
bare ID is ambiguous. Disambiguate with an @partition qualifier:

  mg show mg-4fa7@2026-04   # the twin archived in partition 2026-04

A long-lived item accumulates dated sections, and its live spec then sits in the
same undifferentiated body as the history that superseded it. --sections prints
an index of those dated headings with the body line each sits on, and a plain
'mg show' says so in one line once there are three or more:

  mg show mg-49b1 --sections   # a jump table, in document order

Neither says which section is CURRENT — that is a judgement someone made, and it
is recorded by striking the superseded section where it sits, not by being the
bottom entry in a list.

'Creator' is whoever ran 'mg new' (MG_ACTOR, else POGO_AGENT_NAME, else the OS
user). It is self-asserted and forgeable, so it is attribution and not
authentication. On items filed before mg-ddf4 it records the unix user, which
was the SAME STRING for every agent on this box — read 'daniel' on an older
item as "unknown", not as Daniel.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		// One resolve, not two: reading the body and the status separately
		// let `show` render one item under another item's status. The same
		// resolve carries the item's LOCATION, which is where its result
		// sidecar lives — re-deriving that later is a second chance to derive
		// it wrong, and deriving it wrong is exactly mg-6bc9.
		item, match, err := workitem.ReadWithMatch(root, args[0])
		if err != nil {
			return err
		}
		status := match.Status

		if showJSON && showBodyHash {
			return mgerr.Usage("mutually_exclusive_flags",
				"cannot use both --json and --body-hash",
				"--json already carries the hash in its body_hash field")
		}
		if showSections && showBodyHash {
			return mgerr.Usage("mutually_exclusive_flags",
				"cannot use both --sections and --body-hash",
				"--body-hash prints one line and nothing else; run the two commands separately")
		}

		// --sections replaces the whole view with the index: a reader who asked
		// where the dated headings are is not also asking to scroll the body
		// they were trying to navigate. --json is honoured rather than ignored,
		// because a caller who passed it will parse whatever comes back.
		if showSections {
			secs := workitem.DatedSections(item.Body)
			if showJSON {
				return writeSectionsJSON(item, secs)
			}
			renderSections(os.Stdout, item, secs, resolveListWidth(os.Stdout, false))
			return nil
		}

		// --body-hash prints the bare hash and nothing else, so the safe write
		// is one command rather than a pipe through jq:
		//   mg edit ID --if-unchanged="$(mg show ID --body-hash)" --body-file …
		if showBodyHash {
			fmt.Println(workitem.BodyHash(item.Body))
			return nil
		}

		if showJSON {
			return writeShowJSON(root, item, match)
		}

		fmt.Printf("%-10s %s\n", "ID:", item.ID)
		fmt.Printf("%-10s %s\n", "Type:", item.Type)
		fmt.Printf("%-10s %s\n", "Status:", status)
		fmt.Printf("%-10s %s\n", "Created:", item.Created.Format("2006-01-02 15:04:05Z"))
		fmt.Printf("%-10s %s\n", "Creator:", item.Creator)
		if item.Assignee != "" {
			fmt.Printf("%-10s %s\n", "Assignee:", item.Assignee)
		}
		if item.Priority != "" {
			fmt.Printf("%-10s %s\n", "Priority:", item.Priority)
		}
		if item.Branch != "" {
			fmt.Printf("%-10s %s\n", "Branch:", item.Branch)
		}
		if item.Budget != nil {
			fmt.Printf("%-10s %s tokens\n", "Budget:", formatThousands(*item.Budget))
			if recs, err := spend.ReadItem(root, item.ID); err == nil && len(recs) > 0 {
				spent := 0
				for _, r := range recs {
					spent += r.Input + r.CacheRead + r.CacheCreate + r.Output
				}
				line := fmt.Sprintf("%s tokens (%d%% of budget)", formatThousands(spent), pctOfBudget(spent, *item.Budget))
				if *item.Budget > 0 && spent > *item.Budget {
					line += " ⚠"
				}
				fmt.Printf("%-10s %s\n", "Spent:", line)
			}
		}
		if len(item.Tags) > 0 {
			fmt.Printf("%-10s %s\n", "Tags:", strings.Join(item.Tags, ", "))
		}
		if len(item.Depends) > 0 {
			fmt.Printf("%-10s %s\n", "Depends:", strings.Join(item.Depends, ", "))
		}

		// The successor: tag is already inside the Tags line above, so these
		// lines are not here to reveal the id — they are here to RESOLVE it.
		// `mg done` prints "Successor mg-4b01 (available): build the thing" at
		// the moment of the link, and that line was the only place a
		// wrong-but-real successor was ever visible; it scrolled away with the
		// terminal that printed it. Reprinting it on demand is what makes the
		// chain walkable later, which is the complaint mg-3386 actually had:
		// given a closed item, what carried its remainder forward, and where has
		// that got to since?
		//
		// A tag pointing at nothing prints UNRESOLVED rather than being skipped.
		// The guards refuse a dangling successor at close time, so a dangling one
		// HERE means the target was deleted afterwards — a link that has rotted
		// since it was checked, which is exactly the state worth showing and the
		// one a bare tag list cannot distinguish from a healthy link.
		printLinks("Successor:", workitem.DescribeSuccessors(root, item))
		printLinks("Predecessor:", workitem.DescribePredecessors(root, item))
		if item.SnoozeRaw != "" {
			// A gate you cannot see is a gate nobody audits. Malformed values
			// are shown as they are stored and named as malformed, because a
			// snooze mg cannot parse is one that never opens.
			switch {
			case item.SnoozeMalformed():
				fmt.Printf("%-10s %s  ⚠ not an RFC3339 timestamp — this gate can never open\n", "Snooze:", item.SnoozeRaw)
			case item.Snoozed(time.Now().UTC()):
				fmt.Printf("%-10s %s (in %s)\n", "Snooze:", item.SnoozeRaw, workitem.HumanUntil(time.Until(item.Snooze)))
			default:
				// An elapsed gate on a PENDING item is about to open — this very
				// command is promoting it on another goroutine, and the read that
				// produced this line may simply have won the race. On any other
				// status the gate is spent litter, not a promise.
				if status == "pending" {
					fmt.Printf("%-10s %s (elapsed; being released now — `mg show` again)\n", "Snooze:", item.SnoozeRaw)
				} else {
					fmt.Printf("%-10s %s (elapsed and spent; `mg schedule` clears it)\n", "Snooze:", item.SnoozeRaw)
				}
			}
		}
		if item.Repo != "" {
			fmt.Printf("%-10s %s\n", "Repo:", item.Repo)
		}

		// The resolved sidecar path, printed on the surface people actually
		// read. A reader who can see the path has no reason to construct one,
		// and constructing one is where the glob comes from. An unreadable
		// store says so rather than printing nothing: a missing line and a line
		// that could not be produced are the two states this must not merge.
		if sc, err := workitem.SidecarOf(match); err != nil {
			fmt.Printf("%-10s ⚠ could not read the result sidecar: %s\n", "Result:", err)
		} else if sc.Exists {
			fmt.Printf("%-10s %s\n", "Result:", sc.Path)
		}

		fmt.Printf("%-10s %s\n", "Title:", item.Title)

		// Above the body, not below it: a reader who learns the body is a log
		// after reading it has learned it too late, which is the whole defect.
		if banner := sectionsBannerLine(item, workitem.DatedSections(item.Body)); banner != "" {
			fmt.Println(banner)
		}

		if item.Body != "" {
			fmt.Printf("\n%s", item.Body)
		}

		return nil
	},
}

func init() {
	showCmd.Flags().BoolVar(&showJSON, "json", false, "emit the full work item as one JSON object")
	showCmd.Flags().BoolVar(&showBodyHash, "body-hash", false, "print only the body's SHA-256, for 'mg edit --if-unchanged'")
	showCmd.Flags().BoolVar(&showSections, "sections", false, "print an index of the body's dated headings instead of the item")
}

// writeShowJSON marshals the work item as a single JSON object on stdout.
// Spend is summed from the per-item spend records (0 when none are recorded),
// mirroring the human view's Spent line.
func writeShowJSON(root string, item *workitem.Item, match workitem.Match) error {
	spent := 0
	if recs, err := spend.ReadItem(root, item.ID); err == nil {
		for _, r := range recs {
			spent += r.Input + r.CacheRead + r.CacheCreate + r.Output
		}
	}
	// An unreadable store must fail the command rather than render result_path
	// null: "there is no result" and "I could not look" are the two states this
	// pair exists to keep apart, and collapsing them here would reintroduce the
	// bug one layer up.
	sc, result, ok, err := workitem.SidecarResultOf(match)
	if err != nil {
		return err
	}
	out := showJSONItem{
		listJSONItem: toJSONItem(item, match.Status),
		Creator:      item.Creator,
		Body:         item.Body,
		BodyHash:     workitem.BodyHash(item.Body),
		Budget:       item.Budget,
		Spent:        spent,
	}
	if sc.Exists {
		path := sc.Path
		out.ResultPath = &path
	}
	if ok {
		out.Result = result
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printLinks renders resolved successor/predecessor links under a label, one
// per line, or nothing at all when there are none. The overwhelming majority of
// items carry no links and a label printed for them is noise — the same reason
// `mg done` prints no successor line on a completion that has none.
//
// "Predecessor:" is 12 characters — the longest label mg prints, and two wider
// than the %-10s column the fixed fields above use. Both link lines take that
// width so they align with EACH OTHER; the alternative is each line choosing
// its own and the pair stepping against itself, which is worse than the pair
// sitting two columns right of the block above.
func printLinks(label string, refs []workitem.SuccessorRef) {
	for _, r := range refs {
		if r.Status == "" {
			fmt.Printf("%-12s %s  ⚠ UNRESOLVED — nothing by that id exists now\n", label, r.ID)
			continue
		}
		fmt.Printf("%-12s %s (%s): %s\n", label, r.ID, r.Status, r.Title)
	}
}

// pctOfBudget rounds spent/budget to a whole percentage. A zero budget is
// reported as 0% to avoid divide-by-zero — the ⚠ marker still fires when spent>0.
func pctOfBudget(spent, budget int) int {
	if budget <= 0 {
		return 0
	}
	return (spent * 100) / budget
}

// formatThousands renders an integer with comma separators (e.g. 200000 → "200,000").
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

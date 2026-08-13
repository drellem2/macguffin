package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	sidecarPathOnly bool
	sidecarJSON     bool
)

var sidecarCmd = &cobra.Command{
	Use:   "sidecar ID",
	Short: "Print an item's result sidecar (or --path, where it is)",
	Long: `Print the result sidecar recorded for one work item.

This is the command to use instead of building the path yourself. It RESOLVES
the item and reads the sidecar beside that item's .md — it never scans, never
matches a pattern, and never returns a candidate list.

    mg sidecar mg-6ff4              # the result, on stdout
    mg sidecar mg-6ff4 --path       # the absolute path, nothing else
    mg sidecar mg-6ff4 --json       # {id,status,partition,path,result}

DO NOT GLOB FOR A SIDECAR. The shape everyone reaches for,

    ls ~/.macguffin/work/*/mg-6ff4.result.json

is a ONE-LEVEL pattern, and the archive is nested by month —
work/archive/2026-08/mg-6ff4.result.json. It therefore cannot match an ARCHIVED
sidecar at all, and the archive holds the overwhelming majority of them. The
glob does not fail into your result; it fails beside it, and what lands in your
result is the empty set. That is what makes it dangerous: on 2026-08-13 two
agents ninety minutes apart published "no sidecar" for items that had one.

The three outcomes are kept distinct so no caller can render two of them the
same way:

    exit 0   the sidecar was read; its bytes (or path) are on stdout
    exit 3   no_such_item / no_sidecar — the lookup SUCCEEDED and the answer is
             "there is none". Nothing is written to stdout.
    exit 1   io_error — the store could not be read. This is a failed probe,
             NOT an absent result, and must never be reported as one.

An item is resolved with the same rules as 'mg show': an @partition qualifier
selects between archived twins, and an ambiguous id is refused rather than
guessed.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		if sidecarPathOnly && sidecarJSON {
			return mgerr.Usage("mutually_exclusive_flags",
				"cannot use both --path and --json",
				"--json already carries the location in its path field")
		}

		if sidecarJSON {
			return writeSidecarJSON(root, args[0])
		}

		// Both remaining modes must be silent on stdout when there is no
		// sidecar, so both go through a lookup that errors rather than one that
		// returns an empty value.
		sc, data, err := workitem.ReadSidecar(root, args[0])
		if err != nil {
			return err
		}
		if sidecarPathOnly {
			fmt.Println(sc.Path)
			return nil
		}
		// Written verbatim: a result is whatever the agent recorded, and
		// re-serialising it here would silently normalise a record mg does not
		// own. A trailing newline is added only when the file lacks one.
		os.Stdout.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			fmt.Println()
		}
		return nil
	},
}

// sidecarJSONOut is the machine-readable shape of `mg sidecar ID --json`.
// Field names are FROZEN and additive-only, like every other --json contract.
//
// It is emitted ONLY when the sidecar exists. An absent one produces no stdout
// at all and the frozen JSON error object on stderr
// ({"error":{"code":"no_sidecar",...}}), which is where every other mg command
// puts its refusals — a parser that reads stdout for a result and the exit code
// for whether there is one is never handed a document that says "nothing".
//
// Result is null in exactly one case: the file exists but its bytes are not
// valid JSON. Path is still populated then, which is the whole answer a reader
// needs to go open it.
type sidecarJSONOut struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Partition string          `json:"partition"` // "" unless archived
	Path      string          `json:"path"`      // absolute path to the sidecar
	Result    json.RawMessage `json:"result"`    // null when the file is not valid JSON
}

func writeSidecarJSON(root, id string) error {
	sc, result, ok, err := workitem.SidecarResult(root, id)
	if err != nil {
		return err
	}
	if !sc.Exists {
		return workitem.ErrNoSidecar(sc)
	}
	out := sidecarJSONOut{
		ID:        sc.ID,
		Status:    sc.Status,
		Partition: sc.Partition,
		Path:      sc.Path,
	}
	if ok {
		out.Result = result
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func init() {
	sidecarCmd.Flags().BoolVar(&sidecarPathOnly, "path", false,
		"print the resolved absolute path instead of the contents")
	sidecarCmd.Flags().BoolVar(&sidecarJSON, "json", false,
		"emit id, status, path, exists and the parsed result as one JSON object")
}

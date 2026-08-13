package main

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// schemaVersion is the version of the `mg schema` document shape itself. It is
// bumped only on a BREAKING change to the shape (a renamed or removed field).
// Additive changes (new fields) do NOT bump it — see the contract note below.
const schemaVersion = 1

// --- `mg schema` public contract (gh drellem2/pogo#56) ---------------------
//
// `mg schema` dumps the entire cobra command tree as a SINGLE JSON document so
// agent consumers can discover mg's commands, flags, and per-command side-effect
// hints without scraping --help text. The JSON shape below is a PUBLIC CONTRACT,
// following the same discipline as the `mg list --json` / `mg mail --json`
// surfaces (see cmd/mg/list.go, cmd/mg/mail.go):
//
//   - Field names and JSON types are FROZEN. They are never renamed or removed.
//   - The contract is ADDITIVE-ONLY: new fields may be added in a later release;
//     a consumer must ignore unknown fields rather than fail on them.
//   - A breaking change (should it ever be unavoidable) bumps schema_version.
//   - The output is ONE JSON document describing the whole tree (not NDJSON),
//     because the tree is a single nested structure, not a collection of peers.
//
// schema_version gates the SHAPE only. Two things it deliberately does NOT gate,
// which consumers must therefore IGNORE when diffing the surface for stability:
//
//   - The top-level `version` field is BUILD METADATA (the mg binary's version
//     string). It changes every release and says nothing about the command
//     surface; a surface diff must exclude it.
//   - The per-command `mutates` / `idempotent` hints are ADVISORY side-effect
//     annotations for agent planners (e.g. "is this safe to retry?"). Their
//     VALUES may be refined between releases WITHOUT a schema_version bump. The
//     SHAPE (that the fields exist and are bools) is frozen; the values are not.

// schemaFlag is the frozen JSON shape for one flag on a command.
type schemaFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand"`
	Usage     string `json:"usage"`
	Type      string `json:"type"`
	Default   string `json:"default"`
}

// schemaCommand is the frozen JSON shape for one command node in the tree.
// Commands nests recursively; a leaf command has an empty commands array.
type schemaCommand struct {
	Name       string          `json:"name"`       // final path token, e.g. "send"
	Path       string          `json:"path"`       // full path, e.g. "mg mail send"
	Use        string          `json:"use"`        // cobra Use string (usage synopsis)
	Short      string          `json:"short"`      // one-line description
	Aliases    []string        `json:"aliases"`    // alternate command names ([] if none)
	Mutates    bool            `json:"mutates"`    // may change persistent state
	Idempotent bool            `json:"idempotent"` // re-run converges (see commandEffects)
	Flags      []schemaFlag    `json:"flags"`      // flags accepted by this command
	Commands   []schemaCommand `json:"commands"`   // subcommands ([] if leaf)
}

// schemaDoc is the frozen top-level JSON document emitted by `mg schema`.
type schemaDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Tool          string        `json:"tool"`    // always "mg"
	Version       string        `json:"version"` // the mg build version string
	Command       schemaCommand `json:"command"` // the root command tree
}

// commandEffect classifies a command's side effects for the schema.
//
//   - mutates:    the command may change persistent state (work items, mailbox,
//     workspace, git). Read-only/query commands are false.
//   - idempotent: re-running with identical arguments converges to the same
//     state rather than compounding the effect. Only meaningful when mutates is
//     true; read-only commands are trivially idempotent (true).
//
// When idempotency is FLAG-DEPENDENT — some invocations converge, others
// accumulate (e.g. `mg edit --title` is idempotent but `mg edit --add-tags`
// accumulates) — the bool takes the CONSERVATIVE value (false), so a consumer
// never treats a command as safe-to-retry when some flag combination isn't.
//
// These two bools are ADVISORY, not part of the frozen shape: their VALUES may
// change between releases WITHOUT a schema_version bump (schema_version gates
// the document SHAPE only). Consumers must not diff them for contract stability.
type commandEffect struct {
	mutates    bool
	idempotent bool
}

// commandEffects maps a command's full path to its side-effect classification.
// Every command in the tree MUST have an entry (TestSchema_AllCommandsClassified
// enforces this); effectFor falls back to a conservative mutating/non-idempotent
// default only as a runtime safety net. These classifications are provisional
// and flagged for architect review — see the contract note above.
var commandEffects = map[string]commandEffect{
	"mg":          {mutates: false, idempotent: true}, // root: prints usage
	"mg schema":   {mutates: false, idempotent: true},
	"mg sidecar":  {mutates: false, idempotent: true},
	"mg sidecars": {mutates: false, idempotent: true},
	"mg version":  {mutates: false, idempotent: true},
	"mg show":     {mutates: false, idempotent: true},
	"mg list":     {mutates: false, idempotent: true},
	"mg log":      {mutates: false, idempotent: true},
	"mg flow":     {mutates: false, idempotent: true},
	"mg spend":    {mutates: false, idempotent: true},
	"mg init":     {mutates: true, idempotent: true}, // ensures workspace exists
	"mg new":      {mutates: true, idempotent: false},
	"mg claim":    {mutates: true, idempotent: false},
	"mg unclaim":  {mutates: true, idempotent: false},

	// Re-stamping the PID already recorded is a no-op that exits 0, so a retry
	// neither errors nor compounds — which is what a planner reads this hint
	// for. (A bare re-run from a *different* process stamps that process's PID;
	// the state differs, but "the caller's PID is on the claim" converges.)
	"mg reclaim": {mutates: true, idempotent: true},

	"mg done": {mutates: true, idempotent: false},
	"mg edit": {mutates: true, idempotent: false}, // --add-tags/--rm-tags accumulate; flag-dependent idempotency takes the conservative false

	// Re-running converges on the body, but each run SAVES the body it
	// overwrites, so a second run consumes a backup slot and the second restore
	// is a no-op that pushed a real prior version one closer to the prune
	// bound. The conservative false is the honest classification.
	"mg restore-body": {mutates: true, idempotent: false},

	"mg assign":    {mutates: true, idempotent: true},
	"mg reopen":    {mutates: true, idempotent: false},
	"mg shelve":    {mutates: true, idempotent: false},
	"mg unshelve":  {mutates: true, idempotent: false},
	"mg snooze":    {mutates: true, idempotent: false}, // --for is relative, so a re-run moves the wake time
	"mg unsnooze":  {mutates: true, idempotent: true},  // unsnoozing an unsnoozed item refuses, changing nothing
	"mg archive":   {mutates: true, idempotent: true},  // sweep: re-run archives nothing new
	"mg unarchive": {mutates: true, idempotent: false},
	"mg snapshot":  {mutates: true, idempotent: false}, // creates a git snapshot
	"mg schedule":  {mutates: true, idempotent: true},  // promotes ready items; re-run is a no-op

	"mg event":        {mutates: false, idempotent: true}, // group command (help only)
	"mg event append": {mutates: true, idempotent: false},
	"mg event list":   {mutates: false, idempotent: true},

	"mg mail":         {mutates: false, idempotent: true}, // group command (help only)
	"mg mail send":    {mutates: true, idempotent: false},
	"mg mail reply":   {mutates: true, idempotent: false}, // delivers a new message, like send
	"mg mail list":    {mutates: false, idempotent: true},
	"mg mail read":    {mutates: true, idempotent: true}, // marks read; re-read is a no-op
	"mg mail archive": {mutates: true, idempotent: true}, // archiving an archived msg is a no-op
	"mg mail migrate": {mutates: true, idempotent: true}, // merges stray mailboxes; re-run finds none

	// Sweep: a re-run at the same instant reclaims nothing new, because every
	// copy it would select has already left new/ and cur/. (Time still advances,
	// so
	// a LATER run reclaims whatever crossed the window since — that is the
	// window doing its job, not the command failing to converge.)
	"mg mail reclaim": {mutates: true, idempotent: true},

	"mg mail register": {mutates: true, idempotent: true}, // creates an empty maildir; re-registering is a no-op
}

// effectFor returns the side-effect classification for a command path. Unknown
// paths default to the conservative {mutates:true, idempotent:false} (assume it
// changes state and is unsafe to retry) so a newly-added but unclassified
// command is never advertised as safe; the completeness test catches the gap.
func effectFor(path string) commandEffect {
	if e, ok := commandEffects[path]; ok {
		return e
	}
	return commandEffect{mutates: true, idempotent: false}
}

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Dump the command tree as JSON (stable agent-facing contract)",
	Long: `Dump the entire mg command tree as a single JSON document.

Intended for agent/tooling consumers that need to discover mg's commands, flags,
and per-command side-effect hints without scraping --help text. The output is
one JSON object describing the root command and its subcommands recursively;
each command carries its name, path, use synopsis, short description, aliases,
flags, and 'mutates'/'idempotent' side-effect hints.

The JSON shape is a frozen, additive-only public contract: field names are
never renamed or removed, new fields may be added, and a breaking change bumps
"schema_version".

schema_version gates the SHAPE only. When diffing the command surface for
stability, IGNORE two things it does not gate: the top-level "version" field is
build metadata (the mg binary version, changes every release), and each
command's "mutates"/"idempotent" hints are ADVISORY — their values may change
between releases without a schema_version bump.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc := schemaDoc{
			SchemaVersion: schemaVersion,
			Tool:          "mg",
			Version:       versionString(),
			Command:       buildSchemaCommand(rootCmd),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	},
}

// buildSchemaCommand recursively converts a cobra command into its schema shape.
// Children are sorted by name and the cobra-generated "help"/"completion"
// commands and any hidden commands are skipped, so the output is deterministic
// and describes only mg's own surface.
func buildSchemaCommand(c *cobra.Command) schemaCommand {
	eff := effectFor(c.CommandPath())

	aliases := c.Aliases
	if aliases == nil {
		aliases = []string{}
	}

	sc := schemaCommand{
		Name:       c.Name(),
		Path:       c.CommandPath(),
		Use:        c.Use,
		Short:      c.Short,
		Aliases:    aliases,
		Mutates:    eff.mutates,
		Idempotent: eff.idempotent,
		Flags:      []schemaFlag{},
		Commands:   []schemaCommand{},
	}

	// Emit this command's own flags, in pflag's lexical order (deterministic).
	// The universal cobra built-ins --help and --version are omitted: they are
	// not part of mg's declared surface and their initialization is order-
	// dependent, which would make the output unstable.
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Name == "version" {
			return
		}
		sc.Flags = append(sc.Flags, schemaFlag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Usage:     f.Usage,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
		})
	})

	children := make([]*cobra.Command, len(c.Commands()))
	copy(children, c.Commands())
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, cc := range children {
		if cc.Hidden || cc.Name() == "help" || cc.Name() == "completion" {
			continue
		}
		sc.Commands = append(sc.Commands, buildSchemaCommand(cc))
	}

	return sc
}

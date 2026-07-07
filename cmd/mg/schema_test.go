package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestSchema_EmitsValidJSON is the gh drellem2/pogo#56 core: `mg schema` emits a
// single well-formed JSON document with the frozen top-level fields, the root
// command, and its subcommands.
func TestSchema_EmitsValidJSON(t *testing.T) {
	bin := buildBinary(t)
	// schema does not touch the workspace, but run under a temp HOME anyway so
	// the test never depends on the developer's real workspace.
	env := emEnv(t.TempDir())

	out := emOK(t, bin, env, "schema")

	var doc schemaDoc
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields() // the emitted doc must match the frozen shape exactly
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("schema output is not a valid single schemaDoc: %v\n%s", err, out)
	}
	// Exactly one JSON document — nothing trailing.
	if dec.More() {
		t.Errorf("schema emitted more than one JSON document")
	}

	if doc.SchemaVersion != schemaVersion {
		t.Errorf("schema_version = %d, want %d", doc.SchemaVersion, schemaVersion)
	}
	if doc.Tool != "mg" {
		t.Errorf("tool = %q, want mg", doc.Tool)
	}
	if doc.Version == "" {
		t.Error("version is empty")
	}
	if doc.Command.Name != "mg" || doc.Command.Path != "mg" {
		t.Errorf("root command name/path = %q/%q, want mg/mg", doc.Command.Name, doc.Command.Path)
	}
	if len(doc.Command.Commands) == 0 {
		t.Fatal("root command has no subcommands in schema")
	}

	// A few known commands must appear with the expected side-effect hints and
	// flags, so the contract is exercised end-to-end.
	byPath := map[string]schemaCommand{}
	var walk func(c schemaCommand)
	walk = func(c schemaCommand) {
		byPath[c.Path] = c
		for _, cc := range c.Commands {
			walk(cc)
		}
	}
	walk(doc.Command)

	checks := []struct {
		path             string
		mutates, idempot bool
	}{
		{"mg list", false, true},
		{"mg new", true, false},
		{"mg edit", true, false}, // flag-dependent idempotency -> conservative false (--add-tags/--rm-tags accumulate)
		{"mg schema", false, true},
		{"mg mail send", true, false},
		{"mg mail read", true, true},
	}
	for _, c := range checks {
		got, ok := byPath[c.path]
		if !ok {
			t.Errorf("schema missing expected command %q", c.path)
			continue
		}
		if got.Mutates != c.mutates || got.Idempotent != c.idempot {
			t.Errorf("%s: mutates/idempotent = %v/%v, want %v/%v", c.path, got.Mutates, got.Idempotent, c.mutates, c.idempot)
		}
	}

	// The cobra built-ins must be excluded from the tree.
	for _, excluded := range []string{"mg help", "mg completion"} {
		if _, ok := byPath[excluded]; ok {
			t.Errorf("schema should not include cobra built-in %q", excluded)
		}
	}

	// `mg list --status` flag must be present and carry its usage — sanity that
	// flags are emitted with real metadata.
	list := byPath["mg list"]
	foundStatus := false
	for _, f := range list.Flags {
		if f.Name == "status" {
			foundStatus = true
			if f.Type != "string" {
				t.Errorf("mg list --status type = %q, want string", f.Type)
			}
			if f.Usage == "" {
				t.Error("mg list --status usage is empty in schema")
			}
		}
		if f.Name == "help" || f.Name == "version" {
			t.Errorf("schema must omit built-in flag %q", f.Name)
		}
	}
	if !foundStatus {
		t.Error("mg list schema is missing the --status flag")
	}
}

// TestSchema_Stable is the #56 stability requirement: repeated invocations emit
// byte-identical output, so consumers can diff the contract across builds.
func TestSchema_Stable(t *testing.T) {
	bin := buildBinary(t)
	env := emEnv(t.TempDir())
	first := emOK(t, bin, env, "schema")
	second := emOK(t, bin, env, "schema")
	if first != second {
		t.Errorf("schema output is not stable across runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestSchema_AllCommandsClassified guards the completeness of commandEffects: it
// walks the real in-process command tree and asserts every command (except the
// cobra built-ins the schema skips) has an explicit entry — no reliance on the
// conservative effectFor default. A newly-added command that forgets a
// classification fails here instead of silently emitting the fallback.
func TestSchema_AllCommandsClassified(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			return
		}
		if _, ok := commandEffects[c.CommandPath()]; !ok {
			t.Errorf("command %q has no entry in commandEffects (add its mutates/idempotent classification)", c.CommandPath())
		}
		for _, cc := range c.Commands() {
			walk(cc)
		}
	}
	walk(rootCmd)
}

package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestTagFlag_CanonicalAlignment is the gh drellem2/pogo#60 regression: the
// `new` and `edit` commands must agree on which of --tag/--tags is canonical.
// Canonical is --tags; --tag is the back-compat alias. Both flags must exist on
// both commands (so neither spelling breaks), and the alias must point at the
// canonical name on both — previously new made --tag canonical while edit made
// --tags canonical.
func TestTagFlag_CanonicalAlignment(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"new", newCmd},
		{"edit", editCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical := tc.cmd.Flags().Lookup("tags")
			alias := tc.cmd.Flags().Lookup("tag")
			if canonical == nil {
				t.Fatalf("%s: missing canonical --tags flag", tc.name)
			}
			if alias == nil {
				t.Fatalf("%s: missing back-compat --tag flag", tc.name)
			}
			// The alias must advertise itself as an alias for the canonical name.
			if got := alias.Usage; got != "alias for --tags" {
				t.Errorf("%s: --tag usage = %q, want %q (canonical must be --tags)", tc.name, got, "alias for --tags")
			}
			// The alias and canonical must share one underlying value (so mixed
			// use accumulates rather than clobbering).
			if canonical.Value != alias.Value {
				t.Errorf("%s: --tags and --tag do not share an underlying value", tc.name)
			}
		})
	}
}

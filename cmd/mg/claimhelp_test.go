package main

import (
	"strings"
	"testing"
)

// mg-035ef (drellem2/pogo#241): `mg claim` has refused any item not in
// available/ since mg-38b9, but its help said only "Atomically claim a work
// item by ID" — and a reader concluded from that help that claim had no gate
// check. This pins the help to the gate it actually enforces.
func TestClaimHelp_NamesTheAvailableGate(t *testing.T) {
	bin := buildBinary(t)
	help := mgHelp(t, bin, "claim")

	for _, want := range []string{
		"ONLY from available/",
		"exit 4",
		"pending",
		"depends:",
		"snooze:",
		"shelved",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("mg claim --help must mention %q, got:\n%s", want, help)
		}
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The dead-recipient warning put a store walk on `mg mail send`, which every
// agent in the fleet runs constantly. The claim in maildeadrecipient.go is that
// the walk is bounded and that ONE walk answers both questions the send path
// asks. This benchmark is what makes that claim checkable rather than asserted:
//
//	go test ./cmd/mg/ -run XXX -bench RecipientItem -benchtime=100x
//
// Measured on an M2 Pro against the 2,600-item store below: 1.4ms for a
// work-item box, which resolves on the first spelling and so walks once, and
// 2.9ms for a name that is not a work item at all — the only shape that still
// pays for two walks, both spellings missing. `mg mail send` costs ~9ms in
// process startup alone, so this is a fraction of a command that was never
// fast, on a check that fires once per send.

// benchStore lays out a store the size of the real one — 2,600 items spread
// across every lifecycle directory plus an archive partition — writing the files
// directly rather than through the CLI, which would take minutes.
func benchStore(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	dirs := []string{"available", "claimed", "done", "pending", "shelved"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, "work", d), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	partition := filepath.Join(root, "work", "archive", "2026-07")
	if err := os.MkdirAll(partition, 0o755); err != nil {
		b.Fatal(err)
	}
	write := func(dir, id string) {
		body := fmt.Sprintf("# %s title\n\nbody\n", id)
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 2000; i++ {
		write(filepath.Join(root, "work", dirs[i%len(dirs)]), fmt.Sprintf("mg-%04x", i+0x1000))
	}
	for i := 0; i < 600; i++ {
		write(partition, fmt.Sprintf("mg-%04x", i+0x9000))
	}
	return root
}

func BenchmarkLookupRecipientItem(b *testing.B) {
	root := benchStore(b)

	// A polecat box: the id resolves on the first spelling, so one walk.
	b.Run("work-item-box", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			lookupRecipientItem(root, "1001")
		}
	})

	// A crew mailbox such as `mayor`: neither spelling resolves, which is the
	// only shape that still pays for two walks.
	b.Run("non-work-item-box", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			lookupRecipientItem(root, "mayor")
		}
	})
}

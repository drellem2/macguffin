package event

import (
	"os"
	"testing"

	"github.com/drellem2/macguffin/internal/testtmp"
)

// TestMain nests this binary's temp directories inside one swept root.
//
// It is not bookkeeping: t.TempDir() creates its directory directly in $TMPDIR
// and removes it from a t.Cleanup, so on a panic, a -timeout expiry or a kill it
// leaks one $TMPDIR entry PER TEST — and those are the runs a leak matters on.
// testtmp.Run points $TMPDIR at a single pid-named directory for the whole
// process, so what leaks on an abnormal exit is one entry that the next run
// reclaims by ownership. See internal/testtmp.
func TestMain(m *testing.M) { os.Exit(testtmp.Run("event", m.Run)) }

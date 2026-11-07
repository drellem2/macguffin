package workitem

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
//
// MG_ID_HEX_LEN is cleared for the same reason in miniature: tests that pin the
// default mint width must not change meaning with the caller's environment.
// Tests that exercise it set it themselves with t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv(idHexLenEnv)
	os.Exit(testtmp.Run("workitem", m.Run))
}

package main

import (
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workspace"
)

// rootFlag holds --root, the highest-precedence workspace override. cobra fills
// it during flag parsing, well before any RunE calls resolveRoot().
var rootFlag string

// resolveRoot is the ONE place every command asks where the workspace lives:
//
//	--root  >  $MG_ROOT  >  $HOME/.macguffin
//
// No command may reach for the home-only DefaultRoot directly — that ignores both
// overrides and writes to the real store, which is exactly the foot-gun that
// silently marked a live work item done (mg-4fa7).
//
// Caveat: `mg event append` and `mg log` set DisableFlagParsing to pass their
// args through verbatim, so cobra never binds --root for them — use $MG_ROOT to
// redirect those two. (`mg event list` parses flags normally and honours
// --root.) The write path of the pair guards itself; see rejectRootFlag.
func resolveRoot() (string, error) {
	return workspace.Root(rootFlag)
}

// rejectRootFlag makes a DisableFlagParsing command fail loudly when handed
// --root, instead of silently consuming it as data and writing to the default
// store. Such commands must call this before any side effect. Read-only
// passthroughs (`mg log`) forward the token to the underlying tool and need no
// guard; write paths (`mg event append`) do.
func rejectRootFlag(args []string, useLine string) error {
	for _, a := range args {
		if a == "--root" || strings.HasPrefix(a, "--root=") {
			return mgerr.Usage("usage",
				"--root is not supported by this command because it forwards its arguments verbatim; set $"+workspace.EnvRoot+" instead",
				useLine)
		}
	}
	return nil
}

func init() {
	rootCmd.PersistentFlags().StringVar(&rootFlag, "root", "",
		"workspace root directory (overrides $"+workspace.EnvRoot+"; default ~/.macguffin)")

	rootCmd.Long = rootCmd.Short + "\n\n" +
		"Workspace: mg reads and writes a single store, resolved as\n" +
		"  --root flag  >  $" + workspace.EnvRoot + " env  >  $HOME/.macguffin\n" +
		"The working directory is never consulted; cd does not isolate mg."
}

package main

import (
	"github.com/drellem2/macguffin/mgcli"
	"github.com/drellem2/macguffin/store/fsstore"
)

// Build metadata, overridden at release time via -ldflags "-X main.version=...".
// version is the release tag (e.g. "v0.1.3"); commit and date carry the git SHA
// and build timestamp so a binary in the wild can be traced back to its source.
// The sentinels below are what a plain `go install`/`go build` (no ldflags)
// produces — a dev build.
//
// The Go toolchain also stamps vcs.revision into the binary by itself, and
// reading that back would look like a free way to fill `commit` in. It is not:
// built from a linked git worktree it reports the ENCLOSING repository's HEAD,
// silently and confidently (mg-b7fe). Every agent that builds mg builds from
// exactly that layout. The values here come from git via build.sh's
// derive_version instead, and buildprovenance_test.go fails the suite if anyone
// reaches for the toolchain stamp again.
//
// They stay in package main, not mgcli, so the -X main.* ldflags in build.sh
// and .goreleaser.yml keep meaning what they always meant.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// main is the filesystem build of mg: the whole CLI lives in mgcli, and this
// binary hands it the filesystem storage backend (the store as it has always
// been laid out on disk). A separate module builds mg against another backend
// by calling mgcli.Main with its own store.Opener.
func main() {
	mgcli.Main(fsstore.Open, mgcli.Build{Version: version, Commit: commit, Date: date})
}

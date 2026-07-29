package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// --- the ephemeral-repo guard (mg-0b57) --------------------------------------
//
// A work item is a DURABLE artifact, and its `repo` breadcrumb is a promise that
// the path will still be there when someone dispatches the item. Two trees under
// the pogo home break that promise by construction: every directory in them
// belongs to one agent for one lifetime, and gitgc deletes it when that agent is
// reaped.
//
// Pointing an item at one of those is a SILENT, DELAYED failure. The path is
// real at filing time and stays real for as long as the worktree lives; it
// breaks only when the item is dispatched AFTER the reap, at which point a
// polecat is handed a repo path that does not exist, for an item filed weeks
// earlier by an agent nobody can ask. Nothing reports it at filing time, nothing
// reports it at reap time, and there is no error until the point of use.
//
// `mg new` already declines to AUTO-DETECT under pogo automation — but that rule
// keys on POGO_PID, and measured 2026-07-29 while fixing this: POGO_PID IS NOT
// SET IN A POLECAT'S ENVIRONMENT. A polecat gets POGO_HOME, POGO_AGENT_NAME,
// POGO_AGENT_TYPE, POGO_PROCESS_NAME, POGO_AGENT_PROMPT and POGO_ROLE, and no
// POGO_PID, so for the fleet's most common filer that guard has never fired.
// That is precisely why this one keys on THE PATH: a path is an observation about
// where the repo actually is, while an environment variable is a hope that
// whoever spawned the process exported the right name.
//
// The guard fires on the RESOLVED repo, however it was resolved. An explicit
// `--repo=$(pwd)` from inside a polecat worktree produces exactly the same
// broken item as an omitted flag does, and "remember to pass the right --repo"
// is the class of convention this repo has repeatedly refused to rely on.
//
// It deliberately does NOT rewrite the path to the worktree's origin repo. It
// only NAMES that origin in the hint, so the filer confirms the substitution
// rather than having mg guess on their behalf.

// ephemeralTrees are the paths pogo owns as ephemeral, relative to the pogo
// home. They are matched as prefixes of a resolved absolute path — a repo AT one
// of these directories is not itself a disposable worktree, so only paths
// strictly inside them count.
var ephemeralTrees = []string{
	filepath.Join(".pogo", "polecats"),
	filepath.Join(".pogo", "refinery", "worktrees"),
}

// pogoHome returns the directory the ephemeral trees hang off: $POGO_HOME when
// pogo declared one, otherwise the user's home directory. Returns "" if neither
// can be determined, which leaves the guard inert — a guard that cannot locate
// the trees must fail OPEN, because refusing a legitimate filing is the failure
// mode that gets a guard worked around within a day.
func pogoHome() string {
	if h := os.Getenv("POGO_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// ephemeralRepoTree reports the ephemeral tree containing repo, as a display
// label ("~/.pogo/polecats"), or "" when repo is durable. Both sides are
// symlink-resolved before comparison: git reports physical paths while $HOME may
// be a symlinked one (on macOS /tmp, /var and /etc all are), and comparing one
// against the other by string silently never matches.
func ephemeralRepoTree(repo string) string {
	home := pogoHome()
	if home == "" || repo == "" {
		return ""
	}
	resolved := resolvePath(repo)
	for _, tree := range ephemeralTrees {
		if within(resolved, resolvePath(filepath.Join(home, tree))) {
			return filepath.ToSlash(filepath.Join("~", tree))
		}
	}
	return ""
}

// resolvePath makes p absolute and follows symlinks. A path that does not exist
// cannot be resolved, so it falls back to the absolute form — a repo argument
// naming a directory that is already gone is still worth matching on.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// within reports whether path is strictly inside tree. It compares via
// filepath.Rel rather than a string prefix so that a sibling whose name merely
// starts with the tree's name — ".pogo/polecats-archive" against
// ".pogo/polecats" — is not swept up by the guard.
func within(path, tree string) bool {
	rel, err := filepath.Rel(tree, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// worktreeOrigin returns the repository a git worktree was created from, or ""
// if repo is not a worktree (or the answer is unusable). A linked worktree's
// .git file points at the shared git dir of the repo it came from, so the origin
// is that git dir's parent — the worktree knows its own provenance and does not
// have to be told.
func worktreeOrigin(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repo, common)
	}
	origin := filepath.Dir(resolvePath(common))
	// A repo that is not a linked worktree resolves to itself, which is no
	// suggestion at all; and an origin that is ALSO ephemeral (a worktree of a
	// worktree) would just move the same defect one level up.
	if origin == "" || resolvePath(origin) == resolvePath(repo) || ephemeralRepoTree(origin) != "" {
		return ""
	}
	if fi, err := os.Stat(origin); err != nil || !fi.IsDir() {
		return ""
	}
	return origin
}

// checkEphemeralRepo refuses a repo path inside a pogo-owned ephemeral tree.
// explicit says whether the caller passed --repo themselves, which changes only
// the hint: an omitted flag needs to be told WHERE the path came from, while a
// caller who typed the path already knows.
func checkEphemeralRepo(repo string, explicit bool) error {
	tree := ephemeralRepoTree(repo)
	if tree == "" {
		return nil
	}

	var b strings.Builder
	if explicit {
		b.WriteString("Pass the durable repo the item is actually about")
	} else {
		b.WriteString("--repo was omitted, so mg resolved it from the current directory. " +
			"Pass the durable repo the item is actually about")
	}
	if origin := worktreeOrigin(repo); origin != "" {
		fmt.Fprintf(&b, " — this worktree was created from %s, so --repo=%s", origin, origin)
	}
	b.WriteString("; or --no-repo if the item is not about a code repo; " +
		"or --allow-ephemeral-repo to record this path anyway.")

	return mgerr.Usage("ephemeral_repo", fmt.Sprintf(
		"refusing to record repo %s: it is inside %s, which pogo deletes when the agent owning it is reaped — "+
			"the item would outlive the path and fail only when someone dispatched it",
		repo, tree), b.String())
}

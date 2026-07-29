package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ephemeral-repo guard (mg-0b57).
//
// Measured over the live store on 2026-07-29: 2 of 85 work items carried a repo
// path inside a polecat worktree — a directory gitgc deletes when the polecat is
// reaped. Neither did damage, and the rate was measured BEFORE "polecat files its
// own successor" became the standard shape on the gh-issue track, which makes the
// common filer of new items an agent whose cwd is guaranteed to be deleted.
//
// These tests are written in pairs, and the NEGATIVE half is the one that
// matters. A guard that refuses filings from a real checkout would be worked
// around within a day, and the resulting workaround (everyone passes
// --allow-ephemeral-repo out of habit) is strictly worse than the defect it was
// meant to fix. So every "it refuses" test below has an "and it does not refuse
// this" test on the same page.

// mgIn runs mg from cwd with $HOME and $POGO_HOME both pointed at home, so the
// guard's view of where the ephemeral trees live is the test's temp dir rather
// than the developer's real one. Both are set because pogoHome() prefers
// POGO_HOME, and the real value IS exported in a polecat's environment — a test
// that set only HOME would silently keep testing against ~/.pogo.
func mgIn(t *testing.T, bin, root, home, cwd string, args ...string) (string, int) {
	t.Helper()
	full := append([]string{"--root=" + root}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "POGO_HOME="+home, "POGO_PID=")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mg %v: %v", args, err)
	}
	return string(out), code
}

// gitInit makes dir a real git repo with one commit, so `git rev-parse
// --show-toplevel` resolves and `git worktree add` has a commit to branch from.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	write(t, filepath.Join(dir, "README.md"), "seed\n")
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
}

// gitWorktree adds a linked worktree of origin at path — the same relationship a
// polecat worktree has to the repo it was cut from, so worktreeOrigin has a real
// .git pointer to follow rather than a fabricated one.
func gitWorktree(t *testing.T, origin, path, branch string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path)
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", path, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// itemRepo returns the repo recorded on an item, or "" if it has none.
func itemRepo(t *testing.T, bin, root, id string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := cutPrefixFold(line, "repo:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func cutPrefixFold(line, prefix string) (string, bool) {
	line = strings.TrimSpace(line)
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	return line[len(prefix):], true
}

// ephemeralFixture builds the whole shape at once: a fake pogo home, a durable
// origin repo outside it, and a linked worktree at $HOME/.pogo/polecats/<name>
// standing in for a polecat's working directory.
type ephemeralFixture struct {
	bin, root, home, origin, worktree string
}

func newEphemeralFixture(t *testing.T) ephemeralFixture {
	t.Helper()
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)
	base := t.TempDir()

	home := filepath.Join(base, "home")
	origin := filepath.Join(base, "dev", "macguffin")
	worktree := filepath.Join(home, ".pogo", "polecats", "0b57")

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", home, err)
	}
	gitInit(t, origin)
	gitWorktree(t, origin, worktree, "polecat-0b57")

	return ephemeralFixture{bin: bin, root: root, home: home, origin: origin, worktree: worktree}
}

// --- the acceptance criterion, and the control that makes it mean something ---

// TestCLI_NewFromPolecatWorktreeRefusesEphemeralRepo is the primary acceptance
// criterion: `mg new` with --repo OMITTED, run from inside a polecat worktree,
// must not silently produce an item pointing at that worktree.
func TestCLI_NewFromPolecatWorktreeRefusesEphemeralRepo(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.worktree, "new", "--title=filed from a doomed worktree")

	if code == 0 {
		t.Fatalf("mg new from %s exited 0 — the item was filed pointing at a worktree "+
			"gitgc will delete, which is the whole defect\n%s", f.worktree, out)
	}
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage); got:\n%s", code, out)
	}
	if !strings.Contains(out, ".pogo/polecats") {
		t.Errorf("refusal does not name the ephemeral tree, so the filer cannot tell "+
			"WHY it was refused:\n%s", out)
	}
	// The remedy has to be a copy-paste, not a lookup: an agent that has to go
	// find its origin repo will reach for --allow-ephemeral-repo instead.
	if !strings.Contains(out, f.origin) {
		t.Errorf("refusal does not name the origin repo %s, so the fix is a lookup:\n%s", f.origin, out)
	}
	if !strings.Contains(out, "--no-repo") {
		t.Errorf("refusal does not mention --no-repo:\n%s", out)
	}
}

// TestCLI_NewFromNormalCheckoutStillAutoDetects is the POSITIVE CONTROL the
// ticket demanded be asserted. A guard that also fires on legitimate filings gets
// worked around, so the ordinary case — a developer filing from a real repo — must
// be completely untouched, right down to still auto-filling the repo.
func TestCLI_NewFromNormalCheckoutStillAutoDetects(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.origin, "new", "--title=filed from a real checkout")
	if code != 0 {
		t.Fatalf("mg new from a normal checkout %s: exit %d — the guard fired on a "+
			"legitimate filing\n%s", f.origin, code, out)
	}

	id := idFrom(t, out)
	if got := itemRepo(t, f.bin, f.root, id); resolvePath(got) != resolvePath(f.origin) {
		t.Errorf("repo = %q, want %q — auto-detection from a real checkout regressed", got, f.origin)
	}
}

// --- the explicit flag is not a way around it -------------------------------

// TestCLI_NewExplicitEphemeralRepoRefused covers the case the POGO_PID rule can
// never reach: an agent that dutifully passes --repo, and passes its own cwd.
// The resulting item is identical to the omitted-flag one, so the guard keys on
// the resolved path rather than on how it was resolved.
func TestCLI_NewExplicitEphemeralRepoRefused(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.origin,
		"new", "--title=explicitly aimed at a worktree", "--repo="+f.worktree)
	if code == 0 {
		t.Fatalf("explicit --repo=%s exited 0 — the guard is bypassable by passing "+
			"the same path it refuses to detect\n%s", f.worktree, out)
	}
	if !strings.Contains(out, "--allow-ephemeral-repo") {
		t.Errorf("refusal does not name its own override, so the only way past it is "+
			"to stop using --repo:\n%s", out)
	}
}

// TestCLI_NewRefineryWorktreeRefused covers the second ephemeral tree. The
// refinery's worktrees are as disposable as a polecat's, and a guard that knew
// only about ~/.pogo/polecats would be half a guard.
func TestCLI_NewRefineryWorktreeRefused(t *testing.T) {
	f := newEphemeralFixture(t)
	refinery := filepath.Join(f.home, ".pogo", "refinery", "worktrees", "mr-17")
	gitWorktree(t, f.origin, refinery, "mr-17")

	out, code := mgIn(t, f.bin, f.root, f.home, refinery, "new", "--title=filed from a refinery worktree")
	if code == 0 {
		t.Fatalf("mg new from %s exited 0\n%s", refinery, out)
	}
	if !strings.Contains(out, ".pogo/refinery/worktrees") {
		t.Errorf("refusal does not name the refinery tree:\n%s", out)
	}
}

// --- the escape hatch, and the shapes that must stay unaffected -------------

// TestCLI_NewAllowEphemeralRepoRecordsIt proves the override actually overrides.
// The refusal is a default, not a prohibition — mg cannot know that no item will
// ever legitimately be about a worktree.
func TestCLI_NewAllowEphemeralRepoRecordsIt(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.worktree,
		"new", "--title=deliberately about this worktree", "--allow-ephemeral-repo")
	if code != 0 {
		t.Fatalf("--allow-ephemeral-repo: exit %d\n%s", code, out)
	}
	id := idFrom(t, out)
	if got := itemRepo(t, f.bin, f.root, id); resolvePath(got) != resolvePath(f.worktree) {
		t.Errorf("repo = %q, want the worktree %q — the override did not record the path", got, f.worktree)
	}
}

// TestCLI_NewNoRepoFromWorktreeUnaffected: --no-repo already answers the
// question, so there is no resolved repo to refuse. An item with no repo is the
// correct outcome here, not a refusal.
func TestCLI_NewNoRepoFromWorktreeUnaffected(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.worktree, "new", "--title=not about code", "--no-repo")
	if code != 0 {
		t.Fatalf("--no-repo from a worktree: exit %d — the guard fired on a filing that "+
			"records no repo at all\n%s", code, out)
	}
	if got := itemRepo(t, f.bin, f.root, idFrom(t, out)); got != "" {
		t.Errorf("repo = %q, want empty", got)
	}
}

// TestCLI_NewDurableRepoNamedFromWorktree: filing FROM a worktree while naming a
// durable repo is the good behaviour the ticket wants agents to adopt. It must
// work, or the refusal has no compliant path to point at.
func TestCLI_NewDurableRepoNamedFromWorktree(t *testing.T) {
	f := newEphemeralFixture(t)

	out, code := mgIn(t, f.bin, f.root, f.home, f.worktree,
		"new", "--title=filed from a worktree, aimed at the real repo", "--repo="+f.origin)
	if code != 0 {
		t.Fatalf("--repo=%s from inside a worktree: exit %d\n%s", f.origin, code, out)
	}
	if got := itemRepo(t, f.bin, f.root, idFrom(t, out)); resolvePath(got) != resolvePath(f.origin) {
		t.Errorf("repo = %q, want %q", got, f.origin)
	}
}

// TestCLI_NewOutsideAnyRepoUnaffected: a cwd that is no git repo at all resolves
// to no repo, which is neither ephemeral nor an error.
func TestCLI_NewOutsideAnyRepoUnaffected(t *testing.T) {
	f := newEphemeralFixture(t)
	plain := filepath.Join(t.TempDir(), "notarepo")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, code := mgIn(t, f.bin, f.root, f.home, plain, "new", "--title=filed from nowhere in particular")
	if code != 0 {
		t.Fatalf("mg new from a non-repo dir: exit %d\n%s", code, out)
	}
}

// --- unit-level: the boundary cases a CLI test cannot reach cheaply ---------

// TestEphemeralRepoTreeBoundaries pins the two ways a prefix match goes wrong: a
// sibling directory whose NAME merely starts with an ephemeral tree's name, and
// the tree directory itself (which is durable — pogo deletes the worktrees under
// it, not the container).
func TestEphemeralRepoTreeBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("POGO_HOME", home)
	t.Setenv("HOME", home)

	cases := []struct {
		name     string
		path     string
		wantTree bool
	}{
		{"polecat worktree", filepath.Join(home, ".pogo", "polecats", "0b57"), true},
		{"nested under a worktree", filepath.Join(home, ".pogo", "polecats", "0b57", "sub"), true},
		{"refinery worktree", filepath.Join(home, ".pogo", "refinery", "worktrees", "mr-1"), true},
		{"the polecats container itself", filepath.Join(home, ".pogo", "polecats"), false},
		{"the refinery worktrees container", filepath.Join(home, ".pogo", "refinery", "worktrees"), false},
		{"a name-prefix sibling", filepath.Join(home, ".pogo", "polecats-archive", "0b57"), false},
		{"elsewhere under .pogo", filepath.Join(home, ".pogo", "agents", "mayor"), false},
		{"an ordinary checkout", filepath.Join(home, "dev", "macguffin"), false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ephemeralRepoTree(tc.path)
			if (got != "") != tc.wantTree {
				t.Errorf("ephemeralRepoTree(%q) = %q, want ephemeral=%v", tc.path, got, tc.wantTree)
			}
		})
	}
}

// TestEphemeralRepoTreeInertWithoutHome: with no pogo home and no user home the
// trees cannot be located, and the guard must fail OPEN. Refusing every filing
// because the environment is unusual is how a guard gets deleted.
func TestEphemeralRepoTreeInertWithoutHome(t *testing.T) {
	t.Setenv("POGO_HOME", "")
	t.Setenv("HOME", "")
	if got := ephemeralRepoTree("/anywhere/.pogo/polecats/0b57"); got != "" {
		t.Errorf("ephemeralRepoTree = %q with no home resolvable, want inert", got)
	}
}

// TestWorktreeOriginOfPlainRepo: a repo that is not a linked worktree must yield
// no suggestion. Its git common dir is its own .git, whose parent is the repo
// itself, and "try --repo=<the path you just gave me>" is worse than silence.
func TestWorktreeOriginOfPlainRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	gitInit(t, dir)
	if got := worktreeOrigin(dir); got != "" {
		t.Errorf("worktreeOrigin(plain repo) = %q, want empty", got)
	}
}

// TestWorktreeOriginOfLinkedWorktree: the provenance the refusal quotes comes
// from git, not from parsing the path. A worktree knows the repo it came from.
func TestWorktreeOriginOfLinkedWorktree(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "origin")
	wt := filepath.Join(base, "wt")
	gitInit(t, origin)
	gitWorktree(t, origin, wt, "feature")

	got := worktreeOrigin(wt)
	if resolvePath(got) != resolvePath(origin) {
		t.Errorf("worktreeOrigin(%s) = %q, want %q", wt, got, origin)
	}
}

// idFrom parses the id out of "Created mg-XXXX: title".
func idFrom(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "Created ")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	return strings.TrimSpace(id)
}

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Guard for mg-b7fe: mg must never take its provenance from the VCS metadata
// the Go toolchain stamps by itself.
//
// THE TRAP. Go >= 1.18 embeds vcs.revision / vcs.time / vcs.modified into every
// binary with no ldflags at all, readable via runtime/debug.ReadBuildInfo() or
// `go version -m`. That makes it the obvious zero-cost answer to "which commit
// is this binary". It is wrong here. When the build directory is a LINKED GIT
// WORKTREE (.git is a file, not a directory) nested inside another git
// repository, the toolchain stamps the ENCLOSING repository's HEAD. It does not
// warn and it does not fail -- it emits a confident, well-formed, wrong answer.
//
// That is not an edge case for this project. It is the default layout for every
// agent that touches it: worktrees live under /Users/daniel/.pogo/polecats/<id>
// and /Users/daniel/.pogo is itself a git repo. Reproduced from this very
// worktree -- HEAD 0cabf73, clean -- the stamp read:
//
//	vcs.revision=ec68dc1a2c49d285521117d7307690f3d521f17f
//	vcs.modified=true
//
// ec68dc1a is not an object in macguffin at all (`git cat-file -e` says "Not a
// valid object name"); it is the enclosing repo's HEAD, and that repo's dirty
// files are where modified=true came from. The module pseudo-version came out
// as v0.0.0-20260707170135-ec68dc1a2c49+dirty: a plausible-looking version
// string carrying another project's commit.
//
// WHY A TEST AND NOT A COMMENT. The answer today is that mg consumes none of
// this -- build.sh derives the version by asking git directly (`git -C`, which
// is worktree-correct) and passes it through the -X ldflags in main.go, and the
// survey for mg-b7fe found zero readers of the toolchain stamp anywhere in the
// tree. But "we don't use it" established by one grep on one day is a fact about
// that grep. This test is the instrument that keeps it a fact about the
// codebase: the failure mode is silent by construction -- a wrong revision and a
// right revision are the same token to every check that exists -- so the only
// thing that can catch a future reintroduction is something that looks.
//
// If you are here because this test failed, it is not asking you to find a
// cleverer way to call ReadBuildInfo. Ask git: `git -C <dir> rev-parse HEAD`
// answers for the worktree you are standing in, which is the question you meant.

// provenanceAPIs are the Go entry points to the toolchain's own VCS stamp.
// debug/buildinfo reads it out of a binary on disk; runtime/debug reads it out
// of the running process. Both report the enclosing repo for a worktree build.
const (
	pkgBuildinfo   = "debug/buildinfo"
	pkgRuntimeDbg  = "runtime/debug"
	stampFunc      = "ReadBuildInfo"
	shellStampCall = "go version -m"
	shellStampKey  = "vcs.revision"
)

func TestNoToolchainVCSStampInGoSources(t *testing.T) {
	root := testProjectRoot(t)

	var violations []string
	walkGoFiles(t, root, func(rel string, path string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}

		// Local names bound to runtime/debug in this file. Usually "debug",
		// but an alias binds something else and would slip a name-only scan.
		runtimeDebugNames := map[string]bool{}

		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			switch p {
			case pkgBuildinfo:
				// No legitimate use here: the package exists only to read the
				// stamp, so importing it at all is the violation.
				violations = append(violations, rel+": imports "+pkgBuildinfo+
					" — that package exists only to read the toolchain VCS stamp")
			case pkgRuntimeDbg:
				// runtime/debug is mostly innocent (debug.Stack,
				// SetGCPercent), so the import is not the violation; the
				// ReadBuildInfo call below is.
				name := "debug"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "." {
					violations = append(violations, rel+": dot-imports "+pkgRuntimeDbg+
						" — import it by name so "+stampFunc+" calls stay visible")
					continue
				}
				if name != "_" {
					runtimeDebugNames[name] = true
				}
			}
		}

		if len(runtimeDebugNames) == 0 {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != stampFunc {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || !runtimeDebugNames[id.Name] {
				return true
			}
			violations = append(violations, rel+":"+
				strconv.Itoa(fset.Position(sel.Pos()).Line)+": calls "+id.Name+"."+stampFunc)
			return true
		})
	})

	if len(violations) > 0 {
		t.Errorf("Go source reads the toolchain's VCS stamp (mg-b7fe): built from a "+
			"linked worktree it reports the ENCLOSING repo's HEAD, silently.\n"+
			"Derive the revision from git instead: git -C <dir> rev-parse HEAD\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestNoToolchainVCSStampInShellScripts covers the same misattribution reached
// by the other door. `go version -m <binary>` prints exactly the settings
// ReadBuildInfo returns, so a build or release script that scrapes vcs.revision
// out of it is wrong in precisely the same way and for the same reason -- the
// Go-only guard above would not see it. Comment lines are stripped first: the
// trap is documented in build.sh's derive_version, and describing it must not
// count as doing it.
func TestNoToolchainVCSStampInShellScripts(t *testing.T) {
	root := testProjectRoot(t)

	var violations []string
	walkFiles(t, root, ".sh", func(rel string, path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if code == "" || strings.HasPrefix(code, "#") {
				continue
			}
			for _, bad := range []string{shellStampCall, shellStampKey} {
				if strings.Contains(code, bad) {
					violations = append(violations, rel+":"+strconv.Itoa(i+1)+": "+bad)
				}
			}
		}
	})

	if len(violations) > 0 {
		t.Errorf("shell script scrapes the toolchain's VCS stamp (mg-b7fe): built from a "+
			"linked worktree it reports the ENCLOSING repo's HEAD, silently.\n"+
			"Derive the revision from git instead: git -C <dir> rev-parse HEAD\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func walkGoFiles(t *testing.T, root string, fn func(rel, path string)) {
	t.Helper()
	walkFiles(t, root, ".go", fn)
}

// walkFiles visits every file under root with the given extension, skipping
// vendor/ and dot-directories (.git in particular, which in a linked worktree
// is a file but in the source repo is a large directory).
func walkFiles(t *testing.T, root, ext string, fn func(rel, path string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ext {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		fn(rel, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

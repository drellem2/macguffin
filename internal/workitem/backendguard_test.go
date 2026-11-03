package workitem_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mg's commands reach storage ONLY through store.Backend (mg-b979b).
//
// internal/workitem is mg's whole lifecycle — create, claim, done, edit,
// archive and every other move — and mgcli and cmd/mg are the CLI on top of it.
// If any of them reads, writes or moves a work item by PATH, a second backend
// plugged in through mgcli.Main is silently not reached by that command: it
// passes storetest, and mg never calls it. This test fails on each way back to
// a path:
//
//  1. Importing store/fsstore for anything but fsstore.Open. Open is an
//     Opener like any other and returns a store.Backend; it is the default
//     backend in internal/workitem/backend.go and the one cmd/mg builds with.
//     Every other fsstore symbol (MoveItem, Locate, RewriteExisting, ...) is
//     a path primitive.
//  2. Raw file I/O anywhere in internal/workitem. Item documents AND their
//     companions — the result sidecar, saved bodies, the sweep stamp — go
//     through store.Backend (Backend.Companions, mg-7153e). There used to be
//     an exemption list of companion files here; it is empty now, and a file
//     added to it is a companion reached around the seam again.
//  3. A "work" path literal in internal/workitem, internal/flow,
//     internal/workspace, mgcli or cmd/mg — the start of every hand-built
//     store path (work/available/...). Directories come from the backend.
//  4. A companion file name (".result.json", ".bodybak", ".last-sweep") in a
//     string literal in any of those packages. Those names are the filesystem
//     backend's layout and live in store/fsstore; anywhere else they are a
//     companion path being built by hand, which a non-fs backend never sees.
//     Multi-line raw strings (cobra help text) are documentation, not paths,
//     and are not counted. The positive control is store/fsstore itself,
//     which must contain every one of them.
//
// store/fsstore's own guard (no raw rename/link/O_EXCL outside fsstore) is the
// layer below this one and still applies.

// companionFiles may do raw file I/O in internal/workitem. EMPTY since
// mg-7153e: the companions are reached through store.Companions.
var companionFiles = map[string]bool{}

// companionNames are the filesystem backend's companion file names.
var companionNames = []string{".result.json", ".bodybak", ".last-sweep"}

// rawIO is the file I/O a storage read or write is made of.
var rawIO = map[string]map[string]bool{
	"os": {
		"ReadFile": true, "ReadDir": true, "WriteFile": true, "OpenFile": true, "Open": true,
		"Create": true, "CreateTemp": true, "Stat": true, "Lstat": true, "Remove": true, "RemoveAll": true,
		"Mkdir": true, "MkdirAll": true, "Rename": true, "Link": true, "Symlink": true,
		"Chtimes": true, "Truncate": true, "DirFS": true,
	},
	"io/ioutil": {"ReadFile": true, "ReadDir": true, "WriteFile": true, "TempFile": true},
	"syscall":   {"Open": true, "Rename": true, "Link": true, "Renameat": true, "Unlink": true},
}

const fsstorePath = "github.com/drellem2/macguffin/store/fsstore"

// guardedDirs are the packages held to rules 1 and 3; rawIODirs to rule 2.
var (
	guardedDirs = []string{"internal/workitem", "internal/flow", "internal/workspace", "mgcli", "cmd/mg"}
	rawIODirs   = []string{"internal/workitem"}
)

type finding struct {
	rule, where string
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

// scan returns every guarded reference in the non-test files of dirs.
func scan(t *testing.T, root string, dirs []string) []finding {
	t.Helper()
	var out []finding
	for _, d := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, d))
		if err != nil {
			t.Fatalf("reading %s: %v", d, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := d + "/" + name
			out = append(out, scanFile(t, filepath.Join(root, rel), rel)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].where < out[j].where })
	return out
}

func scanFile(t *testing.T, path, rel string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	local := map[string]string{} // local package name -> import path
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if p == fsstorePath || rawIO[p] != nil {
			local[name] = p
		}
	}
	at := func(pos token.Pos) string { return rel + ":" + strconv.Itoa(fset.Position(pos).Line) }
	var out []finding
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			x, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkg, ok := local[x.Name]
			if !ok {
				return true
			}
			if pkg == fsstorePath {
				out = append(out, finding{"fsstore." + n.Sel.Name, at(n.Pos())})
			} else if rawIO[pkg][n.Sel.Name] {
				out = append(out, finding{"rawio " + pkg + "." + n.Sel.Name, at(n.Pos())})
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			if n.Value == `"work"` {
				out = append(out, finding{`"work" literal`, at(n.Pos())})
			}
			if strings.HasPrefix(n.Value, "`") && strings.Contains(n.Value, "\n") {
				return true // multi-line raw string: help text, not a path
			}
			for _, name := range companionNames {
				if strings.Contains(n.Value, name) {
					out = append(out, finding{"companion " + name, at(n.Pos())})
				}
			}
		}
		return true
	})
	return out
}

func TestLifecycleReachesStorageOnlyThroughBackend(t *testing.T) {
	root := moduleRoot(t)
	all := scan(t, root, guardedDirs)

	// Positive controls: the scanner must see what IS there, or an empty
	// violation list below says nothing. The default Opener in backend.go and
	// cmd/mg's are known positives for rule 1; store/fsstore, where the
	// companion names legitimately live, is the known positive for rules 2
	// and 4 — scanned with the same scanner, it must show raw I/O and every
	// companion name.
	var opens int
	for _, f := range all {
		if f.rule == "fsstore.Open" {
			opens++
		}
	}
	if opens < 2 {
		t.Fatalf("control: the scanner found %d fsstore.Open references (want internal/workitem's default and cmd/mg's) — it is not looking:\n%v", opens, all)
	}
	seen := map[string]bool{}
	for _, f := range scan(t, root, []string{"store/fsstore"}) {
		if strings.HasPrefix(f.rule, "rawio ") && strings.HasSuffix(f.where[:strings.LastIndex(f.where, ":")], "companions.go") {
			seen["rawio"] = true
		}
		if strings.HasPrefix(f.rule, "companion ") {
			seen[strings.TrimPrefix(f.rule, "companion ")] = true
		}
	}
	for _, want := range append([]string{"rawio"}, companionNames...) {
		if !seen[want] {
			t.Fatalf("control: scanning store/fsstore found no %s — the scanner is not looking (saw %v)", want, seen)
		}
	}

	var bad []string
	for _, f := range all {
		file := f.where[:strings.LastIndex(f.where, ":")]
		switch {
		case f.rule == "fsstore.Open":
			// an Opener: a store.Backend, not a path primitive
		case strings.HasPrefix(f.rule, "fsstore."):
			bad = append(bad, f.where+": "+f.rule+" — a filesystem path primitive; go through store.Backend")
		case strings.HasPrefix(f.rule, "rawio "):
			inRawDir := false
			for _, d := range rawIODirs {
				if strings.HasPrefix(file, d+"/") {
					inRawDir = true
				}
			}
			if inRawDir && !companionFiles[file] {
				bad = append(bad, f.where+": "+strings.TrimPrefix(f.rule, "rawio ")+" — raw file I/O outside the companion files; item documents go through store.Backend")
			}
		case strings.HasPrefix(f.rule, "companion "):
			bad = append(bad, f.where+": "+f.rule+" — a companion file name outside store/fsstore; reach it through store.Backend.Companions")
		default:
			bad = append(bad, f.where+": "+f.rule+" — a hand-built store path; take directories from the backend")
		}
	}
	if len(bad) > 0 {
		t.Fatalf("storage reached around store.Backend:\n%s", strings.Join(bad, "\n"))
	}
}

// The scanner itself, against a file known to break every rule.
func TestBackendGuardCatchesViolations(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import (
	"os"
	fs "github.com/drellem2/macguffin/store/fsstore"
)

func f(root, id string) {
	_ = fs.MoveItem("a", "b")
	_, _ = os.ReadFile(root + "/" + "work")
	_ = id + ".result.json"
	_ = "/.bodybak/" + id
	_ = ".last-sweep"
}

var help = ` + "`" + `docs mention work/.bodybak/<id>/
on two lines` + "`" + `
`
	path := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range scanFile(t, path, "internal/workitem/bad.go") {
		got[f.rule] = true
	}
	for _, want := range []string{"fsstore.MoveItem", "rawio os.ReadFile", `"work" literal`,
		"companion .result.json", "companion .bodybak", "companion .last-sweep"} {
		if !got[want] {
			t.Errorf("scanner missed %s in a file that has it (found %v)", want, got)
		}
	}
	// The multi-line help string mentions .bodybak once and is not counted:
	// exactly one .bodybak finding, from the path literal.
	n := 0
	for _, f := range scanFile(t, path, "internal/workitem/bad.go") {
		if f.rule == "companion .bodybak" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d .bodybak findings, want 1 (the path, not the help text)", n)
	}
}

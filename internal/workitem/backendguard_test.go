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
//  2. Raw file I/O in internal/workitem outside the COMPANION files: the
//     result sidecar, saved bodies and the sweep stamp are local files by
//     nature and are reached only on a store.Local backend, through the
//     directories it names. They are listed below by name; an item document
//     read or written anywhere else is a violation.
//  3. A "work" path literal in internal/workitem, internal/flow, mgcli or
//     cmd/mg — the start of every hand-built store path (work/available/...).
//     Directories come from the backend (store.Local), never from a join.
//
// store/fsstore's own guard (no raw rename/link/O_EXCL outside fsstore) is the
// layer below this one and still applies.

// companionFiles may do raw file I/O in internal/workitem — on companion
// files only, under directories a store.Local backend names.
var companionFiles = map[string]bool{
	"internal/workitem/bodybackup.go":     true,
	"internal/workitem/sidecar.go":        true,
	"internal/workitem/sidecarpath.go":    true,
	"internal/workitem/sidecarcompare.go": true,
	"internal/workitem/sweepstamp.go":     true,
}

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
	guardedDirs = []string{"internal/workitem", "internal/flow", "mgcli", "cmd/mg"}
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
			if n.Kind == token.STRING && n.Value == `"work"` {
				out = append(out, finding{`"work" literal`, at(n.Pos())})
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
	// cmd/mg's, and the companion files' raw I/O, are known positives.
	var opens, companion int
	for _, f := range all {
		if f.rule == "fsstore.Open" {
			opens++
		}
		file := f.where[:strings.LastIndex(f.where, ":")]
		if strings.HasPrefix(f.rule, "rawio ") && companionFiles[file] {
			companion++
		}
	}
	if opens < 2 {
		t.Fatalf("control: the scanner found %d fsstore.Open references (want internal/workitem's default and cmd/mg's) — it is not looking:\n%v", opens, all)
	}
	if companion < 5 {
		t.Fatalf("control: the scanner found only %d raw I/O calls in the companion files — it is not looking", companion)
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
		default:
			bad = append(bad, f.where+": "+f.rule+" — a hand-built store path; take directories from the backend (store.Local)")
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

func f(root string) {
	_ = fs.MoveItem("a", "b")
	_, _ = os.ReadFile(root + "/" + "work")
}
`
	path := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range scanFile(t, path, "internal/workitem/bad.go") {
		got[f.rule] = true
	}
	for _, want := range []string{"fsstore.MoveItem", "rawio os.ReadFile", `"work" literal`} {
		if !got[want] {
			t.Errorf("scanner missed %s in a file that has it (found %v)", want, got)
		}
	}
}

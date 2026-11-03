package fsstore_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The atomic filesystem steps — rename(2), link(2), O_EXCL — on work-item store
// paths live ONLY in store/fsstore. Everything else reaches them through its
// primitives (MoveItem, MoveNoReplace, CreateExclusive, RewriteExisting,
// MoveFile) or through store.Backend. A raw os.Rename creeping back into
// internal/workitem or mgcli is a transition the backend seam cannot see, and
// one a second backend would silently not implement.
//
// internal/mail is exempt, deliberately and by name: mail is its own store
// (maildir-style tmp/new/cur), it stays local under every backend, and it is
// not part of store.Backend. If mail ever moves behind the seam, delete the
// exemption and this test will list what has to move.

var guarded = map[string]map[string]bool{
	"os":      {"Rename": true, "Link": true, "O_EXCL": true},
	"syscall": {"Rename": true, "Link": true, "Renameat": true, "O_EXCL": true},
}

var allowedDirs = []string{
	"store/fsstore/",
	"internal/mail/", // see the comment above
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

// rawFSCalls returns "rel:line: pkg.Name" for every guarded reference in the
// module's non-test Go files, keyed by relative path.
func rawFSCalls(t *testing.T) []string {
	t.Helper()
	root := moduleRoot(t)
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		// Local names for the guarded packages in this file (an alias must
		// not slip past a name-only scan).
		local := map[string]string{}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if _, ok := guarded[p]; !ok {
				continue
			}
			name := p
			if imp.Name != nil {
				name = imp.Name.Name
			}
			local[name] = p
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg, ok := local[x.Name]; ok && guarded[pkg][sel.Sel.Name] {
				hits = append(hits, rel+":"+strconv.Itoa(fset.Position(sel.Pos()).Line)+": "+pkg+"."+sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(hits)
	return hits
}

func allowed(hit string) bool {
	for _, d := range allowedDirs {
		if strings.HasPrefix(hit, d) {
			return true
		}
	}
	return false
}

func TestNoRawRenameLinkOrExclOutsideFSBackend(t *testing.T) {
	hits := rawFSCalls(t)

	// Positive control: the scanner must find the primitives where they DO
	// live. If it finds nothing in store/fsstore, the instrument is broken and
	// an empty violation list below would mean nothing.
	var inBackend int
	for _, h := range hits {
		if strings.HasPrefix(h, "store/fsstore/") {
			inBackend++
		}
	}
	if inBackend < 3 {
		t.Fatalf("scanner found only %d guarded calls in store/fsstore (want rename, link and O_EXCL at least) — the guard is not looking:\n%s",
			inBackend, strings.Join(hits, "\n"))
	}

	var bad []string
	for _, h := range hits {
		if !allowed(h) {
			bad = append(bad, h)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("raw rename/link/O_EXCL outside the filesystem backend — route these through store/fsstore:\n%s", strings.Join(bad, "\n"))
	}
}

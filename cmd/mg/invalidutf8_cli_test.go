package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// drellem2/macguffin#41: invalid UTF-8 is refused at write time, with exit 2,
// naming the field and the byte offset — on the real CLI paths a caller meets.

func TestCLI_NewRefusesInvalidUTF8BodyFile(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	// 0xC3 opens a two-byte sequence that never finishes.
	out, code := mgArchive(t, bin, root, "new", "--title=latin1 body", bodyFileArg(t, "ok so far\n\xc3"))
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "body: invalid UTF-8 at byte offset 10") {
		t.Errorf("refusal does not name the field and offset:\n%s", out)
	}
	for _, d := range []string{"available", "pending"} {
		entries, _ := os.ReadDir(filepath.Join(root, "work", d))
		if len(entries) != 0 {
			t.Fatalf("a refused filing left %d entr(ies) in %s/", len(entries), d)
		}
	}
}

func TestCLI_DoneRefusesInvalidUTF8Result(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)
	id := seedClaimedTagged(t, bin, root, "a result with a bad byte")

	// Valid JSON by json.Valid's reading — which is exactly the gap.
	out, code := mgArchive(t, bin, root, "done", id, "--result={\"note\":\"caf\xe9\"}")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "result: invalid UTF-8 at byte offset 12") {
		t.Errorf("refusal does not name the field and offset:\n%s", out)
	}
	if _, err := os.Stat(claimedSidecar(root, id)); !os.IsNotExist(err) {
		t.Errorf("a refused --result was written to the sidecar (stat err %v)", err)
	}
	if out, code := mgArchive(t, bin, root, "done", id, `--result={"note":"café"}`); code != 0 {
		t.Fatalf("valid UTF-8 result refused: exit %d\n%s", code, out)
	}
}

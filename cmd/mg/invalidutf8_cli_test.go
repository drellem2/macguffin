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

// mg-a6f1c: the four reproductions from the PR #42 review — --type and the
// depends lists were not in the field table, so each exited 0 and stored the
// byte. Each must now exit 2 and leave the store byte-identical.
func TestCLI_TypeAndDependsRefuseInvalidUTF8(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)
	id := seedClaimedTagged(t, bin, root, "an item to edit", "--no-repo")
	snapshot := func() string {
		var b strings.Builder
		filepath.Walk(filepath.Join(root, "work"), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				data, _ := os.ReadFile(p)
				b.WriteString(p + "\n" + string(data) + "\n")
			}
			return nil
		})
		return b.String()
	}
	before := snapshot()

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"new", "--no-repo", "--title=t", "--type=t\xff"}, "type: invalid UTF-8 at byte offset 1"},
		{[]string{"new", "--no-repo", "--title=t", "--depends=mg-\xff"}, "depends[0]: invalid UTF-8 at byte offset 3"},
		{[]string{"edit", id, "--type=t\xff"}, "type: invalid UTF-8 at byte offset 1"},
		{[]string{"edit", id, "--depends=mg-\xff"}, "depends[0]: invalid UTF-8 at byte offset 3"},
		{[]string{"edit", id, "--add-depends=mg-\xff"}, "add-depends[0]: invalid UTF-8 at byte offset 3"},
	} {
		out, code := mgArchive(t, bin, root, tc.args...)
		if code != 2 {
			t.Errorf("mg %s: exit = %d, want 2\n%s", tc.args[0], code, out)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("mg %s: refusal does not name %q:\n%s", tc.args[0], tc.want, out)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("a refused write changed the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// Negative control: multibyte type and dependency ids are still accepted.
	if out, code := mgArchive(t, bin, root, "edit", id, "--type=désign", "--add-depends=mg-é"); code != 0 {
		t.Fatalf("valid UTF-8 type/depends refused: exit %d\n%s", code, out)
	}
}

// mg-a6f1c: event append hands its arguments to the JSON encoder, which would
// store U+FFFD in place of the bad byte and exit 0.
func TestCLI_EventAppendRefusesInvalidUTF8(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	eventsBefore := countEventLines(t, home)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"event", "append", "ty\xff", "--k=v"}, "event type: invalid UTF-8 at byte offset 2"},
		{[]string{"event", "append", "ok", "--k=v\xff"}, "argument 2: invalid UTF-8 at byte offset 5"},
	} {
		out, err := emRun(bin, env, tc.args...)
		if code := exitCodeOf(err); code != 2 {
			t.Errorf("exit = %d, want 2\n%s", code, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("refusal does not name %q:\n%s", tc.want, out)
		}
	}
	if n := countEventLines(t, home); n != eventsBefore {
		t.Fatalf("a refused append persisted %d event(s)", n-eventsBefore)
	}
	emOK(t, bin, env, "event", "append", "ok", "--k=café")
}

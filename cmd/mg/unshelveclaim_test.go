package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mg unshelve --claim at the CLI seam (drellem2/macguffin#34). The semantics are
// proven in internal/workitem/shelve_test.go; what is proven here is the flag
// wiring: --pid reaches the claim, the refusal exits non-zero, --pid without
// --claim is a usage error, and the output says whether the item was claimed.

func TestCLI_UnshelveClaim(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "claimed then shelved")
	if out, code := mgArchive(t, bin, root, "claim", id, "--pid=4242"); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "shelve", id); code != 0 {
		t.Fatalf("mg shelve: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 || !strings.Contains(out, "mg unshelve "+id+" --claim") {
		t.Errorf("mg done on the shelved item: exit %d, want a non-zero exit pointing at --claim\n%s", code, out)
	}

	out, code = mgArchive(t, bin, root, "unshelve", id, "--pid=5151")
	if code != 2 {
		t.Errorf("--pid without --claim: exit %d, want 2 (usage)\n%s", code, out)
	}

	out, code = mgArchive(t, bin, root, "unshelve", id, "--claim", "--pid=5151")
	if code != 0 {
		t.Fatalf("mg unshelve --claim: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Unshelved and claimed "+id) {
		t.Errorf("output = %q, want it to say the item was claimed", out)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "claimed", id+".md.5151")); err != nil {
		t.Errorf("expected claimed/%s.md.5151: %v", id, err)
	}
}

func TestCLI_UnshelveClaimRefusesItemNeverClaimed(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "shelved from available")
	if out, code := mgArchive(t, bin, root, "shelve", id); code != 0 {
		t.Fatalf("mg shelve: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "unshelve", id, "--claim")
	if code == 0 {
		t.Fatalf("mg unshelve --claim on an item shelved from available exited 0\n%s", out)
	}
	if st := statusOf(t, bin, root, id); st != "shelved" {
		t.Errorf("status = %q after the refusal, want shelved", st)
	}
}

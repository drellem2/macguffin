package main

import (
	"strings"
	"testing"
)

// mg unshelve --tag at the CLI seam (drellem2/macguffin#32). The semantics are
// proven in internal/workitem/shelve_test.go; what is proven here is the flag
// wiring: the group comes back, ID together with --tag and neither are usage
// errors, --claim is refused with --tag, and a tag with nothing shelved fails.

func TestCLI_UnshelveByTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	a := seedShelvable(t, bin, root, "task", "group one", "--tag=grp")
	b := seedShelvable(t, bin, root, "task", "group two", "--tag=grp")
	other := seedShelvable(t, bin, root, "task", "not in the group")
	if out, code := mgArchive(t, bin, root, "shelve", "--tag=grp"); code != 0 {
		t.Fatalf("mg shelve --tag: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "shelve", other); code != 0 {
		t.Fatalf("mg shelve: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "unshelve", "--tag=grp")
	if code != 0 {
		t.Fatalf("mg unshelve --tag: exit %d\n%s", code, out)
	}
	for _, id := range []string{a, b} {
		if !strings.Contains(out, "Unshelved "+id) {
			t.Errorf("output does not name %s:\n%s", id, out)
		}
		if st := statusOf(t, bin, root, id); st != "available" {
			t.Errorf("%s status = %q, want available", id, st)
		}
	}
	if st := statusOf(t, bin, root, other); st != "shelved" {
		t.Errorf("untagged item status = %q, want shelved", st)
	}
}

func TestCLI_UnshelveTagRefusals(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "tagged", "--tag=grp")
	if out, code := mgArchive(t, bin, root, "shelve", id); code != 0 {
		t.Fatalf("mg shelve: exit %d\n%s", code, out)
	}

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"ID and --tag", []string{"unshelve", id, "--tag=grp"}, 2, "not both"},
		{"neither", []string{"unshelve"}, 2, "requires a work item ID or --tag"},
		{"--claim with --tag", []string{"unshelve", "--tag=grp", "--claim"}, 2, "cannot be combined with --tag"},
		{"no match", []string{"unshelve", "--tag=nope"}, -1, `no shelved items found with tag "nope"`},
	} {
		out, code := mgArchive(t, bin, root, tc.args...)
		if code == 0 || (tc.code > 0 && code != tc.code) {
			t.Errorf("%s: exit %d, want %d\n%s", tc.name, code, tc.code, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: output %q does not contain %q", tc.name, out, tc.want)
		}
	}
	if st := statusOf(t, bin, root, id); st != "shelved" {
		t.Errorf("status = %q after the refusals, want shelved", st)
	}
}

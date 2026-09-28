package main

import (
	"strings"
	"testing"
)

// mg unshelve names the shelved dependents it leaves behind (mg-3066c). The
// rule is proven in internal/workitem/unshelvecascade_test.go; what is proven
// here is that the operator SEES it: a dependent shelved on its own is named
// with its reason, the command still exits 0, and the cascade still comes back.
func TestCLI_UnshelveNamesIndependentlyShelvedDependent(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	a := seedShelvable(t, bin, root, "task", "parent")
	b := seedShelvable(t, bin, root, "task", "cascaded", "--depends="+a)
	x := seedShelvable(t, bin, root, "task", "held on its own", "--depends="+a)
	if out, code := mgArchive(t, bin, root, "shelve", x); code != 0 {
		t.Fatalf("mg shelve %s: exit %d\n%s", x, code, out)
	}
	if out, code := mgArchive(t, bin, root, "shelve", a); code != 0 {
		t.Fatalf("mg shelve %s: exit %d\n%s", a, code, out)
	}
	k := seedShelvable(t, bin, root, "task", "filed under the hold", "--depends="+a)
	if st := statusOf(t, bin, root, k); st != "shelved" {
		t.Fatalf("filed item status = %q, want shelved", st)
	}

	out, code := mgArchive(t, bin, root, "unshelve", a)
	if code != 0 {
		t.Fatalf("mg unshelve: exit %d\n%s", code, out)
	}
	for _, id := range []string{a, b, k} {
		if !strings.Contains(out, "Unshelved "+id) {
			t.Errorf("output does not name %s as unshelved:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "Left shelved "+x) || !strings.Contains(out, "shelved on its own") {
		t.Errorf("output does not name %s as left shelved on its own:\n%s", x, out)
	}
	if strings.Contains(out, "Unshelved "+x) {
		t.Errorf("output claims %s was unshelved:\n%s", x, out)
	}
	if st := statusOf(t, bin, root, x); st != "shelved" {
		t.Errorf("%s status = %q, want shelved", x, st)
	}
	if st := statusOf(t, bin, root, b); st != "pending" {
		t.Errorf("%s status = %q, want pending", b, st)
	}
}

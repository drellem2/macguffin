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
	if !strings.Contains(out, "Unshelved "+a) {
		t.Errorf("output does not name %s as unshelved:\n%s", a, out)
	}
	for _, id := range []string{b, k} {
		if !strings.Contains(out, "\n  "+id+": ") {
			t.Errorf("output does not list %s as a restored dependent:\n%s", id, out)
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

// mg unshelve labels the cascade (drellem2/macguffin#39). Printed with the same
// "Unshelved <id>" line as the named item, a dependent's id read as if the
// caller had passed it. The named item comes first, then every dependent is
// listed under one label, mirroring mg shelve's "Also shelved" (mg-2cf0).
func TestCLI_UnshelveLabelsCascadedDependents(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	a := seedShelvable(t, bin, root, "task", "parent")
	b := seedShelvable(t, bin, root, "task", "child", "--depends="+a)
	c := seedShelvable(t, bin, root, "task", "grandchild", "--depends="+b)
	if out, code := mgArchive(t, bin, root, "shelve", a); code != 0 {
		t.Fatalf("mg shelve %s: exit %d\n%s", a, code, out)
	}

	out, code := mgArchive(t, bin, root, "unshelve", a)
	if code != 0 {
		t.Fatalf("mg unshelve: exit %d\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{
		"Unshelved " + a + ": parent",
		"Also unshelved 2 dependent item(s):",
	}
	if len(lines) != 4 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("output shape:\n%s\nwant %q then %q then two dependent lines", out, want[0], want[1])
	}
	deps := map[string]bool{lines[2]: true, lines[3]: true}
	for _, l := range []string{"  " + b + ": child", "  " + c + ": grandchild"} {
		if !deps[l] {
			t.Errorf("missing dependent line %q:\n%s", l, out)
		}
	}
	for _, id := range []string{b, c} {
		if strings.Contains(out, "Unshelved "+id) {
			t.Errorf("dependent %s reported with the named item's line:\n%s", id, out)
		}
	}

	// Nothing cascaded: no label at all.
	d := seedShelvable(t, bin, root, "task", "alone")
	if out, code := mgArchive(t, bin, root, "shelve", d); code != 0 {
		t.Fatalf("mg shelve %s: exit %d\n%s", d, code, out)
	}
	out, code = mgArchive(t, bin, root, "unshelve", d)
	if code != 0 {
		t.Fatalf("mg unshelve %s: exit %d\n%s", d, code, out)
	}
	if got := strings.TrimRight(out, "\n"); got != "Unshelved "+d+": alone" {
		t.Errorf("output = %q, want only the named item's line", got)
	}
}

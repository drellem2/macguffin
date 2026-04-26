package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUnclaimReleasesByID(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "claimed work", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate a claim with the current process PID — proving Unclaim does
	// NOT consult PID liveness. The reap bug was exactly this: live workers
	// got their claims released because the PID check is unreliable.
	livePID := os.Getpid()
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, livePID))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}

	if res.ID != item.ID {
		t.Errorf("res.ID = %q, want %q", res.ID, item.ID)
	}
	if res.PID != livePID {
		t.Errorf("res.PID = %d, want %d", res.PID, livePID)
	}

	// Item should be back in available/
	availPath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availPath); err != nil {
		t.Errorf("item not back in available/: %v", err)
	}

	// Claimed file should be gone
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("claimed file should be gone, got err: %v", err)
	}
}

func TestUnclaimWithoutPIDSuffix(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "claimed without pid", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Move to claimed/ with no PID suffix
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", item.ID+".md")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}

	if res.PID != 0 {
		t.Errorf("res.PID = %d, want 0 (no suffix)", res.PID)
	}

	availPath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availPath); err != nil {
		t.Errorf("item not back in available/: %v", err)
	}
}

func TestUnclaimNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Unclaim(root, "mg-nope")
	if err == nil {
		t.Fatal("expected error unclaiming nonexistent item, got nil")
	}
}

func TestUnclaimNotClaimedYet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Item exists in available/ but not in claimed/ — Unclaim should fail
	// because there is no claim to release.
	item, err := Create(root, "mg-", "task", "available only", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Unclaim(root, item.ID)
	if err == nil {
		t.Fatal("expected error unclaiming item that is not in claimed/, got nil")
	}
}

func TestParseClaimPID(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"mg-abcd.md.12345", 12345},
		{"mg-abcd.md.0", 0},
		{"mg-abcd.md", 0},         // no suffix
		{"mg-abcd.md.notanum", 0}, // unparseable
	}
	for _, c := range cases {
		got := parseClaimPID(c.name)
		if got != c.want {
			t.Errorf("parseClaimPID(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

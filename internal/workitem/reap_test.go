package workitem

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReapDeadPID(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Will be abandoned", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate a claimed file with a dead PID (99999999 should not exist)
	deadPID := 99999999
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, deadPID))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	reaped, err := Reap(root)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	if len(reaped) != 1 {
		t.Fatalf("expected 1 reaped item, got %d", len(reaped))
	}
	if reaped[0].ID != item.ID {
		t.Errorf("reaped ID = %q, want %q", reaped[0].ID, item.ID)
	}
	if reaped[0].PID != deadPID {
		t.Errorf("reaped PID = %d, want %d", reaped[0].PID, deadPID)
	}

	// Verify: item is back in available/
	availPath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availPath); err != nil {
		t.Errorf("item not found in available/: %v", err)
	}

	// Verify: item is gone from claimed/
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("claimed file should be gone, got err: %v", err)
	}
}

func TestReapLivePID(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Active work", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Use our own PID (definitely alive)
	livePID := os.Getpid()
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, livePID))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	reaped, err := Reap(root)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	if len(reaped) != 0 {
		t.Errorf("expected 0 reaped items (PID alive), got %d", len(reaped))
	}

	// Verify: item is still in claimed/
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("claimed file should still exist: %v", err)
	}
}

func TestReapEmpty(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	reaped, err := Reap(root)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	if len(reaped) != 0 {
		t.Errorf("expected 0 reaped items, got %d", len(reaped))
	}
}

func TestReapMultiple(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	deadPID := 99999998

	// Create two items and simulate dead claims
	for i, title := range []string{"Abandoned one", "Abandoned two"} {
		item, err := Create(root, "mg-", "bug", title, nil)
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		src := filepath.Join(root, "work", "available", item.ID+".md")
		dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, deadPID+i))
		if err := os.Rename(src, dst); err != nil {
			t.Fatalf("simulating claim %d: %v", i, err)
		}
	}

	reaped, err := Reap(root)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	if len(reaped) != 2 {
		t.Fatalf("expected 2 reaped items, got %d", len(reaped))
	}

	// Both should be back in available/
	avail, _ := os.ReadDir(filepath.Join(root, "work", "available"))
	mdCount := 0
	for _, e := range avail {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 2 {
		t.Errorf("expected 2 items in available/, got %d", mdCount)
	}
}

// TestReapE2E is the end-to-end test from the milestone spec:
// claim in a subprocess, kill the subprocess, reap, verify item back in available/.
func TestReapE2E(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "E2E reap target", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Start a subprocess that claims the item and then sleeps forever
	cmd := exec.Command("sleep", "999")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting subprocess: %v", err)
	}
	subPID := cmd.Process.Pid

	// Move the file to claimed/ with the subprocess PID
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, subPID))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	// Kill the subprocess
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing subprocess: %v", err)
	}
	cmd.Wait() // reap zombie

	// Now run Reap — the PID is dead
	reaped, err := Reap(root)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	if len(reaped) != 1 {
		t.Fatalf("expected 1 reaped item, got %d", len(reaped))
	}
	if reaped[0].ID != item.ID {
		t.Errorf("reaped ID = %q, want %q", reaped[0].ID, item.ID)
	}

	// Verify: item is back in available/
	availPath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availPath); err != nil {
		t.Errorf("item not found in available/ after reap: %v", err)
	}
}

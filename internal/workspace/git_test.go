package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitGit_CreatesRepo(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("InitGit: %v", err)
	}

	gitDir := filepath.Join(mgRoot, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		t.Fatalf(".git directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".git should be a directory")
	}
}

func TestInitGit_Idempotent(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("first InitGit: %v", err)
	}
	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("second InitGit: %v", err)
	}
}

func TestSnapshot_CreatesCommit(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("InitGit: %v", err)
	}

	// Create a file to snapshot
	if err := os.WriteFile(filepath.Join(mgRoot, "work", "available", "item.md"), []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Verify commit exists
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = mgRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "state snapshot") {
		t.Errorf("expected commit message containing 'state snapshot', got: %s", out)
	}
}

func TestSnapshot_NothingToCommit(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("InitGit: %v", err)
	}

	// First snapshot to commit the initial dirs
	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("first Snapshot: %v", err)
	}

	// Second snapshot with no changes should succeed (noop)
	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("second Snapshot should not error on nothing to commit: %v", err)
	}
}

func TestSnapshot_NoGitRepo(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	err := Snapshot(mgRoot)
	if err == nil {
		t.Fatal("Snapshot should fail when no git repo exists")
	}
}

func TestSnapshot_MultipleCommits(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("InitGit: %v", err)
	}

	// First snapshot: create an item
	if err := os.WriteFile(filepath.Join(mgRoot, "work", "available", "item.md"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("first Snapshot: %v", err)
	}

	// Second snapshot: move item to done (simulating lifecycle)
	if err := os.Rename(
		filepath.Join(mgRoot, "work", "available", "item.md"),
		filepath.Join(mgRoot, "work", "done", "item.md"),
	); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}

	// Verify at least 2 commits
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = mgRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected >= 2 commits, got %d: %s", len(lines), out)
	}
}

func TestLog_ShowsHistory(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := InitGit(mgRoot); err != nil {
		t.Fatalf("InitGit: %v", err)
	}

	// Create a file and snapshot
	if err := os.WriteFile(filepath.Join(mgRoot, "work", "available", "item.md"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(mgRoot); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Log should not error
	if err := Log(mgRoot, []string{"--oneline"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
}

func TestLog_NoGitRepo(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")
	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	err := Log(mgRoot, nil)
	if err == nil {
		t.Fatal("Log should fail when no git repo exists")
	}
}

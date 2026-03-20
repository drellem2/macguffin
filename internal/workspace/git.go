package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// InitGit initializes a git repository in the macguffin root directory.
// It is idempotent — if .git already exists, it does nothing.
func InitGit(root string) error {
	gitDir := filepath.Join(root, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		return nil // already initialized
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	return nil
}

// Snapshot stages all files and creates a commit in the macguffin root.
// The commit message includes an ISO-8601 timestamp.
// Returns nil if there is nothing to commit.
func Snapshot(root string) error {
	gitDir := filepath.Join(root, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return fmt.Errorf("not a git repository (run mg init --git first): %w", err)
	}

	// Stage everything
	add := exec.Command("git", "add", "-A")
	add.Dir = root
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -A: %s: %w", out, err)
	}

	// Check if there is anything to commit
	diff := exec.Command("git", "diff", "--cached", "--quiet")
	diff.Dir = root
	if err := diff.Run(); err == nil {
		fmt.Println("nothing to snapshot")
		return nil
	}

	// Commit
	msg := fmt.Sprintf("state snapshot %s", time.Now().UTC().Format(time.RFC3339))
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = root
	commit.Stdout = os.Stdout
	commit.Stderr = os.Stderr
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// Log runs git log in the macguffin root, passing through any extra arguments.
func Log(root string, args []string) error {
	gitDir := filepath.Join(root, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return fmt.Errorf("not a git repository (run mg init --git first): %w", err)
	}

	gitArgs := append([]string{"log"}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	return nil
}

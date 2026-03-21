package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRoot returns the default macguffin root directory (~/.macguffin).
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".macguffin"), nil
}

// Init creates the canonical macguffin directory tree.
// If root is empty, DefaultRoot() is used.
// Init is idempotent — safe to call on an existing tree.
func Init(root string) error {
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return err
		}
	}

	dirs := []string{
		filepath.Join(root, "work", "available"),
		filepath.Join(root, "work", "claimed"),
		filepath.Join(root, "work", "done"),
		filepath.Join(root, "work", "pending"),
		filepath.Join(root, "work", "archive"),
		filepath.Join(root, "agents"),
		filepath.Join(root, "mail"),
		filepath.Join(root, "log"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}

	fmt.Printf("Initialized macguffin at %s\n", root)
	return nil
}

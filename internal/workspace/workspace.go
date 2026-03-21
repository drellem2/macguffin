package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

const configFile = "config"

// DefaultPrefix is the default work item ID prefix.
const DefaultPrefix = "mg-"

// WriteConfig writes a key=value pair to the workspace config file.
func WriteConfig(root, key, value string) error {
	cfg := readConfigMap(root)
	cfg[key] = value
	return writeConfigMap(root, cfg)
}

// ReadConfig reads a value from the workspace config file.
func ReadConfig(root, key string) string {
	cfg := readConfigMap(root)
	return cfg[key]
}

// Prefix returns the configured work item ID prefix, or DefaultPrefix if unset.
func Prefix(root string) string {
	if p := ReadConfig(root, "prefix"); p != "" {
		return p
	}
	return DefaultPrefix
}

func readConfigMap(root string) map[string]string {
	cfg := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(root, configFile))
	if err != nil {
		return cfg
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			cfg[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return cfg
}

func writeConfigMap(root string, cfg map[string]string) error {
	var lines []string
	for k, v := range cfg {
		lines = append(lines, k+"="+v)
	}
	// Sort for deterministic output
	sort.Strings(lines)
	data := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(filepath.Join(root, configFile), []byte(data), 0o644)
}

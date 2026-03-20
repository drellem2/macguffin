package workitem

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Item represents a work item with YAML frontmatter fields.
type Item struct {
	ID      string
	Type    string
	Created time.Time
	Creator string
	Title   string
	Body    string // everything after frontmatter (raw markdown)
}

// GenerateID produces a short hash ID in gt-XXX format (3 hex chars).
func GenerateID(title string, created time.Time) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte(created.Format(time.RFC3339Nano)))
	sum := h.Sum(nil)
	return fmt.Sprintf("gt-%x", sum[:2])
}

// Create writes a new work item file to available/.
func Create(root, typ, title string) (*Item, error) {
	now := time.Now().UTC()
	id := GenerateID(title, now)

	creator := currentUser()

	item := &Item{
		ID:      id,
		Type:    typ,
		Created: now,
		Creator: creator,
		Title:   title,
	}

	dir := filepath.Join(root, "work", "available")
	path := filepath.Join(dir, id+".md")

	content := fmt.Sprintf(`---
id: %s
type: %s
created: %s
creator: %s
---

# %s
`, item.ID, item.Type, item.Created.Format(time.RFC3339), item.Creator, item.Title)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing work item: %w", err)
	}

	return item, nil
}

// Read loads a work item by ID, searching across available/, claimed/, and done/.
func Read(root, id string) (*Item, error) {
	dirs := []string{
		filepath.Join(root, "work", "available"),
		filepath.Join(root, "work", "claimed"),
		filepath.Join(root, "work", "done"),
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, id+".md") || strings.HasPrefix(name, id+".md.") {
				path := filepath.Join(dir, name)
				return readFile(path)
			}
		}
	}

	return nil, fmt.Errorf("work item %s not found", id)
}

// List returns all work items in available/.
func List(root string) ([]*Item, error) {
	dir := filepath.Join(root, "work", "available")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading available/: %w", err)
	}

	var items []*Item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		item, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.Before(items[j].Created)
	})

	return items, nil
}

func readFile(path string) (*Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(data))
}

// Parse extracts an Item from markdown content with YAML frontmatter.
func Parse(content string) (*Item, error) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}

	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return nil, fmt.Errorf("unterminated YAML frontmatter")
	}

	fm := content[4 : 4+end]
	body := content[4+end+5:] // skip past closing "---\n"

	item := &Item{Body: body}

	for _, line := range strings.Split(fm, "\n") {
		key, val, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "id":
			item.ID = val
		case "type":
			item.Type = val
		case "created":
			t, err := time.Parse(time.RFC3339, val)
			if err == nil {
				item.Created = t
			}
		case "creator":
			item.Creator = val
		}
	}

	// Extract title from first markdown heading
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			item.Title = strings.TrimPrefix(line, "# ")
			break
		}
	}

	if item.ID == "" {
		return nil, fmt.Errorf("work item missing id")
	}

	return item, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

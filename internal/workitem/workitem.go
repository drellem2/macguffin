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
	Depends []string // IDs of items that must be done before this is available
	Tags    []string // free-form labels
	Repo     string   // repository path where this item was created (optional breadcrumb)
	Assignee string   // person assigned to this item (optional)
	Title    string
	Body     string // everything after frontmatter (raw markdown)
}

// CreateOption configures optional fields on a new work item.
type CreateOption func(*Item)

// WithRepo sets the repository path on a work item.
func WithRepo(repo string) CreateOption {
	return func(item *Item) {
		item.Repo = repo
	}
}

// WithAssignee sets the assignee on a work item.
func WithAssignee(assignee string) CreateOption {
	return func(item *Item) {
		item.Assignee = assignee
	}
}

// GenerateID produces a short hash ID with the given prefix (e.g. "mg-a3f0").
func GenerateID(prefix, title string, created time.Time) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte(created.Format(time.RFC3339Nano)))
	sum := h.Sum(nil)
	return fmt.Sprintf("%s%x", prefix, sum[:2])
}

// Create writes a new work item file. Items with no dependencies go to
// available/; items with unmet dependencies go to pending/.
func Create(root, prefix, typ, title string, depends []string, opts ...CreateOption) (*Item, error) {
	now := time.Now().UTC()
	id := GenerateID(prefix, title, now)

	creator := currentUser()

	item := &Item{
		ID:      id,
		Type:    typ,
		Created: now,
		Creator: creator,
		Depends: depends,
		Title:   title,
	}

	for _, opt := range opts {
		opt(item)
	}

	// Items with dependencies start in pending/; others in available/
	subdir := "available"
	if len(depends) > 0 {
		subdir = "pending"
	}
	dir := filepath.Join(root, "work", subdir)
	path := filepath.Join(dir, id+".md")

	content := Render(item)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing work item: %w", err)
	}

	return item, nil
}

// Render serialises an Item back to markdown with YAML frontmatter.
func Render(item *Item) string {
	depsLine := "[]"
	if len(item.Depends) > 0 {
		depsLine = "[" + strings.Join(item.Depends, ", ") + "]"
	}

	repoLine := ""
	if item.Repo != "" {
		repoLine = fmt.Sprintf("repo: %s\n", item.Repo)
	}

	assigneeLine := ""
	if item.Assignee != "" {
		assigneeLine = fmt.Sprintf("assignee: %s\n", item.Assignee)
	}

	tagsLine := ""
	if len(item.Tags) > 0 {
		tagsLine = fmt.Sprintf("tags: [%s]\n", strings.Join(item.Tags, ", "))
	}

	body := item.Body
	// If body is empty or doesn't start with the title heading, generate it
	if body == "" || !strings.Contains(body, "# "+item.Title) {
		body = fmt.Sprintf("\n# %s\n", item.Title)
		if item.Body != "" {
			body += item.Body
		}
	}

	return fmt.Sprintf("---\nid: %s\ntype: %s\ncreated: %s\ncreator: %s\ndepends: %s\n%s%s%s---\n%s",
		item.ID, item.Type, item.Created.Format(time.RFC3339), item.Creator, depsLine, tagsLine, repoLine, assigneeLine, body)
}

// FindPath returns the filesystem path and status directory for a work item by ID.
func FindPath(root, id string) (path string, status string, err error) {
	states := []string{"available", "claimed", "done", "pending"}

	for _, state := range states {
		dir := filepath.Join(root, "work", state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, id+".md") || strings.HasPrefix(name, id+".md.") {
				return filepath.Join(dir, name), state, nil
			}
		}
	}

	// Search archive partitions
	archiveRoot := filepath.Join(root, "work", "archive")
	partitions, err2 := os.ReadDir(archiveRoot)
	if err2 == nil {
		for _, p := range partitions {
			if !p.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(archiveRoot, p.Name()))
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if strings.HasPrefix(name, id+".md") {
					return filepath.Join(archiveRoot, p.Name(), name), "archived", nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("work item %s not found", id)
}

// Read loads a work item by ID, searching across available/, claimed/, done/, pending/, and archive/.
func Read(root, id string) (*Item, error) {
	dirs := []string{
		filepath.Join(root, "work", "available"),
		filepath.Join(root, "work", "claimed"),
		filepath.Join(root, "work", "done"),
		filepath.Join(root, "work", "pending"),
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

	// Search archive partitions
	archiveRoot := filepath.Join(root, "work", "archive")
	partitions, err := os.ReadDir(archiveRoot)
	if err == nil {
		for _, p := range partitions {
			if !p.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(archiveRoot, p.Name()))
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if strings.HasPrefix(name, id+".md") {
					return readFile(filepath.Join(archiveRoot, p.Name(), name))
				}
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
		case "depends":
			item.Depends = parseDependsList(val)
		case "tags":
			item.Tags = parseDependsList(val) // same [a, b] format
		case "repo":
			item.Repo = val
		case "assignee":
			item.Assignee = val
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

// parseDependsList parses a YAML-style list like "[gt-aaa, gt-bbb]" into a slice.
func parseDependsList(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	var deps []string
	for _, d := range strings.Split(val, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			deps = append(deps, d)
		}
	}
	return deps
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

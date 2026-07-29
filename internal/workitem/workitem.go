package workitem

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// Item represents a work item with YAML frontmatter fields.
type Item struct {
	ID       string
	Type     string
	Created  time.Time
	Creator  string
	Depends  []string // IDs of items that must be done before this is available
	Tags     []string // free-form labels
	Repo     string   // repository path where this item was created (optional breadcrumb)
	Assignee string   // person assigned to this item (optional)
	Priority string   // priority level: low, medium, high (optional, default: medium)
	Branch   string   // branch name associated with this item (optional)
	Budget   *int     // estimated token budget for this item (optional, nil = unset)
	Title    string
	Body     string    // everything after frontmatter (raw markdown)
	Mtime    time.Time // file modification time; zero when parsed from a string
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

// WithPriority sets the priority on a work item.
func WithPriority(priority string) CreateOption {
	return func(item *Item) {
		item.Priority = priority
	}
}

// WithBranch sets the branch name on a work item.
func WithBranch(branch string) CreateOption {
	return func(item *Item) {
		item.Branch = branch
	}
}

// WithBudget sets the token budget on a work item.
func WithBudget(budget int) CreateOption {
	return func(item *Item) {
		item.Budget = &budget
	}
}

// WithBody sets the body on a work item.
func WithBody(body string) CreateOption {
	return func(item *Item) {
		item.Body = body
	}
}

// WithTags sets tags on a work item.
func WithTags(tags []string) CreateOption {
	return func(item *Item) {
		item.Tags = tags
	}
}

// maxMintAttempts bounds the remint loop in Create. Each attempt draws a fresh
// ID from a 65,536-value space, so even a store holding thousands of items
// exhausts this only if the space is essentially full — at which point failing
// loudly beats spinning.
const maxMintAttempts = 64

// nowFunc is the clock Create mints from. Overridden in tests to pin the
// timestamp and thereby force an ID collision, which is otherwise a 1-in-65,536
// event.
var nowFunc = func() time.Time { return time.Now().UTC() }

// GenerateID produces a short hash ID with the given prefix (e.g. "mg-a3f0").
func GenerateID(prefix, title string, created time.Time) string {
	return generateID(prefix, title, created, 0)
}

// generateID is GenerateID with a collision-breaking nonce mixed into the hash
// input. The ID is a DETERMINISTIC HASH of (title, created), not a random draw:
// re-deriving it from the same inputs returns the same ID forever, so a retry
// loop that does not perturb the input never terminates. The nonce is that
// perturbation. nonce==0 reproduces the historical ID exactly, so every ID ever
// minted stays derivable.
func generateID(prefix, title string, created time.Time, nonce int) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte(created.Format(time.RFC3339Nano)))
	if nonce > 0 {
		fmt.Fprintf(h, "\x00nonce=%d", nonce)
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%s%x", prefix, sum[:2])
}

// Create writes a new work item file. Items with no dependencies go to
// available/; items with unmet dependencies go to pending/.
//
// Minting is collision-safe in two independent layers, because the short ID
// space is small enough that collisions are routine rather than theoretical:
//
//  1. The candidate ID is rejected if it already names an item ANYWHERE in the
//     store — every active directory, every archive partition, AND loose
//     records directly under archive/, none of which O_EXCL on one directory
//     would see. Resolve is total over the store precisely so this claim holds:
//     a gap there would let a new item be born a silent alias of a twin the
//     resolver could not see.
//  2. The file is created with O_EXCL. os.WriteFile truncates, so the previous
//     implementation would silently DESTROY a live item in available/ or
//     pending/ whose ID the new item happened to draw. O_EXCL also closes the
//     TOCTOU window that a bare pre-existence check would leave open.
//
// On either rejection the ID is reminted with an incremented nonce.
func Create(root, prefix, typ, title string, depends []string, opts ...CreateOption) (*Item, error) {
	now := nowFunc()
	creator := currentUser()

	// Placement is decided against the RESOLVED state of every dependency, not
	// against the mere presence of a depends list. Parking an item in pending/
	// because it names a parent — when that parent finished last week, or is
	// shelved and will never finish — is what stranded three items in a day:
	// pending/ is only swept by Done, so a dependent filed after its parent
	// completed waits on an unrelated completion, and a dependent of a shelved
	// parent waits on nothing at all. See depends.go.
	deps, err := classifyDeps(root, depends)
	if err != nil {
		return nil, err
	}
	subdir, _ := placeForDeps(deps)
	dir := filepath.Join(root, "work", subdir)

	for nonce := 0; nonce < maxMintAttempts; nonce++ {
		id := generateID(prefix, title, now, nonce)

		// Layer 1: whole-store uniqueness.
		matches, err := Resolve(root, id)
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			continue
		}

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

		// One fact, one marker: a workflow tag and the body's leading
		// `workflow:` line must agree, and the body is the source of truth
		// (see workflowmarker.go). Refuses BEFORE any write, so a rejected
		// filing leaves nothing behind.
		// A filing authors both markers from scratch, so writeShape is the zero
		// value: nothing to grandfather, full enforcement.
		tags, err := reconcileWorkflowMarkers(composeBody(item), item.Tags, writeShape{})
		if err != nil {
			return nil, err
		}
		item.Tags = tags

		// Layer 2: atomic, non-truncating create.
		path := filepath.Join(dir, id+".md")
		if err := writeNew(path, Render(item)); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue // lost a race; remint
			}
			return nil, ioErr(fmt.Sprintf("could not create work item: %s", fsErrText(err)))
		}

		event.Emit(root, "work.created", map[string]string{
			"item_id":   id,
			"to_status": subdir,
			"actor":     actorFor(item),
		})

		return item, nil
	}

	return nil, &mgerr.Error{
		Category: mgerr.CatInternal,
		Code:     "id_exhausted",
		Message:  fmt.Sprintf("could not mint an unused work item ID after %d attempts.", maxMintAttempts),
		Hint:     "The short-ID space appears to be exhausted. Archive or prune old work items.",
	}
}

// writeNew creates path exclusively — it never truncates an existing file.
// The fs.ErrExist it returns on collision is what drives the remint loop.
func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Render serialises an Item back to markdown with YAML frontmatter.
// composeBody returns the body EXACTLY as it will be persisted, including the
// "# Title" heading that is generated when the body does not already carry one.
//
// This is split out of Render because the stored body is not always the body the
// caller supplied, and anything validating the body has to inspect the stored
// form or it is checking a string that never hits disk. In particular the
// gh-issue carrier block sits at the top of the body and the generated heading
// is inserted above it — see reconcileWorkflowMarkers in workflowmarker.go.
func composeBody(item *Item) string {
	body := item.Body
	// If body is empty or doesn't start with the title heading, generate it
	if body == "" || !strings.Contains(body, "# "+item.Title) {
		body = fmt.Sprintf("\n# %s\n", item.Title)
		if item.Body != "" {
			body += item.Body
		}
	}
	return body
}

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

	priorityLine := ""
	if item.Priority != "" {
		priorityLine = fmt.Sprintf("priority: %s\n", item.Priority)
	}

	branchLine := ""
	if item.Branch != "" {
		branchLine = fmt.Sprintf("branch: %s\n", item.Branch)
	}

	budgetLine := ""
	if item.Budget != nil {
		budgetLine = fmt.Sprintf("budget: %d\n", *item.Budget)
	}

	tagsLine := ""
	if len(item.Tags) > 0 {
		tagsLine = fmt.Sprintf("tags: [%s]\n", strings.Join(item.Tags, ", "))
	}

	body := composeBody(item)

	return fmt.Sprintf("---\nid: %s\ntype: %s\ncreated: %s\ncreator: %s\ndepends: %s\n%s%s%s%s%s%s---\n%s",
		item.ID, item.Type, item.Created.Format(time.RFC3339), item.Creator, depsLine, tagsLine, repoLine, assigneeLine, priorityLine, branchLine, budgetLine, body)
}

// FindPath returns the filesystem path and status directory for a work item by
// ID, erroring if the ID is ambiguous. See Resolve.
func FindPath(root, id string) (path string, status string, err error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return "", "", err
	}
	return m.Path, m.Status, nil
}

// Read loads a work item by ID from anywhere in the store, erroring if the ID
// is ambiguous. See Resolve.
func Read(root, id string) (*Item, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	return readFile(m.Path)
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
	item, err := Parse(string(data))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil {
		item.Mtime = info.ModTime()
	}
	return item, nil
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
		case "priority":
			item.Priority = val
		case "branch":
			item.Branch = val
		case "budget":
			if n, err := strconv.Atoi(val); err == nil {
				item.Budget = &n
			}
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

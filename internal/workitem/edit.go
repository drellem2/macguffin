package workitem

import (
	"fmt"
	"os"
	"strings"
)

// UpdateField represents which field to update and how.
type UpdateField struct {
	Title      *string  // replace title
	Body       *string  // replace body
	Type       *string  // replace type
	Repo       *string  // replace repo
	Depends    []string // replace all dependencies (nil = no change, empty = clear)
	AddDepends []string // append to existing dependencies
	RmDepends  []string // remove from existing dependencies
	Tags       []string // replace all tags (nil = no change, empty = clear)
	AddTags    []string // append to existing tags
	RmTags     []string // remove from existing tags
}

// Update applies field changes to an existing work item and writes it back.
func Update(root, id string, fields UpdateField) (*Item, error) {
	itemPath, _, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(itemPath)
	if err != nil {
		return nil, fmt.Errorf("reading work item: %w", err)
	}

	item, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing work item: %w", err)
	}

	// Apply field updates
	if fields.Title != nil {
		oldTitle := item.Title
		item.Title = *fields.Title
		// Update title in body
		if oldTitle != "" && strings.Contains(item.Body, "# "+oldTitle) {
			item.Body = strings.Replace(item.Body, "# "+oldTitle, "# "+item.Title, 1)
		}
	}

	if fields.Body != nil {
		item.Body = *fields.Body
	}

	if fields.Type != nil {
		item.Type = *fields.Type
	}

	if fields.Repo != nil {
		item.Repo = *fields.Repo
	}

	// Dependencies: full replacement takes precedence over incremental
	if fields.Depends != nil {
		item.Depends = fields.Depends
	} else {
		if len(fields.AddDepends) > 0 {
			item.Depends = addUnique(item.Depends, fields.AddDepends)
		}
		if len(fields.RmDepends) > 0 {
			item.Depends = removeAll(item.Depends, fields.RmDepends)
		}
	}

	// Tags: full replacement takes precedence over incremental
	if fields.Tags != nil {
		item.Tags = fields.Tags
	} else {
		if len(fields.AddTags) > 0 {
			item.Tags = addUnique(item.Tags, fields.AddTags)
		}
		if len(fields.RmTags) > 0 {
			item.Tags = removeAll(item.Tags, fields.RmTags)
		}
	}

	content := Render(item)
	if err := os.WriteFile(itemPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing work item: %w", err)
	}

	return item, nil
}

// addUnique appends values to a slice, skipping duplicates.
func addUnique(existing, add []string) []string {
	set := make(map[string]bool, len(existing))
	for _, v := range existing {
		set[v] = true
	}
	result := append([]string{}, existing...)
	for _, v := range add {
		if !set[v] {
			result = append(result, v)
			set[v] = true
		}
	}
	return result
}

// removeAll returns a new slice with specified values removed.
func removeAll(existing, remove []string) []string {
	rm := make(map[string]bool, len(remove))
	for _, v := range remove {
		rm[v] = true
	}
	var result []string
	for _, v := range existing {
		if !rm[v] {
			result = append(result, v)
		}
	}
	return result
}

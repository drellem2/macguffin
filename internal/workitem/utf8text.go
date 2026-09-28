package workitem

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// checkNewItemUTF8 refuses a filing whose text is not valid UTF-8 (see
// mgerr.CheckUTF8, drellem2/macguffin#41). Everything on a new item is text
// this write introduces, so every free-text field is checked.
func checkNewItemUTF8(item *Item) error {
	for _, f := range []struct{ name, val string }{
		{"title", item.Title},
		{"type", item.Type},
		{"body", item.Body},
		{"assignee", item.Assignee},
		{"repo", item.Repo},
		{"branch", item.Branch},
	} {
		if err := mgerr.CheckUTF8(f.name, f.val); err != nil {
			return err
		}
	}
	if err := checkTagsUTF8("depends", item.Depends); err != nil {
		return err
	}
	return checkTagsUTF8("tags", item.Tags)
}

// checkUpdateUTF8 refuses an edit that would introduce text that is not valid
// UTF-8. Only the values the caller supplied are checked — never the stored
// body — so an item that is already damaged stays editable, and a full --body
// replacement can repair it.
func checkUpdateUTF8(fields UpdateField) error {
	for _, f := range []struct {
		name string
		val  *string
	}{
		{"title", fields.Title},
		{"body", fields.Body},
		{"append-body", fields.AppendBody},
		{"type", fields.Type},
		{"assignee", fields.Assignee},
		{"repo", fields.Repo},
	} {
		if f.val == nil {
			continue
		}
		if err := mgerr.CheckUTF8(f.name, *f.val); err != nil {
			return err
		}
	}
	// --rm-depends and --rm-tags are deliberately NOT checked: they introduce
	// no text, and removing a damaged entry by its exact bytes is how an item
	// written before this check gets repaired.
	for _, l := range []struct {
		name string
		vals []string
	}{
		{"depends", fields.Depends},
		{"add-depends", fields.AddDepends},
		{"tags", fields.Tags},
		{"add-tags", fields.AddTags},
	} {
		if err := checkTagsUTF8(l.name, l.vals); err != nil {
			return err
		}
	}
	return nil
}

// checkTagsUTF8 names the offending entry of a list field (tags, depends) by
// index, so the byte offset points into one entry rather than into a joined
// string the caller never wrote.
func checkTagsUTF8(field string, tags []string) error {
	for i, tag := range tags {
		if err := mgerr.CheckUTF8(fmt.Sprintf("%s[%d]", field, i), tag); err != nil {
			return err
		}
	}
	return nil
}

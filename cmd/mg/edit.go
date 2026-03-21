package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	editTitle      string
	editBody       string
	editType       string
	editRepo       string
	editDepends    string
	editAddDepends string
	editRmDepends  string
	editTags       string
	editAddTags    string
	editRmTags     string
	editAssignee   string
)

var editCmd = &cobra.Command{
	Use:   "edit ID [flags]",
	Short: "Update fields on an existing work item",
	Long: `Update fields on an existing work item.

Use --title, --body, --type, --repo, --assignee to replace fields directly.
Use --depends to replace all dependencies, or --add-depends / --rm-depends for incremental changes.
Use --tags to replace all tags, or --add-tags / --rm-tags for incremental changes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		fields := workitem.UpdateField{}
		changed := false

		if cmd.Flags().Changed("title") {
			fields.Title = &editTitle
			changed = true
		}
		if cmd.Flags().Changed("body") {
			fields.Body = &editBody
			changed = true
		}
		if cmd.Flags().Changed("type") {
			fields.Type = &editType
			changed = true
		}
		if cmd.Flags().Changed("repo") {
			fields.Repo = &editRepo
			changed = true
		}
		if cmd.Flags().Changed("depends") {
			fields.Depends = splitCSV(editDepends)
			changed = true
		}
		if cmd.Flags().Changed("add-depends") {
			fields.AddDepends = splitCSV(editAddDepends)
			changed = true
		}
		if cmd.Flags().Changed("rm-depends") {
			fields.RmDepends = splitCSV(editRmDepends)
			changed = true
		}
		if cmd.Flags().Changed("tags") {
			fields.Tags = splitCSV(editTags)
			changed = true
		}
		if cmd.Flags().Changed("add-tags") {
			fields.AddTags = splitCSV(editAddTags)
			changed = true
		}
		if cmd.Flags().Changed("rm-tags") {
			fields.RmTags = splitCSV(editRmTags)
			changed = true
		}
		if cmd.Flags().Changed("assignee") {
			fields.Assignee = &editAssignee
			changed = true
		}

		if !changed {
			return fmt.Errorf("no fields specified; use --title, --body, --type, --assignee, --depends, --tags, etc.")
		}

		item, err := workitem.Update(root, args[0], fields)
		if err != nil {
			return err
		}

		fmt.Printf("Updated %s: %s\n", item.ID, item.Title)
		return nil
	},
}

func init() {
	editCmd.Flags().StringVar(&editTitle, "title", "", "new title")
	editCmd.Flags().StringVar(&editBody, "body", "", "new body (markdown)")
	editCmd.Flags().StringVar(&editType, "type", "", "new type")
	editCmd.Flags().StringVar(&editRepo, "repo", "", "new repo path")
	editCmd.Flags().StringVar(&editDepends, "depends", "", "replace all dependencies (comma-separated)")
	editCmd.Flags().StringVar(&editAddDepends, "add-depends", "", "add dependencies (comma-separated)")
	editCmd.Flags().StringVar(&editRmDepends, "rm-depends", "", "remove dependencies (comma-separated)")
	editCmd.Flags().StringVar(&editTags, "tags", "", "replace all tags (comma-separated)")
	editCmd.Flags().StringVar(&editAddTags, "add-tags", "", "add tags (comma-separated)")
	editCmd.Flags().StringVar(&editRmTags, "rm-tags", "", "remove tags (comma-separated)")
	editCmd.Flags().StringVar(&editAssignee, "assignee", "", "person to assign this item to")
}

// splitCSV splits a comma-separated string into trimmed non-empty parts.
func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	if result == nil {
		return []string{}
	}
	return result
}

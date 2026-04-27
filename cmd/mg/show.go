package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/spend"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show a work item by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Read(root, args[0])
		if err != nil {
			return err
		}

		status, err := workitem.Status(root, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("%-10s %s\n", "ID:", item.ID)
		fmt.Printf("%-10s %s\n", "Type:", item.Type)
		fmt.Printf("%-10s %s\n", "Status:", status)
		fmt.Printf("%-10s %s\n", "Created:", item.Created.Format("2006-01-02 15:04:05Z"))
		fmt.Printf("%-10s %s\n", "Creator:", item.Creator)
		if item.Assignee != "" {
			fmt.Printf("%-10s %s\n", "Assignee:", item.Assignee)
		}
		if item.Priority != "" {
			fmt.Printf("%-10s %s\n", "Priority:", item.Priority)
		}
		if item.Branch != "" {
			fmt.Printf("%-10s %s\n", "Branch:", item.Branch)
		}
		if item.Budget != nil {
			fmt.Printf("%-10s %s tokens\n", "Budget:", formatThousands(*item.Budget))
			if recs, err := spend.ReadItem(root, item.ID); err == nil && len(recs) > 0 {
				spent := 0
				for _, r := range recs {
					spent += r.Input + r.CacheRead + r.CacheCreate + r.Output
				}
				line := fmt.Sprintf("%s tokens (%d%% of budget)", formatThousands(spent), pctOfBudget(spent, *item.Budget))
				if *item.Budget > 0 && spent > *item.Budget {
					line += " ⚠"
				}
				fmt.Printf("%-10s %s\n", "Spent:", line)
			}
		}
		if len(item.Tags) > 0 {
			fmt.Printf("%-10s %s\n", "Tags:", strings.Join(item.Tags, ", "))
		}
		if len(item.Depends) > 0 {
			fmt.Printf("%-10s %s\n", "Depends:", strings.Join(item.Depends, ", "))
		}
		if item.Repo != "" {
			fmt.Printf("%-10s %s\n", "Repo:", item.Repo)
		}
		fmt.Printf("%-10s %s\n", "Title:", item.Title)

		if item.Body != "" {
			fmt.Printf("\n%s", item.Body)
		}

		return nil
	},
}

// pctOfBudget rounds spent/budget to a whole percentage. A zero budget is
// reported as 0% to avoid divide-by-zero — the ⚠ marker still fires when spent>0.
func pctOfBudget(spent, budget int) int {
	if budget <= 0 {
		return 0
	}
	return (spent * 100) / budget
}

// formatThousands renders an integer with comma separators (e.g. 200000 → "200,000").
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

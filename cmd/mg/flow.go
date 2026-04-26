package main

import (
	"os"
	"time"

	"github.com/drellem2/macguffin/internal/flow"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	flowGroupBy         string
	flowAgeDistribution bool
	flowRepo            string
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Show throughput and bottleneck metrics across work items",
	Long: `Show throughput and bottleneck metrics across work items.

By default groups by status (available, claimed, pending, done) and reports
per-group counts, median age, and items completed in the last 7 days. The
group with the highest median-age-to-throughput ratio is flagged as the
likely bottleneck.

Use --group-by to slice the work along a different axis:
  status            (default) lifecycle state
  repo              repository
  tag               by tag (items with multiple tags appear in multiple rows)
  tag:<value>       items containing <value> in their tags, sub-grouped by status
  assignee          by assignee
  priority          by priority
  age               by age bucket (<24h, 24h-7d, 7d-30d, >30d)

Use --age-distribution to append a histogram of items by age.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		res, err := flow.Compute(flow.Options{
			Root:    root,
			GroupBy: flowGroupBy,
			Repo:    flowRepo,
		})
		if err != nil {
			return err
		}

		var dist *flow.AgeDistribution
		if flowAgeDistribution {
			records, err := flow.LoadAllRecords(root, time.Time{})
			if err != nil {
				return err
			}
			if flowRepo != "" {
				records = flow.FilterByRepo(records, flowRepo)
			}
			d := flow.ComputeAgeDistribution(records)
			dist = &d
		}

		flow.Render(os.Stdout, res, dist)
		return nil
	},
}

func init() {
	flowCmd.Flags().StringVar(&flowGroupBy, "group-by", "status", "axis to group by: status, repo, tag, tag:<value>, assignee, priority, age")
	flowCmd.Flags().BoolVar(&flowAgeDistribution, "age-distribution", false, "append a histogram of items by age")
	flowCmd.Flags().StringVar(&flowRepo, "repo", "", "filter to items whose repo contains this substring")
}

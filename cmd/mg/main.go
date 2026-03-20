package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

var rootCmd = &cobra.Command{
	Use:           "mg",
	Short:         "macguffin work-item tracker",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetOut(os.Stderr)
		cmd.Println(cmd.UsageString())
		return fmt.Errorf("a command is required")
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("mg %s\n", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(claimCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(mailCmd)
	rootCmd.AddCommand(reapCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(logCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

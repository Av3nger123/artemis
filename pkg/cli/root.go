package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/exp/slog"
)

func Init() {
	// A failed test run is reported by RunE as an error; cobra must not answer
	// it with usage text, and main prints the message itself.
	RootCmd.SilenceUsage = true
	RootCmd.SilenceErrors = true

	// Parse command for validating yaml file
	RootCmd.AddCommand(parseCmd)
	parseCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := parseCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}

	// Test command to actually test all apis
	RootCmd.AddCommand(testCmd)
	testCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := testCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
	testCmd.Flags().StringP("log", "l", "", "Write a JSON log of the run to this file (default: no log file)")
	testCmd.Flags().StringP("env", "e", ".env", "Path to the env file")

	RootCmd.AddCommand(generateCmd)
	generateCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := testCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
}

var RootCmd = &cobra.Command{
	Use:   "artemis",
	Short: "Artemis is an API testing tool",
	Long: `Artemis is a comprehensive CLI tool for API testing. 
    It provides functionalities to parse and validate YAML files containing API test cases.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Use 'artemis parse <file_path>' to parse and validate a yaml file")
	},
}

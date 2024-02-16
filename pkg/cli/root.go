package cli

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

func Init() {

	// Parse command for validating yaml file
	RootCmd.AddCommand(parseCmd)
	parseCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := parseCmd.MarkFlagRequired("file"); err != nil {
		log.Fatalf("Error marking flag as required: %v", err)
	}

	// Test command to actually test all apis
	RootCmd.AddCommand(testCmd)
	testCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := testCmd.MarkFlagRequired("file"); err != nil {
		log.Fatalf("Error marking flag as required: %v", err)
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

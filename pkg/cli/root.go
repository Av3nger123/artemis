package cli

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

func Init() {
	RootCmd.AddCommand(parseCmd)
	parseCmd.Flags().StringP("file", "f", "", "Path to YAML file")
	if err := parseCmd.MarkFlagRequired("file"); err != nil {
		log.Fatalf("Error marking flag as required: %v", err)
	}
}

var RootCmd = &cobra.Command{
	Use:   "artemis",
	Short: "Artemis is an api testing tool",
	Long:  "Artemis is an api testing tool",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Use 'artemis parse <file_path>' to parse and validate a yaml file")
	},
}

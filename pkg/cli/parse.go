package cli

import (
	"artemis/pkg/shared"
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

var parseCmd = &cobra.Command{
	Use:   "parse",
	Short: "Parse YAML file",
	Long:  "Parse YAML file and display the parsed configuration",
	Run: func(cmd *cobra.Command, args []string) {
		config := parseYAMLFile(cmd)
		fmt.Println(config)
	},
}

func parseYAMLFile(cmd *cobra.Command) shared.APIConfig {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		log.Fatalf("error getting file path: %v", err)
	}

	config, err := shared.ParseYAMLFile(filePath)
	if err != nil {
		log.Fatalf("error parsing YAML file: %v", err)
	}

	return config
}

package cli

import (
	"artemis/pkg/shared"
	"artemis/pkg/shared/models"
	"fmt"
	"log/slog"

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

func parseYAMLFile(cmd *cobra.Command) models.Config {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		slog.Error("error getting file path")
	}

	config, err := shared.ParseYAMLFile(filePath)
	if err != nil {
		slog.Error("error parsing YAML file", "error", err)
	}
	return config
}

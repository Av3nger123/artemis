package cli

import (
	"artemis/pkg/executor"
	"artemis/pkg/shared"
	"artemis/pkg/shared/models"
	"fmt"

	"github.com/spf13/cobra"
)

var parseCmd = &cobra.Command{
	Use:   "parse",
	Short: "Parse YAML file",
	Long:  "Parse YAML file and display the parsed configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := parseYAMLFile(cmd)
		if err != nil {
			return err
		}
		fmt.Println(config)
		return nil
	},
}

// parseYAMLFile reads the --file flag and parses it. Both a missing flag and an
// unparseable file are returned as errors: the caller is a RunE, so an
// unreadable scenario fails the command instead of becoming an empty run.
//
// The step types a scenario is validated against are the ones the registry
// holds, so `artemis parse` accepts exactly what `artemis test` can run.
func parseYAMLFile(cmd *cobra.Command) (models.Config, error) {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return models.Config{}, fmt.Errorf("reading --file flag: %w", err)
	}

	config, err := shared.ParseYAMLFile(filePath, executor.Default().Types())
	if err != nil {
		return models.Config{}, fmt.Errorf("parse %s: %w", filePath, err)
	}
	return config, nil
}

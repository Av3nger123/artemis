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
		filePath, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		config, err := loadScenario(filePath)
		if err != nil {
			return err
		}
		fmt.Println(config)
		return nil
	},
}

// loadScenario reads one scenario file and returns it only if artemis
// understands every part of it. The error names the path, because a caller that
// loads a whole folder has nothing else to tell the reader which file it was.
//
// The step types a scenario is validated against are the ones the registry
// holds, so `artemis parse` accepts exactly what `artemis run` can run.
func loadScenario(filePath string) (models.Config, error) {
	config, err := shared.ParseYAMLFile(filePath, executor.Default().Types())
	if err != nil {
		return models.Config{}, fmt.Errorf("parse %s: %w", filePath, err)
	}
	return config, nil
}

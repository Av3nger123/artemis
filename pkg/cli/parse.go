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
	Short: "Check a scenario file without running any of it",
	Long: `Check the scenario file named by --file and run nothing: no request is
sent, no command is run.

A .art file is lexed, parsed and name-checked, and every problem artemis finds
is reported -- all of them, in file order, with the source line echoed. Any
error exits non-zero. A YAML file is validated the way it always was.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		// Two formats, one command, for exactly as long as two formats exist:
		// the YAML branch goes with the rest of the YAML reader.
		if isArtFile(filePath) {
			if _, _, err := loadArt(cmd, filePath); err != nil {
				return err
			}
			// A command whose only job is to tell you something has to say
			// something when the answer is yes.
			fmt.Fprintf(cmd.OutOrStdout(), "%s: ok\n", filePath)
			return nil
		}
		config, err := loadScenario(filePath)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), config)
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

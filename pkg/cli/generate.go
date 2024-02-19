package cli

import (
	"artemis/pkg/shared"
	"log/slog"

	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Convert postman collection json to yaml",
	Long:  "Convert postman collection json to yaml",
	Run: func(cmd *cobra.Command, args []string) {
		filePath, err := cmd.Flags().GetString("file")
		if err != nil {
			slog.Error("error getting file path", "error", err)
		}

		collection, err := shared.ParsePostmanJSON(filePath)
		if err != nil {
			slog.Error("error parsing file path", "error", err)
		}

		if err := shared.ConvertJsonToYaml(collection, filePath); err != nil {
			slog.Error("error while conversion of json file to yaml file", "error", err)
		}
	},
}

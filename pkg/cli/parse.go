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
	Long:  "Parse YAML file",
	Run: func(cmd *cobra.Command, args []string) {
		filePath, _ := cmd.Flags().GetString("file")
		config, err := shared.ParseYAMLFile(filePath)
		if err != nil {
			log.Fatalf("error parsing YAML file: %v", err)
		}
		fmt.Println(config)
	},
}

package cli

import (
	"artemis/pkg/shared"

	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test APIs defined in YAML file",
	Long:  "Test APIs defined in YAML file and display the responses",
	Run: func(cmd *cobra.Command, args []string) {
		file := shared.InitLog("app.log")
		defer file.Close()
		testAPIs(cmd)
	},
}

func testAPIs(cmd *cobra.Command) {
	config := parseYAMLFile(cmd)
	for i := range config.Apis {
		shared.ExecuteAPI(config.Apis[i], &config.Configuration)
	}
}

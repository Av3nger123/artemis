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
		logFilePath, _ := cmd.Flags().GetString("log")
		envFilePath, _ := cmd.Flags().GetString("env")
		file := shared.InitLog(logFilePath)
		shared.InitEnv(envFilePath)
		defer file.Close()
		testAPIs(cmd)
	},
}

func testAPIs(cmd *cobra.Command) {
	config := parseYAMLFile(cmd)

	// Add env variables

	for key, value := range config.Configuration {
		config.Configuration[key] = shared.SubstituteEnvVars(value.(string))
	}

	// Execute Tests
	for i := range config.Apis {
		shared.ExecuteAPI(config.Apis[i], &config.Configuration)
	}
}

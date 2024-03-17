package cli

import (
	"artemis/pkg/shared"
	"artemis/pkg/shared/models"
	"net/http"
	"time"

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

	// Map creation and ENV substitution
	for i := range config.Collection.Variables {
		config.Collection.VariableMap[config.Collection.Variables[i].Key] = shared.SubstituteEnvVars(config.Collection.Variables[i].Value.(string))
	}

	// Execute Tests
	for i := range config.Apis {
		testAPI(config.Apis[i], &config.Collection.VariableMap)
	}
}

func testAPI(api models.API, configVars *map[string]interface{}) {
	i := 0
	var resp *http.Response
	var err error
	var response map[string]interface{}
	retry := api.Meta.RetryEnabled
	for i < api.Meta.MaxRetries || retry {
		i++
		resp, err = shared.LogDecorator(shared.CallAPI)(api, configVars)
		time.Sleep(time.Second * time.Duration(api.Meta.RetryFrequency))

		// exit condition
		retry = (resp != nil && resp.StatusCode != int(api.Test.Status))
		response = shared.ParseResponse(api, resp)
		shared.Logger.Info("API response", "response", response)
		shared.AssertResponse(api, response)
	}
	if err != nil {
		shared.Logger.Warn("Error while executing API", "name", api.Name, "error", err.Error())
		return
	}
}

package cli

import (
	"artemis/pkg/shared"
	"artemis/pkg/shared/models"
	"fmt"
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
	shared.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", config.Collection.Name))

	// Map creation and ENV substitution
	for i := range config.Collection.Variables {
		config.Collection.VariableMap[config.Collection.Variables[i].Key] = shared.SubstituteEnvVars(config.Collection.Variables[i].Value.(string))
	}

	// Execute Tests
	for i := range config.Apis {
		withLogging(testAPI)(config.Apis[i], &config.Collection.VariableMap)
	}
	shared.Logger.Info("Testing ended")
}

func withLogging(testAPIFunc func(api models.API, configVars *map[string]interface{})) func(api models.API, configVars *map[string]interface{}) {
	return func(api models.API, configVars *map[string]interface{}) {
		shared.Logger.Info(fmt.Sprintf("Starting API testing for: %s", api.Name))
		startTime := time.Now()
		testAPIFunc(api, configVars)
		shared.Logger.Info(fmt.Sprintf("API testing completed for: %s, Duration: %v", api.Name, time.Since(startTime)))
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
		shared.Logger.Info("Making API call", "attempt", i, "retry", retry)
		resp, err = shared.LogDecorator(shared.CallAPI)(api, configVars)
		time.Sleep(time.Second * time.Duration(api.Meta.RetryFrequency))
		shared.Logger.Info("API call completed", "attempt", i, "retry", retry)

		// exit condition
		shared.Logger.Info("Asserting API response status code", "attempt", i)
		retry = (resp != nil && resp.StatusCode != int(api.Test.Status))
		if retry {
			continue
		}

		shared.Logger.Info("Parsing API response", "attempt", i)
		response = shared.ParseResponse(api, resp)

		shared.Logger.Info("API response parsed", "attempt", i, "response", response)

		shared.Logger.Info("Asserting API response", "attempt", i)
		retry = shared.AssertResponse(api, response)
		if !retry {
			break
		}
	}
	if err != nil {
		shared.Logger.Warn("Error while executing API", "name", api.Name, "error", err.Error())
		return
	}
}

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

	variableMap := make(map[string]interface{}, 0)

	// map creation and env substitution
	for i := range config.Collection.Variables {
		variableMap[config.Collection.Variables[i].Key] = shared.SubstituteEnvVars(config.Collection.Variables[i].Value.(string))
	}
	// tests execution
	for i := range config.Apis {
		withLogging(testAPI)(config.Apis[i], &variableMap)
	}
	shared.Logger.Info("Testing ended")
}

func withLogging(testAPIFunc func(api models.API, configVars *map[string]interface{})) func(api models.API, configVars *map[string]interface{}) {
	return func(api models.API, configVars *map[string]interface{}) {
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
	for i < api.Meta.MaxRetries {
		i++
		resp, err = shared.LogDecorator(shared.CallAPI)(api, configVars)
		time.Sleep(time.Second * time.Duration(api.Meta.RetryFrequency))
		if resp != nil && resp.StatusCode != int(api.Test.Status) {
			fmt.Println(resp.StatusCode, api.Test.Status)
			continue
		}
		response = shared.ParseResponse(api, resp)
		assert := shared.AssertResponse(api, response)
		if assert {
			break
		}
	}
	if err != nil {
		shared.Logger.Warn("Error while executing API", "name", api.Name, "error", err.Error())
		return
	}
}

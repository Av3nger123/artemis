package cli

import (
	"artemis/pkg/shared"
	"artemis/pkg/shared/api"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
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
		file := logger.InitLog(logFilePath)
		env.InitEnv(envFilePath)
		defer file.Close()
		executeSteps(cmd)
	},
}

func executeSteps(cmd *cobra.Command) {
	config := parseYAMLFile(cmd)
	logger.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", config.Name))

	variableMap := make(map[string]interface{}, 0)

	// map creation and env substitution
	for i := range config.Variables {
		variableMap[config.Variables[i].Name] = shared.SubstituteEnvVars(config.Variables[i].Value)
	}
	// tests execution
	for i := range config.Steps {
		if config.Steps[i].Type == "api" {
			withLogging(testAPI)(config.Steps[i], &variableMap)
		}
	}
	logger.Logger.Info("Testing ended")
}

func withLogging(testAPIFunc func(api models.Step, configVars *map[string]interface{})) func(api models.Step, configVars *map[string]interface{}) {
	return func(api models.Step, configVars *map[string]interface{}) {
		startTime := time.Now()
		testAPIFunc(api, configVars)
		logger.Logger.Info(fmt.Sprintf("API testing completed for: %s, Duration: %v", api.Name, time.Since(startTime)))
	}
}

func testAPI(step models.Step, configVars *map[string]interface{}) {
	i := 0
	var resp *http.Response
	var err error
	var response map[string]interface{}
	for i < step.Retry {
		i++
		resp, err = utils.LogDecorator(api.CallAPI)(step, configVars)
		time.Sleep(time.Second * time.Duration(10))
		if resp != nil && resp.StatusCode != int(step.Response.StatusCode) {
			continue
		}
		response = api.ParseResponse(step, resp)
		assert := api.AssertResponse(step, response)
		if assert {
			break
		}
	}
	if err != nil {
		logger.Logger.Warn("Error while executing API", "name", step.Name, "error", err.Error())
		return
	}
	err = api.ExecuteScripts(response, step, configVars)
	if err != nil {
		logger.Logger.Warn("Error while executing post api scripts", "name", step.Name, "error", err.Error())
	}
}

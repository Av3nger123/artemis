package cli

import (
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/api"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test APIs defined in YAML file",
	Long:  "Test APIs defined in YAML file and display the responses",
	RunE: func(cmd *cobra.Command, args []string) error {
		logFilePath, _ := cmd.Flags().GetString("log")
		envFilePath, _ := cmd.Flags().GetString("env")
		file := logger.InitLog(logFilePath)
		defer file.Close()
		env.InitEnv(envFilePath)
		return runTest(cmd)
	},
}

// runTest loads the scenario, runs it, and returns an error if anything in it
// failed -- that error is what makes the process exit non-zero.
func runTest(cmd *cobra.Command) error {
	config, err := parseYAMLFile(cmd)
	if err != nil {
		return err
	}
	filePath, _ := cmd.Flags().GetString("file")

	run := executeSteps(config, filePath)
	if !run.Passed() {
		return runFailedError(run)
	}
	return nil
}

// runFailedError states in one line what failed. The readable summary is ART-3;
// this is only the reason attached to a non-zero exit.
func runFailedError(run *result.RunResult) error {
	c := run.Counts()
	msg := fmt.Sprintf("%d of %d steps failed", c.Steps.Failed+c.Steps.Errored, c.Steps.Total)
	if c.Steps.Errored > 0 {
		msg += fmt.Sprintf(" (%d errored)", c.Steps.Errored)
	}
	if c.Assertions.Total > 0 {
		msg += fmt.Sprintf(", %d of %d assertions failed", c.Assertions.Failed+c.Assertions.Errored, c.Assertions.Total)
	}
	return errors.New(msg)
}

// executeSteps runs every step of the scenario and returns the outcome. It never
// returns nil: a run with nothing in it is a run that passed, and the caller
// decides what that is worth.
func executeSteps(config models.Config, filePath string) *result.RunResult {
	run := result.NewRun()
	scenario := run.NewScenario(config.Name, filePath)
	logger.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", config.Name))

	variableMap := make(map[string]interface{}, 0)

	// map creation and env substitution
	for i := range config.Variables {
		variableMap[config.Variables[i].Name] = shared.SubstituteEnvVars(config.Variables[i].Value)
	}

	// tests execution
	scenarioStart := time.Now()
	for i := range config.Steps {
		step := config.Steps[i]
		stepResult := scenario.NewStep(step.Name)
		if step.Type != "api" {
			stepResult.Skip(fmt.Sprintf("unsupported step type %q", step.Type))
			logger.Logger.Warn("Skipping step", "name", step.Name, "type", step.Type)
			continue
		}
		testAPI(step, &variableMap, stepResult)
		logger.Logger.Info(fmt.Sprintf("API testing completed for: %s, Duration: %v", step.Name, stepResult.Duration))
	}
	scenario.Finish(time.Since(scenarioStart))

	run.Finish()
	logger.Logger.Info("Testing ended")
	return run
}

// testAPI calls one step's API, retrying while it is still wrong, and records
// what happened on stepResult. The assertions kept are the last attempt's.
func testAPI(step models.Step, configVars *map[string]interface{}, stepResult *result.StepResult) {
	start := time.Now()

	var (
		resp       *http.Response
		callErr    error
		response   map[string]interface{}
		statusOK   bool
		assertions []result.AssertionResult
	)

	for attempt := 1; attempt <= step.Retry; attempt++ {
		stepResult.Attempts = attempt
		response, assertions, statusOK = nil, nil, false

		resp, callErr = utils.LogDecorator(api.CallAPI)(step, configVars)
		if callErr == nil && resp != nil {
			statusOK = resp.StatusCode == step.Response.StatusCode
			if statusOK {
				response = api.ParseResponse(step, resp)
				assertions = api.AssertResponse(step, response)
				if api.AllPassed(assertions) {
					break
				}
			}
		}
		// Sleep between attempts only -- never after the last one. ART-5 makes
		// the delay configurable; today it is a fixed 10s.
		if attempt < step.Retry {
			time.Sleep(time.Second * time.Duration(10))
		}
	}

	if stepResult.Attempts == 0 {
		// retry: 0 (or omitted) means the loop above never ran. A step that was
		// never tried is not a step that passed. ART-5 defaults it to one attempt.
		stepResult.Fail(time.Since(start), errors.New("step made no attempts: retry is 0"))
		return
	}
	if callErr != nil {
		logger.Logger.Warn("Error while executing API", "name", step.Name, "error", callErr.Error())
		stepResult.Fail(time.Since(start), callErr)
		return
	}
	if resp == nil {
		stepResult.Fail(time.Since(start), errors.New("no response from the API"))
		return
	}

	statusAssertion := result.Assertion{
		Step:     step.Name,
		Kind:     "status_code",
		Operator: "equals",
		Expected: step.Response.StatusCode,
		Actual:   resp.StatusCode,
	}
	if statusOK {
		stepResult.Assert(statusAssertion.Pass())
	} else {
		stepResult.Assert(statusAssertion.Fail())
	}
	for _, a := range assertions {
		stepResult.Assert(a)
	}

	if statusOK {
		if err := api.ExecuteScripts(response, step, configVars); err != nil {
			logger.Logger.Warn("Error while executing post api scripts", "name", step.Name, "error", err.Error())
			stepResult.Assert(result.Assertion{Step: step.Name, Kind: "capture", Operator: "exists"}.Errored(err))
		}
	}

	stepResult.Finish(time.Since(start))
}

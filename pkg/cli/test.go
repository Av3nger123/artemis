package cli

import (
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/api"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"errors"
	"fmt"
	"io"
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
		closer, err := logger.InitLog(logFilePath)
		if err != nil {
			return err
		}
		defer closer.Close()
		if err := env.InitEnv(envFilePath); err != nil {
			logger.Logger.Warn("Could not load env file", "path", envFilePath, "error", err.Error())
			// A missing .env is the normal case and not worth a line; a path the
			// user named and that did not load is.
			if cmd.Flags().Changed("env") {
				// err already names the path.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
		}
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

	// cmd.OutOrStdout, not os.Stdout: a test captures the run's output by
	// setting the command's writer.
	rep := report.NewConsole(cmd.OutOrStdout())
	run := executeSteps(config, filePath, rep)
	rep.Summary(run)
	if !run.Passed() {
		return runFailedError(run)
	}
	return nil
}

// runFailedError states in one line what failed. The readable breakdown is the
// console summary on stdout; this is the reason attached to a non-zero exit, and
// it is what survives when only stderr is kept.
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
func executeSteps(config models.Config, filePath string, rep *report.Console) *result.RunResult {
	run := result.NewRun()
	scenario := run.NewScenario(config.Name, filePath)
	rep.Scenario(scenario)
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
		// Unreachable for a parsed scenario -- models.Config.Validate rejects
		// an unknown type before anything runs -- but a caller that builds a
		// Config by hand must not be the one path where a step artemis cannot
		// execute still reports a pass. A skip counts as passed; this does not.
		if !models.IsKnownStepType(step.Type) {
			stepResult.Fail(0, fmt.Errorf("unknown step type %q", step.Type))
			logger.Logger.Error("Unknown step type", "name", step.Name, "type", step.Type)
		} else {
			testAPI(step, &variableMap, stepResult)
			logger.Logger.Info(fmt.Sprintf("API testing completed for: %s, Duration: %v", step.Name, stepResult.Duration))
		}
		// Every step that was reached gets a line, including one artemis could
		// not execute: a step missing from the list is a step nobody questions.
		rep.Step(stepResult)
	}
	scenario.Finish(time.Since(scenarioStart))

	run.Finish()
	logger.Logger.Info("Testing ended")
	return run
}

// sleep is how the retry loop waits between attempts. It is a variable so a
// test can record the delays asked for instead of waiting them out.
var sleep = time.Sleep

// attempt is everything one try of a step produced. A later attempt replaces an
// earlier one whole, so a recorded result is never a mix of two tries.
type attempt struct {
	resp       *http.Response
	err        error
	response   map[string]interface{}
	statusOK   bool
	assertions []result.AssertionResult
}

// passed reports whether this attempt is one worth stopping on: the call was
// made, the status was what the scenario asked for, and every assertion passed.
func (a attempt) passed() bool {
	return a.err == nil && a.resp != nil && a.statusOK && api.AllPassed(a.assertions)
}

// testAPI calls one step's API, retrying while it is still wrong, and records
// what happened on stepResult. The assertions kept are those of the attempt the
// loop stopped on.
func testAPI(step models.Step, configVars *map[string]interface{}, stepResult *result.StepResult) {
	start := time.Now()

	// Resolve the retry policy before anything is sent: a delay that will not
	// parse is the scenario's mistake, not the API's.
	delay, err := step.Retry.Wait()
	if err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	attempts := step.Retry.Attempts()

	var last attempt
	for i := 1; i <= attempts; i++ {
		stepResult.Attempts = i
		last = tryAPI(step, configVars)
		if last.passed() {
			break
		}
		// Between attempts only -- never before the first, never after the last.
		if i < attempts && delay > 0 {
			sleep(delay)
		}
	}

	if last.err != nil {
		logger.Logger.Warn("Error while executing API", "name", step.Name, "error", last.err.Error())
		stepResult.Fail(time.Since(start), last.err)
		return
	}
	if last.resp == nil {
		stepResult.Fail(time.Since(start), errors.New("no response from the API"))
		return
	}

	statusAssertion := result.Assertion{
		Step:     step.Name,
		Kind:     "status_code",
		Operator: "equals",
		Expected: step.Response.StatusCode,
		Actual:   last.resp.StatusCode,
	}
	if last.statusOK {
		stepResult.Assert(statusAssertion.Pass())
	} else {
		stepResult.Assert(statusAssertion.Fail())
	}
	for _, a := range last.assertions {
		stepResult.Assert(a)
	}

	// Scripts read values out of the parsed body, so they need one: a response
	// that was not JSON leaves it nil, and capturing from nil is an error worth
	// saying out loud rather than a path that mysteriously does not resolve.
	if last.statusOK && len(step.Scripts) > 0 {
		if last.response == nil {
			stepResult.Assert(result.Assertion{Step: step.Name, Kind: "capture", Operator: "exists"}.
				Errored(errors.New("no parsed response body to capture from")))
		} else if err := api.ExecuteScripts(last.response, step, configVars); err != nil {
			logger.Logger.Warn("Error while executing post api scripts", "name", step.Name, "error", err.Error())
			stepResult.Assert(result.Assertion{Step: step.Name, Kind: "capture", Operator: "exists"}.Errored(err))
		}
	}

	stepResult.Finish(time.Since(start))
}

// tryAPI makes one request and asserts against it.
func tryAPI(step models.Step, configVars *map[string]interface{}) attempt {
	var a attempt
	// The body is closed by the defer below. bodyclose cannot see through
	// LogDecorator's generic wrapper, hence the suppression.
	a.resp, a.err = utils.LogDecorator(api.CallAPI)(step, configVars) //nolint:bodyclose
	if a.err != nil || a.resp == nil {
		return a
	}
	// Every attempt's body has to be read and closed or the connection is not
	// reused -- with retries, a leak per attempt rather than per step.
	defer func() {
		_, _ = io.Copy(io.Discard, a.resp.Body)
		a.resp.Body.Close()
	}()

	a.statusOK = a.resp.StatusCode == step.Response.StatusCode
	if a.statusOK {
		a.response = api.ParseResponse(step, a.resp)
		a.assertions = api.AssertResponse(step, a.response)
	}
	return a
}

package cli

import (
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"errors"
	"fmt"
	"time"

	// Registers the "api" step type. The runner reaches it through the
	// registry and never names it.
	_ "artemis/pkg/steps/httpstep"

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
	run := executeSteps(executor.Default(), config, filePath, rep)
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
//
// reg is where a step type is turned into something that can run it. The runner
// knows nothing else about what a step is: no URLs, no response bodies, no
// protocol. What it owns is the result tree and the per-step policy -- how many
// attempts, how long between them, how long each may take.
func executeSteps(reg *executor.Registry, config models.Config, filePath string, rep *report.Console) *result.RunResult {
	run := result.NewRun()
	scenario := run.NewScenario(config.Name, filePath)
	rep.Scenario(scenario)
	logger.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", config.Name))

	// The scenario's declared variables, with whatever the steps capture
	// written over the top as the run goes on.
	scope := executor.NewScope()
	for i := range config.Variables {
		scope.Set(config.Variables[i].Name, shared.SubstituteEnvVars(config.Variables[i].Value))
	}

	scenarioStart := time.Now()
	for i := range config.Steps {
		step := config.Steps[i]
		stepResult := scenario.NewStep(step.Name)
		runStep(reg, step, scope, stepResult)
		logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", step.Name, stepResult.Duration))
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

// runStep attempts step until it passes or its attempts run out, and records on
// stepResult what the attempt it stopped on produced.
//
// A later attempt replaces an earlier one whole, so a recorded step is never a
// mix of two tries. An attempt is one worth stopping on when it ran at all and
// every assertion it made passed.
func runStep(reg *executor.Registry, step models.Step, scope executor.Scope, stepResult *result.StepResult) {
	start := time.Now()

	// Resolve the per-step policy before anything runs: a retry delay or a
	// timeout that will not parse is the scenario's mistake, not the service's,
	// and it must not cost a request to find out.
	delay, err := step.Retry.Wait()
	if err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	if err := step.CheckTimeout(); err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	attempts := step.Retry.Attempts()

	var (
		res     *result.StepResult
		lastErr error
	)
	for i := 1; i <= attempts; i++ {
		stepResult.Attempts = i
		res, lastErr = executor.Run(reg, step, scope)
		if lastErr == nil && res != nil && result.AllPassed(res.Assertions) {
			break
		}
		// Between attempts only -- never before the first, never after the last.
		if i < attempts && delay > 0 {
			sleep(delay)
		}
	}

	if lastErr != nil {
		logger.Logger.Warn("Error while executing a step", "name", step.Name, "type", step.Type, "error", lastErr.Error())
		stepResult.Fail(time.Since(start), lastErr)
		return
	}
	if res == nil {
		// An executor that returns neither a result nor an error is broken, and
		// the step it was asked to run must not report a pass because of it.
		stepResult.Fail(time.Since(start), fmt.Errorf("the executor for %q returned nothing", step.Type))
		return
	}

	for _, a := range res.Assertions {
		stepResult.Assert(a)
	}
	stepResult.Finish(time.Since(start))
}

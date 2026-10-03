package cli

import (
	"context"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"fmt"
	"time"

	// Registers the step types. The runner reaches them through the registry
	// and never names them; this is the one place the binary says which types
	// it is built with.
	_ "artemis/pkg/steps/execstep"
	_ "artemis/pkg/steps/httpstep"

	"github.com/spf13/cobra"
)

// testCmd is what `artemis run` replaces: the same run with the path read off a
// flag instead of the command line. It is kept so that every README, script and
// CI job written against the only runner artemis has ever had keeps working, and
// cobra tells its users where to go. Delete it a release after `run` has shipped.
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run the scenarios in one file (deprecated: use `artemis run`)",
	Long: "Run the scenarios in the file named by --file: a .art file, or a YAML one.\n\n" +
		"A .art file is parsed, checked, lowered and run, with the same reporting\n" +
		"and the same exit codes as a YAML one.\n\n" +
		"Deprecated: use `artemis run <path>`, which also takes a folder.",
	Deprecated: "use `artemis run <path>` instead; it takes a folder as well as a file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		closer, err := initRunEnv(cmd)
		if err != nil {
			return err
		}
		defer closer.Close()
		return runTest(cmd)
	},
}

// runTest runs the one file named by --file. It is `artemis run <file>` with the
// path read off a flag, and goes when the deprecated `test` command goes.
//
// Both formats, because it is reportRun: `artemis test -f x.art` is the
// invocation the design names, and it works here for the same reason
// `artemis run x.art` does -- the dispatch is in runFiles and neither command
// knows there are two formats.
func runTest(cmd *cobra.Command) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file flag: %w", err)
	}
	return reportRun(cmd, filePath)
}

// executeSteps runs one scenario as a whole run, for a caller that has a config
// in hand and wants only its outcome. It never returns nil: a run with nothing
// in it is a run that passed, and the caller decides what that is worth.
func executeSteps(reg *executor.Registry, config models.Config, filePath string, rep *report.Console) *result.RunResult {
	run := result.NewRun()
	executeScenario(context.Background(), reg, config, filePath, run, rep)
	run.Finish()
	return run
}

// executeScenario runs every step of config and appends the outcome to run, so
// a run can hold a scenario per file.
//
// reg is where a step type is turned into something that can run it. The runner
// knows nothing else about what a step is: no URLs, no response bodies, no
// protocol. What it owns is the result tree and the per-step policy -- how many
// attempts, how long between them, how long each may take.
func executeScenario(ctx context.Context, reg *executor.Registry, config models.Config, filePath string, run *result.RunResult, rep *report.Console) {
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
		// The line the step was written on, so a step that could not run at all
		// -- and an assertion with no line of its own -- still points somewhere
		// a reader can go (ART-12).
		stepResult.Line = step.Line
		runStep(ctx, reg, step, scope, stepResult)
		logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", step.Name, stepResult.Duration))
		// Every step that was reached gets a line, including one artemis could
		// not execute: a step missing from the list is a step nobody questions.
		rep.Step(stepResult)
	}
	scenario.Finish(time.Since(scenarioStart))
	logger.Logger.Info("Testing ended")
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
func runStep(ctx context.Context, reg *executor.Registry, step models.Step, scope executor.Scope, stepResult *result.StepResult) {
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
		res, lastErr = executor.Run(ctx, reg, step, scope)
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

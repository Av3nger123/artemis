package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// This file is the run path: compile a .art file, then run each scenario in
// it. It sat beside a second, YAML runner in run.go until ART-40, written to
// be read against it line for line; that one is gone, and what it did
// differently is the design's and worth keeping on record.
//
// The assertions come from the step, not from the executor. A YAML step
// carried its checks as data, so httpstep was the only thing that could
// evaluate them. A .art step carries them as expressions, so the executor is
// asked only what it observed -- executor.Observe -- and the step asserts
// against it. Which means the status code stopped being special: the YAML path
// suppressed body checks when the status did not match, and here `expect
// status == 200` is one `expect` among its peers and every one of them is
// evaluated.
//
// A file that does not compile is one errored scenario. runFiles contracts
// that "a file artemis cannot load is recorded as an errored scenario and the
// rest still run", and a file with diagnostics is such a file.

// runArtFile compiles path and runs every scenario in it, appending to run.
//
// The full diagnostics -- the caret gutter, the hints -- go to diagOut, which
// is always stderr: stdout carries documents only, so `artemis run suite
// --report json | jq` keeps working. What goes into the result tree is the
// one-line reason, because report.Console.field trims each line of a value it
// prints and would destroy a caret's alignment.
func runArtFile(ctx context.Context, reg *executor.Registry, file string, run *result.RunResult, rep *report.Console, diagOut io.Writer) {
	scenarios, err := compileArt(file, diagOut)
	if err != nil {
		// No name to give it: the file did not compile, so all anyone knows
		// about these scenarios is where they live.
		scenario := run.NewScenario("", file)
		rep.Scenario(scenario)
		scenario.Fail(0, err)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not load a scenario", "file", file, "error", err.Error())
		return
	}
	for _, sc := range scenarios {
		executeArtScenario(ctx, reg, sc, file, run, rep)
	}
}

// compileArt reads path, runs the front end over it and lowers it.
//
// The front end is frontEnd, which is what `artemis parse` and `artemis fmt`
// use, so a file this accepts is exactly a file `artemis parse` calls ok. The
// error is the one-line reason a non-zero exit carries; the diagnostics
// themselves have already been rendered to diagOut.
func compileArt(path string, diagOut io.Writer) ([]*lower.Scenario, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return nil, err
	}

	tree, info, bag := frontEnd(path, string(src))
	diags := bag.All()
	if len(diags) > 0 {
		files := diag.NewFiles()
		files.Add(path, string(src))
		if err := diag.Terminal(diagOut, files, diags); err != nil {
			return nil, err
		}
	}
	if bag.HasErrors() {
		return nil, firstError(diags)
	}

	scenarios, err := lower.File(tree, info)
	if err != nil {
		// Reachable only for a tree the checker accepted and the lowerer could
		// not read, which is a bug in artemis rather than in the file -- so it
		// names the file and says what it was doing.
		return nil, fmt.Errorf("lowering %s: %w", path, err)
	}
	return scenarios, nil
}

// firstError is the one-line reason attached to a scenario that would not
// compile: the first error in file order, in the `file:line:col: message` form
// every compiler writes and every editor's error parser reads, with a count of
// the rest.
//
// The first rather than a summary of all of them, because this line is what
// survives when a CI log keeps only the last line, and the first error is the
// one to go and fix -- the ones after it are often its consequences.
func firstError(diags []diag.Diagnostic) error {
	var first *diag.Diagnostic
	n := 0
	for i := range diags {
		if diags[i].Severity != diag.Error {
			continue
		}
		n++
		if first == nil {
			first = &diags[i]
		}
	}
	if first == nil {
		// Unreachable: the caller asked only because the bag has errors.
		return fmt.Errorf("the file did not compile")
	}
	where := fmt.Sprintf("%s:%d:%d", first.Span.File, first.Span.Line, first.Span.Col)
	if n == 1 {
		return fmt.Errorf("%s: %s", where, first.Message)
	}
	return fmt.Errorf("%s: %s (and %s)", where, first.Message, plural(n-1, "more error", "more errors"))
}

// executeArtScenario runs every step of one lowered scenario and appends the
// outcome to run, so a run can hold a scenario per file -- and a file can hold
// more than one.
func executeArtScenario(ctx context.Context, reg *executor.Registry, sc *lower.Scenario, file string, run *result.RunResult, rep *report.Console) {
	scenario := run.NewScenario(sc.Name, file)
	rep.Scenario(scenario)
	logger.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", sc.Name))

	// The scenario's declared variables, with whatever the steps capture
	// written over the top as the run goes on. Bind evaluates them in source
	// order, so `var b = a` sees `a` only when `a` is above it.
	//
	// Nothing substitutes {{env.NAME}} here and nothing needs to: the process
	// environment is reached through `env("NAME")`, which is an expression the
	// evaluator resolves like any other.
	scope := executor.NewScope()
	if err := sc.Bind(scope); err != nil {
		// A scenario whose variables do not resolve has nothing worth running,
		// and it gets no steps -- which is the shape of a file that would not
		// load, and is reported the same way.
		scenario.Fail(0, err)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not bind a scenario's variables", "file", file, "error", err.Error())
		return
	}

	scenarioStart := time.Now()
	for _, st := range sc.Steps {
		stepResult := scenario.NewStep(st.Name)
		// The line the step was written on, so a step that could not run at all
		// -- and an assertion with no line of its own -- still points somewhere
		// a reader can go (ART-12).
		stepResult.Line = st.Line
		runArtStep(ctx, reg, st, scope, stepResult)
		logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", st.Name, stepResult.Duration))
		// Every step that was reached gets a line, including one artemis could
		// not execute.
		rep.Step(stepResult)
	}
	scenario.Finish(time.Since(scenarioStart))
	logger.Logger.Info("Testing ended")
}

// runArtStep attempts st until it passes or its attempts run out, and records
// on stepResult what the attempt it stopped on produced.
//
// A later attempt replaces an earlier one whole, so a recorded step is never a
// mix of two tries. An attempt is one worth stopping on when it ran at all and
// every assertion it made -- every `expect`, and every `capture` that had to be
// read -- passed.
func runArtStep(ctx context.Context, reg *executor.Registry, st *lower.Step, scope executor.Scope, stepResult *result.StepResult) {
	start := time.Now()

	// The step's own scope with no roots on it: a URL, a command or a retry
	// count may read a var or an earlier step's capture, and cannot read this
	// step's own status.
	env := &eval.Env{Vars: scope.Vars()}

	// Resolve the whole step before anything runs. Model evaluates the action
	// and the per-step policy, which is where a retry delay or a timeout that
	// will not parse is caught -- the scenario's mistake, not the service's,
	// and it must not cost a request to find out. For a literal the checker
	// caught it already; this is what catches one that came out of an
	// expression. Once, before the first attempt: nothing it evaluates changes
	// between attempts.
	model, err := st.Model(env)
	if err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	delay, err := model.Retry.Wait()
	if err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	if err := model.CheckTimeout(); err != nil {
		stepResult.Fail(time.Since(start), err)
		return
	}
	attempts := model.Retry.Attempts()

	var (
		asserts []result.AssertionResult
		lastErr error
	)
	for i := 1; i <= attempts; i++ {
		stepResult.Attempts = i
		asserts, lastErr = attemptArtStep(ctx, reg, st, model, scope, env)
		if lastErr == nil && result.AllPassed(asserts) {
			break
		}
		// Between attempts only -- never before the first, never after the last.
		if i < attempts && delay > 0 {
			sleep(delay)
		}
	}

	if lastErr != nil {
		logger.Logger.Warn("Error while executing a step", "name", st.Name, "type", st.Type, "error", lastErr.Error())
		stepResult.Fail(time.Since(start), lastErr)
		return
	}
	for _, a := range asserts {
		stepResult.Assert(a)
	}
	stepResult.Finish(time.Since(start))
}

// attemptArtStep makes one attempt: observe, then assert, then capture.
//
// The order is httpstep.Execute's -- the checks, then the captures -- and it is
// what makes a capture that could not be read count against the attempt, so a
// `retry` behaves the same whichever front end wrote the step.
//
// The env the assertions see is a second one, with the step's observation as its
// roots over the same vars. It is built per attempt because the observation is
// what changed; nothing else about the step did.
func attemptArtStep(ctx context.Context, reg *executor.Registry, st *lower.Step, model models.Step, scope executor.Scope, base *eval.Env) ([]result.AssertionResult, error) {
	roots, err := executor.Observe(ctx, reg, model, scope)
	if err != nil {
		return nil, err
	}
	observed := &eval.Env{Roots: roots, Vars: base.Vars, Getenv: base.Getenv}
	// Assert before Apply: an expect reads what the step observed, and a
	// capture writes into the scope the steps *after* this one read.
	asserts := st.Assert(observed)
	return append(asserts, st.Apply(observed, scope)...), nil
}

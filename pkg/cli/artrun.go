package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/session"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/steps/browserstep"
	"artemis/pkg/trace"
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

// loadArtFile compiles file into what the rest of the run needs: its
// scenarios and the environment variables it names.
//
// A file that will not compile is reported to diagOut already (compileArt did
// that), but is not recorded as an errored scenario here. It is the caller's
// job to call failScenario with the error this returns -- and the caller
// gets to choose when, which matters: runFilesWith compiles every file before
// running any of them, and a file's place in run.Scenarios has to match the
// order it was discovered in, not the order its outcome became known. Folding
// the failScenario call in here would put every file that would not compile
// ahead of every file that ran, regardless of which file actually came first.
func loadArtFile(file string, diagOut io.Writer) (compiled, error) {
	scenarios, needs, err := compileArt(file, diagOut)
	if err != nil {
		return compiled{}, err
	}
	return compiled{file: file, scenarios: scenarios, needs: needs}, nil
}

// runCompiled runs every scenario of one compiled file.
func runCompiled(ctx context.Context, rt *runtimeEnv, c compiled, run *result.RunResult, rep *report.Console) {
	for _, sc := range c.scenarios {
		executeArtScenario(ctx, rt, sc, c.file, run, rep)
	}
}

// compileArt reads path, runs the front end over it and lowers it.
//
// The front end is frontEnd, which is what `artemis parse` and `artemis fmt`
// use, so a file this accepts is exactly a file `artemis parse` calls ok. The
// error is the one-line reason a non-zero exit carries; the diagnostics
// themselves have already been rendered to diagOut.
//
// The env() names a file reads come back beside the scenarios, each error
// path answering nil: the check before the run (envGate) only ever sees needs
// from a file that compiled.
func compileArt(path string, diagOut io.Writer) ([]*lower.Scenario, []check.EnvNeed, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return nil, nil, err
	}

	tree, info, bag := frontEnd(path, string(src))
	diags := bag.All()
	if len(diags) > 0 {
		files := diag.NewFiles()
		files.Add(path, string(src))
		if err := diag.Terminal(diagOut, files, diags); err != nil {
			return nil, nil, err
		}
	}
	if bag.HasErrors() {
		return nil, nil, firstError(diags)
	}

	scenarios, err := lower.File(tree, info)
	if err != nil {
		// Reachable only for a tree the checker accepted and the lowerer could
		// not read, which is a bug in artemis rather than in the file -- so it
		// names the file and says what it was doing.
		return nil, nil, fmt.Errorf("lowering %s: %w", path, err)
	}
	return scenarios, info.EnvNeeds(), nil
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
func executeArtScenario(ctx context.Context, rt *runtimeEnv, sc *lower.Scenario, file string, run *result.RunResult, rep *report.Console) {
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

	// `config browser`, resolved against the vars just bound: `viewport =
	// env("SIZE")` is an expression and only the run could have known what it
	// says. It is resolved for every scenario, browser step or not, and a
	// config that will not resolve fails the scenario -- the same shape as
	// Bind failing, and the alternative is a scenario that reaches its first
	// browser step and only then discovers it cannot open one.
	cfg, err := sc.Browser.Resolve(&eval.Env{Vars: scope.Vars()})
	if err != nil {
		scenario.Fail(0, err)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not resolve a scenario's browser config", "file", file, "error", err.Error())
		return
	}

	scenarioStart := time.Now()
	// The scenario boundary: one browser for the scenario, opened lazily on
	// its first browser step and closed when the scenario ends, pass, fail or
	// panic. It is wrapped unconditionally because the registry is lazy --
	// nothing is downloaded, launched or written unless a step asks -- so a
	// run of api steps pays nothing for this line.
	closeErr := session.WithScenario(ctx, session.Config{
		Headless: cfg.Headless,
		Viewport: cfg.Viewport,
	}, func(ctx context.Context) error {
		for _, st := range sc.Steps {
			stepResult := scenario.NewStep(st.Name)
			// The line the step was written on, so a step that could not run
			// at all -- and an assertion with no line of its own -- still
			// points somewhere a reader can go (ART-12).
			stepResult.Line = st.Line
			runArtStep(ctx, rt, sc.Name, st, scope, stepResult)
			logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", st.Name, stepResult.Duration))
			// Every step that was reached gets a line, including one artemis
			// could not execute.
			rep.Step(stepResult)
		}
		return nil
	})
	scenario.Finish(time.Since(scenarioStart))
	// A browser that would not close is the scenario's problem and nobody
	// else's: the steps have already been reported, so this is folded into the
	// scenario's own status rather than allowed to replace a step's reason.
	// WithScenario reports it only when the body returned nil, which it always
	// does -- a failed step is a status in the tree, not an error out of here.
	if closeErr != nil {
		scenario.Fail(time.Since(scenarioStart), closeErr)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not close a scenario's browser", "file", file, "error", closeErr.Error())
	}
	logger.Logger.Info("Testing ended")
}

// runArtStep attempts st until it passes or its attempts run out, and records
// on stepResult what the attempt it stopped on produced.
//
// A later attempt replaces an earlier one whole, so a recorded step is never a
// mix of two tries. An attempt is one worth stopping on when it ran at all and
// every assertion it made -- every `expect`, and every `capture` that had to be
// read -- passed.
func runArtStep(ctx context.Context, rt *runtimeEnv, scenario string, st *lower.Step, scope executor.Scope, stepResult *result.StepResult) {
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
		// The last attempt's observation, for the trace. Only the last one:
		// pkg/executor's contract is that a later attempt replaces an earlier
		// one whole, so a trace of every attempt would describe a step the
		// result tree beside it does not.
		roots map[string]any
	)
	for i := 1; i <= attempts; i++ {
		stepResult.Attempts = i
		asserts, roots, lastErr = attemptArtStep(ctx, rt.reg, st, model, scope, env)
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
		// Before the early return: a step that could not run at all is the
		// case a picture helps most -- a click that timed out says nothing
		// about what the page was showing.
		shoot(ctx, rt, scenario, st, stepResult)
		// And the case a trace helps most: there is no response to read, so
		// what was sent is the whole of the evidence.
		record(rt, scenario, st, model, scope, stepResult, roots, lastErr)
		return
	}
	for _, a := range asserts {
		stepResult.Assert(a)
	}
	stepResult.Finish(time.Since(start))
	if !stepResult.Passed() {
		shoot(ctx, rt, scenario, st, stepResult)
	}
	// Every step, pass or fail. A trace exists to answer "what actually went
	// over the wire", and comparing a passing step with a failing one is half
	// of how that question gets answered.
	record(rt, scenario, st, model, scope, stepResult, roots, nil)
}

// record writes one step's trace and stamps its path on the step for the report
// to name.
//
// A trace that could not be written is logged and otherwise ignored: it is
// evidence about the run, and failing a run because its evidence would not
// write would be the worst possible report of a disk problem. This is shoot's
// rule, for shoot's reason.
func record(rt *runtimeEnv, scenario string, st *lower.Step, model models.Step, scope executor.Scope, stepResult *result.StepResult, roots map[string]any, runErr error) {
	if rt.traces == nil || rt.traces.Dir == "" {
		return
	}
	path, err := rt.traces.On(trace.Of(
		scenario, model, stepResult.Attempts, roots, runErr, secretValues(st, scope)...))
	if err != nil {
		logger.Logger.Warn("Could not write a trace", "name", model.Name, "error", err.Error())
		return
	}
	stepResult.Trace = path
}

// secretValues resolves the step's secret bindings to the strings they hold, for
// the trace to scrub.
//
// Only what the scope already has: this evaluates nothing and reads nothing new,
// so it cannot fail and cannot change what the run did. A binding whose value is
// not a string -- a secret capture off a number -- is rendered the way a report
// renders it, because that is the form it would appear in a trace as.
func secretValues(st *lower.Step, scope executor.Scope) []string {
	names := st.SecretNames()
	if len(names) == 0 {
		return nil
	}
	vars := scope.Vars()
	out := make([]string, 0, len(names))
	for _, name := range names {
		v, ok := vars[name]
		if !ok {
			// Declared secret but never bound: a capture whose step has not run
			// yet, or one that failed to read. There is no value to scrub.
			continue
		}
		if s, isString := v.(string); isString {
			out = append(out, s)
			continue
		}
		out = append(out, eval.Render(v))
	}
	return out
}

// shoot photographs the page of a browser step that did not pass, and stamps
// the path on the step for the report to name.
//
// After the last attempt, not per attempt: the result tree records the attempt
// it stopped on, so a picture of a discarded one would show a page the report
// does not describe. Nothing is written for a step that passed, for a step of
// any other type, or for a run that turned screenshots off.
//
// A screenshot that could not be taken is logged and otherwise ignored. The
// step has already got the news a reader acts on, and replacing "the receipt
// never appeared" with "could not write a PNG" would bury it.
func shoot(ctx context.Context, rt *runtimeEnv, scenario string, st *lower.Step, stepResult *result.StepResult) {
	if rt.shots == nil || st.Type != browserstep.StepType {
		return
	}
	page, err := browserstep.Open(ctx)
	if err != nil {
		// The session never opened, which is itself what failed the step.
		return
	}
	path, err := rt.shots.On(page, scenario, st.Name)
	if err != nil {
		logger.Logger.Warn("Could not write a screenshot", "name", st.Name, "error", err.Error())
		return
	}
	stepResult.Screenshot = path
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
func attemptArtStep(ctx context.Context, reg *executor.Registry, st *lower.Step, model models.Step, scope executor.Scope, base *eval.Env) ([]result.AssertionResult, map[string]any, error) {
	roots, err := executor.Observe(ctx, reg, model, scope)
	if err != nil {
		return nil, nil, err
	}
	// The live page, for a browser step and for nothing else. It is what makes
	// `text(".x")` resolve and what the settle loop re-reads the roots through;
	// a nil one means there is nothing an assertion could wait for.
	page := livePage(ctx, st.Type)
	observed := &eval.Env{Roots: roots, Vars: base.Vars, Lookup: base.Lookup}
	if page != nil {
		observed.Elements = page.Elements()
	}
	// Assert before Apply: an expect reads what the step observed, and a
	// capture writes into the scope the steps *after* this one read.
	asserts := settle(ctx, st, observed, page)
	return append(asserts, st.Apply(observed, scope)...), roots, nil
}

// livePage is the page a browser step's assertions read, and nil for a step
// type that has none.
//
// It goes through browserstep.Open, which is the same lookup Observe used, so
// the assertions cannot read a different page from the one the actions drove.
// The error is swallowed because reaching here means Observe already opened the
// session successfully: a nil page then means "this is not a browser step",
// which is exactly how the settle loop reads it.
func livePage(ctx context.Context, stepType string) *browserstep.Bindings {
	if stepType != browserstep.StepType {
		return nil
	}
	page, err := browserstep.Open(ctx)
	if err != nil {
		return nil
	}
	return page
}

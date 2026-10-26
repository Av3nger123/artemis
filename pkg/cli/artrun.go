package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/session"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/steps/browserstep"
	"artemis/pkg/steps/httpstep"
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

// load compiles one file of the run. The file an --item run is about is its
// item's scenario, in a file of its own that imports the collection (item.go),
// reported under the collection file's path.
func (rt *runtimeEnv) load(file string, diagOut io.Writer) (compiled, error) {
	if rt.item == nil || rt.item.file != file {
		return loadArtFile(file, diagOut)
	}
	scenarios, needs, err := compileArtSource(rt.item.name, rt.item.src, diagOut)
	if err != nil {
		return compiled{}, err
	}
	return compiled{file: file, scenarios: scenarios, needs: needs}, nil
}

// runCompiled runs every scenario of one compiled file.
func runCompiled(ctx context.Context, rt *runtimeEnv, c compiled, run *result.RunResult, rep *report.Console) {
	for i, sc := range c.scenarios {
		if !c.runs(i) {
			// In the report, as skipped and why, so a CI page still counts
			// it; not on the console, where a smoke run of a large suite
			// would otherwise be a page of lines saying nothing ran.
			skipped := run.NewScenario(sc.Name, c.file)
			skipped.Tags = rt.tagsOf(c.file, sc)
			skipped.Status = result.StatusSkip
			skipped.Error = rt.sel.reason()
			continue
		}
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
	return compileArtSource(path, string(src), diagOut)
}

// compileArtSource is compileArt over source already in hand: an --item run
// compiles the collection file with the scenario that runs the item appended.
func compileArtSource(path, src string, diagOut io.Writer) ([]*lower.Scenario, []check.EnvNeed, error) {

	// Imports are read from disk, and what is lowered is the expanded tree:
	// every use already replaced by the steps it stands for, so the lowerer
	// and the runner never meet one. A diagnostic may point into an imported
	// collection, so every file the front end read is there to render it.
	u := frontEnd(path, src)
	diags := u.Bag.All()
	if len(diags) > 0 {
		if err := diag.Terminal(diagOut, sourceFiles(u), diags); err != nil {
			return nil, nil, err
		}
	}
	if u.Bag.HasErrors() {
		return nil, nil, firstError(diags)
	}
	info := u.Info

	scenarios, err := lower.File(u.Expanded, info)
	if err != nil {
		// Reachable only for a tree the checker accepted and the lowerer could
		// not read, which is a bug in artemis rather than in the file -- so it
		// names the file and says what it was doing.
		return nil, nil, fmt.Errorf("lowering %s: %w", path, err)
	}
	attributeNeeds(u, scenarios, info.EnvNeeds())
	return scenarios, info.EnvNeeds(), nil
}

// attributeNeeds gives each scenario the env() names it reads, so --tags can
// check the environment of only what it selected. A call a use brought in is
// placed by the use line, which is in the scenario; the rows of a data-driven
// scenario share their spans, so each row is given what the template reads.
func attributeNeeds(u *front.Unit, scenarios []*lower.Scenario, needs []check.EnvNeed) {
	var trees []*ast.Scenario
	for _, d := range u.Expanded.Scenarios {
		if s, ok := d.(*ast.Scenario); ok {
			trees = append(trees, s)
		}
	}
	if len(trees) != len(scenarios) {
		// Not expected; every scenario then counts every need, which is the
		// safe answer: a run checks more than it needs, never less.
		for _, sc := range scenarios {
			sc.EnvNeeds = needs
		}
		return
	}
	for _, n := range needs {
		at := n.Span
		if at.Via > 0 && at.Via <= len(u.Uses) {
			at = u.Uses[at.Via-1].Span
		}
		placed := false
		for i, s := range trees {
			if contains(s.Span(), at) {
				scenarios[i].EnvNeeds = append(scenarios[i].EnvNeeds, n)
				placed = true
			}
		}
		if !placed {
			for _, sc := range scenarios {
				sc.EnvNeeds = append(sc.EnvNeeds, n)
			}
		}
	}
}

// contains reports whether span inner lies within outer, in the same file.
func contains(outer, inner token.Span) bool {
	if outer.File != inner.File {
		return false
	}
	after := inner.Line > outer.Line || (inner.Line == outer.Line && inner.Col >= outer.Col)
	before := inner.Line < outer.EndLine || (inner.Line == outer.EndLine && inner.Col <= outer.EndCol)
	return after && before
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
// more than one. A scenario under a suite.art starts from what the suite's
// setup left: `suite` bound to its values, and a copy of its cookies.
func executeArtScenario(ctx context.Context, rt *runtimeEnv, sc *lower.Scenario, file string, run *result.RunResult, rep *report.Console) {
	in := scenarioIn{tags: rt.tagsOf(file, sc)}
	if st := rt.suiteFor(file); st != nil {
		in.skip = st.failed
		in.preset = map[string]any{"suite": st.view()}
		in.jar = st.jar
		in.cloneJar = true
	}
	execute(ctx, rt, sc, file, run, rep, in)
}

// scenarioIn is how a scenario starts, when that is not from nothing.
type scenarioIn struct {
	// preset are values in scope before the vars are bound: `suite`, for a
	// scenario under a suite.
	preset map[string]any
	// bound is a scope already bound, whose vars are not bound again: a
	// suite's teardown resumes the scope its setup ended with.
	bound executor.Scope
	// jar is the cookie jar to start from, nil for a new one; cloneJar starts
	// from a copy of it, so the scenario's cookies stay its own.
	jar      *httpstep.Jar
	cloneJar bool
	// skip, when it is not empty, is why no step of the scenario runs: a
	// suite above it whose setup did not pass.
	skip string
	// kind is "suite" for a suite's setup or teardown, which a report marks.
	kind string
	// tags are the scenario's own and its suites', for the report.
	tags []string
}

// scenarioOut is what a scenario ended with: what a suite's setup hands to the
// scenarios under it, and to its own teardown.
type scenarioOut struct {
	scope  executor.Scope
	jar    *httpstep.Jar
	passed bool
}

// execute runs one scenario from in. See executeArtScenario.
func execute(ctx context.Context, rt *runtimeEnv, sc *lower.Scenario, file string, run *result.RunResult, rep *report.Console, in scenarioIn) scenarioOut {
	scenario := run.NewScenario(sc.Name, file)
	scenario.Kind = in.kind
	scenario.Tags = in.tags
	if sc.Row != nil {
		scenario.Row = &result.Row{Source: sc.Row.Source, Index: sc.Row.Index}
	}
	rep.Scenario(scenario)
	rt.events.ScenarioStart(scenario, stepNames(sc))
	// Deferred, so every way out of here -- vars that will not bind, a config
	// that will not resolve, a browser that will not close, or every step run
	// -- ends the scenario on the stream exactly once, with the status it
	// actually ended on. Both arguments are pointers, read when it runs.
	defer rt.events.ScenarioEnd(scenario)
	logger.Logger.Info(fmt.Sprintf("Testing started for the collection: %s", sc.Name))

	// A suite above that did not set up leaves this scenario nothing to stand
	// on: every step is skipped, naming the suite, and nothing is bound or
	// sent.
	if in.skip != "" {
		for _, steps := range [][]*lower.Step{sc.Setup, sc.Steps, sc.Teardown} {
			for _, st := range steps {
				r := scenario.NewStep(st.Name)
				r.Line, r.File = st.Line, st.File
				r.Skip(in.skip)
				rep.Step(r)
			}
		}
		scenario.Finish(0)
		return scenarioOut{}
	}

	// The scenario's declared variables, with whatever the steps capture
	// written over the top as the run goes on. Bind evaluates them in source
	// order, so `var b = a` sees `a` only when `a` is above it.
	//
	// Nothing substitutes {{env.NAME}} here and nothing needs to: the process
	// environment is reached through `env("NAME")`, which is an expression the
	// evaluator resolves like any other.
	scope := in.bound
	if scope == nil {
		scope = executor.NewScope()
		for name, v := range in.preset {
			scope.Set(name, v)
		}
	}
	// The scenario's generator: uuid(), now() and the random builtins draw
	// from it in its vars and its steps alike, seeded so that --seed draws
	// the same values again.
	gen := eval.NewGen(rt.seed, rt.seedKey(file), sc.Name)
	defer func() {
		if gen.Used() {
			rt.drew.Store(true)
		}
	}()
	if in.bound == nil {
		if err := sc.Bind(scope, gen); err != nil {
			// A scenario whose variables do not resolve has nothing worth
			// running, and it gets no steps -- which is the shape of a file
			// that would not load, and is reported the same way.
			scenario.Fail(0, err)
			rep.ScenarioFailed(scenario)
			logger.Logger.Error("Could not bind a scenario's variables", "file", file, "error", err.Error())
			return scenarioOut{}
		}
	}

	// `config browser`, resolved against the vars just bound: `viewport =
	// env("SIZE")` is an expression and only the run could have known what it
	// says. It is resolved for every scenario, browser step or not, and a
	// config that will not resolve fails the scenario -- the same shape as
	// Bind failing, and the alternative is a scenario that reaches its first
	// browser step and only then discovers it cannot open one.
	cfg, err := sc.Browser.Resolve(&eval.Env{Vars: scope.Vars(), Gen: gen})
	if err != nil {
		scenario.Fail(0, err)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not resolve a scenario's browser config", "file", file, "error", err.Error())
		return scenarioOut{}
	}

	// `config http`: a cookie jar for the scenario's api steps unless it said
	// cookies = false. Made here, per scenario, so one scenario's session
	// never reaches another's requests -- or copied from the suite's, so it
	// starts logged in and still keeps what it is given to itself.
	httpCfg, err := sc.HTTP.Resolve(&eval.Env{Vars: scope.Vars(), Gen: gen})
	if err != nil {
		scenario.Fail(0, err)
		rep.ScenarioFailed(scenario)
		logger.Logger.Error("Could not resolve a scenario's http config", "file", file, "error", err.Error())
		return scenarioOut{}
	}
	var jar *httpstep.Jar
	if httpCfg.Cookies {
		switch {
		case in.jar != nil && in.cloneJar:
			jar = in.jar.Clone()
		case in.jar != nil:
			jar = in.jar
		default:
			jar = httpstep.NewJar()
		}
		ctx = httpstep.WithJar(ctx, jar)
	} else {
		ctx = httpstep.WithJar(ctx, nil)
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
		index := 0
		// one runs a step, or records why it was skipped, and reports it.
		one := func(phase string, st *lower.Step, skip string) *result.StepResult {
			rt.events.StepStart(scenario, index, st.Name)
			stepResult := scenario.NewStep(st.Name)
			// The line the step was written on, so a step that could not run
			// at all -- and an assertion with no line of its own -- still
			// points somewhere a reader can go (ART-12).
			stepResult.Line = st.Line
			// And the file that line is in, when a use brought the step in
			// from a collection: a line of auth.art is no use against the
			// scenario's file.
			stepResult.File = st.File
			stepResult.Phase = phase
			switch missing := missingNeeds(st, scope); {
			case skip != "":
				stepResult.Skip(skip)
			case phase == result.PhaseTeardown && len(missing) > 0:
				// Cleanup of something no step created: there is nothing to
				// clean, so it is skipped, and says what was never captured.
				stepResult.Skip(fmt.Sprintf("needs %s, which no step before it captured", strings.Join(missing, ", ")))
			default:
				runArtStep(ctx, rt, sc.Name, st, scope, gen, stepResult)
			}
			logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", st.Name, stepResult.Duration))
			// Every step that was reached gets a line, including one artemis
			// could not execute and one it skipped.
			rep.Step(stepResult)
			// After the console's line and after any screenshot, so the
			// result a watcher gets is the one the report will hold.
			rt.events.StepEnd(scenario, index, stepResult)
			index++
			return stepResult
		}

		// Setup first. One that does not pass leaves nothing for the
		// scenario's own steps to stand on, so the rest of setup and every
		// step after it is skipped -- naming the setup step that failed.
		skip := ""
		for _, st := range sc.Setup {
			if r := one(result.PhaseSetup, st, skip); skip == "" && r.Status != result.StatusPass {
				skip = fmt.Sprintf("setup step %q did not pass", st.Name)
			}
		}
		// The scenario's own steps. A failing one does not stop the next, as
		// it never has: each says what it found.
		for _, st := range sc.Steps {
			one("", st, skip)
		}
		// Teardown always, every step of it, after a failure as after a pass.
		for _, st := range sc.Teardown {
			one(result.PhaseTeardown, st, "")
		}
		return nil
	})
	scenario.Finish(time.Since(scenarioStart))
	rt.captures.take(sc, scope)
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
	return scenarioOut{scope: scope, jar: jar, passed: scenario.Status == result.StatusPass}
}

// stepNames is the names of a scenario's steps in the order they run -- setup,
// its own, teardown -- for scenario-start: a watcher can lay the whole scenario
// out before its first step runs.
func stepNames(sc *lower.Scenario) []string {
	names := make([]string, 0, len(sc.Setup)+len(sc.Steps)+len(sc.Teardown))
	for _, steps := range [][]*lower.Step{sc.Setup, sc.Steps, sc.Teardown} {
		for _, st := range steps {
			names = append(names, st.Name)
		}
	}
	return names
}

// missingNeeds is the captures st reads that the scope does not hold.
func missingNeeds(st *lower.Step, scope executor.Scope) []string {
	var out []string
	for _, name := range st.Needs {
		if _, ok := scope.Get(name); !ok {
			out = append(out, name)
		}
	}
	return out
}

// runArtStep attempts st until it passes or its attempts run out, and records
// on stepResult what the attempt it stopped on produced.
//
// A later attempt replaces an earlier one whole, so a recorded step is never a
// mix of two tries. An attempt is one worth stopping on when it ran at all and
// every assertion it made -- every `expect`, and every `capture` that had to be
// read -- passed.
func runArtStep(ctx context.Context, rt *runtimeEnv, scenario string, st *lower.Step, scope executor.Scope, gen *eval.Gen, stepResult *result.StepResult) {
	start := time.Now()

	// The step's own scope with no roots on it: a URL, a command or a retry
	// count may read a var or an earlier step's capture, and cannot read this
	// step's own status.
	env := &eval.Env{Vars: scope.Vars(), Gen: gen}

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
	observed := &eval.Env{Roots: roots, Vars: base.Vars, Lookup: base.Lookup, Gen: base.Gen}
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

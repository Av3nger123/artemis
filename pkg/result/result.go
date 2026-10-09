// Package result holds the structured outcome of an artemis run.
//
// The shape is four levels deep -- a run of scenarios, a scenario of steps, a
// step of assertions -- and each level carries pass/fail, how long it took and
// any error. Status at every level above an assertion is derived from its
// children by the Finish methods rather than set by callers.
//
// This package deliberately imports nothing else from artemis, so the runner,
// the terminal summary and the report writers can all depend on it.
package result

import (
	"fmt"
	"time"
)

// Status is the outcome of an assertion, step, scenario or run.
type Status string

const (
	// StatusPass means everything below it passed.
	StatusPass Status = "pass"
	// StatusFail means something ran and gave the wrong answer.
	StatusFail Status = "fail"
	// StatusError means something could not run at all -- a transport error,
	// an unreadable file, a path that does not resolve.
	StatusError Status = "error"
	// StatusSkip means it never ran, and should not count for or against the run.
	StatusSkip Status = "skip"
)

// Passed reports whether s is an outcome that should not fail the run.
// A skipped thing counts as passed; a failure and an error do not.
func (s Status) Passed() bool {
	return s == StatusPass || s == StatusSkip
}

func (s Status) String() string { return string(s) }

// worstOf folds a child status into a parent's: error beats fail beats pass,
// and a skip never changes the parent.
func worstOf(parent, child Status) Status {
	switch child {
	case StatusError:
		return StatusError
	case StatusFail:
		if parent == StatusError {
			return StatusError
		}
		return StatusFail
	default:
		return parent
	}
}

// AssertionResult is one check against one response: which step it came from,
// what was looked at, what was wanted and what was actually there.
type AssertionResult struct {
	// Step is the name of the step this assertion was made in.
	Step string
	// Kind names the sort of check -- "status_code", "body", and whatever
	// later step types add.
	Kind string
	// Path is what was inspected: a JSONPath for a body check, empty for a
	// check with nothing to address.
	Path string
	// Operator is the comparison that was applied -- "equals", "contains", and so on.
	Operator string
	// Expected is the value the scenario asked for.
	Expected any
	// Actual is the value that was found. Nil when the path did not resolve.
	Actual any
	// Status is pass, fail or error.
	Status Status
	// Error is the message when the check could not be made at all.
	Error string
	// Line is the 1-based line of the scenario file the check was written on,
	// or zero when it is not known -- a step built in Go, or a check with no
	// line of its own. A reader of a failure resolves zero against the step's
	// own line rather than printing it; see Diagnostics.
	Line int
	// File is the file Line is a line of when it is not the scenario's own: a
	// check a use brought in from a collection. Empty otherwise, which is
	// every check written where it runs.
	File string

	// ExpectedSecret and ActualSecret report that the operand came from a
	// binding the scenario declared `secret`, so every report writer prints a
	// placeholder in its place. ART-54.
	//
	// They are per-operand and not one flag, because `expect pw == "hunter2"`
	// has one secret side and withholding both would hide the comparison
	// entirely. The tree keeps the true value either way: redaction is the
	// writer's job, which is what leaves room for a flag that shows a value on
	// a local run without re-architecting anything.
	ExpectedSecret bool
	ActualSecret   bool
}

// Passed reports whether the assertion did not fail the run.
func (a AssertionResult) Passed() bool { return a.Status.Passed() }

// Assertion describes a check that is about to be recorded. The Pass, Fail and
// Errored constructors turn one into an AssertionResult.
type Assertion struct {
	Step     string
	Kind     string
	Path     string
	Operator string
	Expected any
	Actual   any
	Line     int
	File     string // see AssertionResult.File

	// ExpectedSecret and ActualSecret are carried through to the recorded
	// assertion. See AssertionResult.
	ExpectedSecret bool
	ActualSecret   bool
}

// Pass records a as having passed.
func (a Assertion) Pass() AssertionResult {
	return a.with(StatusPass, "")
}

// Fail records a as having run and given the wrong answer.
func (a Assertion) Fail() AssertionResult {
	return a.with(StatusFail, "")
}

// Errored records a as not having been checkable at all.
func (a Assertion) Errored(err error) AssertionResult {
	return a.with(StatusError, errText(err))
}

func (a Assertion) with(status Status, errMsg string) AssertionResult {
	return AssertionResult{
		Step:           a.Step,
		Kind:           a.Kind,
		Path:           a.Path,
		Operator:       a.Operator,
		Expected:       a.Expected,
		Actual:         a.Actual,
		Status:         status,
		Error:          errMsg,
		Line:           a.Line,
		File:           a.File,
		ExpectedSecret: a.ExpectedSecret,
		ActualSecret:   a.ActualSecret,
	}
}

// StepResult is the outcome of one step of a scenario.
type StepResult struct {
	// Name is the step's name from the scenario.
	Name string
	// Status is derived from the assertions by Finish, or set outright by Fail
	// and Skip.
	Status Status
	// Duration is how long the step took, including every attempt.
	Duration time.Duration
	// Error is the message when the step could not run.
	Error string
	// Attempts is how many times the step was tried -- always at least one for
	// a step that ran.
	Attempts int
	// Assertions are the checks made against the final attempt.
	Assertions []AssertionResult
	// Screenshot is the path to a picture of what the step was looking at when
	// it did not pass, and empty for every step that passed and every step
	// type that has nothing to photograph. Only a browser step produces one;
	// it is a field here rather than on a browser-shaped type because the
	// result tree is the one thing pkg/report reads, and a report that had to
	// know which step types have pictures would be a report that breaks on the
	// next one that does.
	Screenshot string
	// Line is the 1-based line of the scenario file the step was written on,
	// or zero when it is not known. It is what a step that could not run at all
	// points at, and the fallback for an assertion with no line of its own.
	Line int
	// File is the file Line is a line of when it is not the scenario's own: a
	// step a use brought in from a collection. Empty otherwise.
	File string

	// Trace is the path to the file holding what this step sent and what it
	// saw, and empty when tracing is off. ART-55.
	//
	// A path and not the trace itself, for the reason Screenshot above is a
	// path: the report is a verdict and the trace is evidence, and a trace of
	// every step of a long suite inlined here would make the report the one
	// document nobody opens. It is a field on the step rather than on an
	// api-shaped type for Screenshot's reason too -- the result tree is the one
	// thing pkg/report reads.
	Trace string
}

// AllPassed reports whether every assertion in as passed. It is what "is this
// attempt worth stopping on" means to the retry loop, and it is asked of a bare
// slice rather than of a StepResult because an executor's result has not been
// Finished yet and so has no status to read.
func AllPassed(as []AssertionResult) bool {
	for _, a := range as {
		if !a.Passed() {
			return false
		}
	}
	return true
}

// Passed reports whether the step did not fail the run.
func (s *StepResult) Passed() bool { return s.Status.Passed() }

// Assert records a check against this step.
func (s *StepResult) Assert(a AssertionResult) {
	if a.Step == "" {
		a.Step = s.Name
	}
	s.Assertions = append(s.Assertions, a)
}

// Finish closes the step after dur, deriving its status from its assertions. A
// step with no assertions and no error passed: there was nothing to get wrong.
func (s *StepResult) Finish(dur time.Duration) {
	s.Duration = dur
	s.Status = StatusPass
	for _, a := range s.Assertions {
		s.Status = worstOf(s.Status, a.Status)
	}
}

// Fail closes the step as errored after dur -- it never got as far as
// asserting anything.
func (s *StepResult) Fail(dur time.Duration, err error) {
	s.Duration = dur
	s.Status = StatusError
	s.Error = errText(err)
}

// Skip closes the step as never run.
func (s *StepResult) Skip(reason string) {
	s.Status = StatusSkip
	s.Error = reason
}

// ScenarioResult is the outcome of one scenario -- today, of one YAML file.
type ScenarioResult struct {
	// Name is the scenario's name.
	Name string
	// File is the path it was read from.
	File string
	// Status is derived from the steps by Finish, or set outright by Fail.
	Status Status
	// Duration is how long every step took together.
	Duration time.Duration
	// Error is the message when the scenario could not run -- unparseable
	// YAML, an unknown step type.
	Error string
	// Steps are its steps, in the order they ran.
	Steps []*StepResult
}

// Passed reports whether the scenario did not fail the run.
func (s *ScenarioResult) Passed() bool { return s.Status.Passed() }

// NewStep appends a step to the scenario and returns it to be filled in.
func (s *ScenarioResult) NewStep(name string) *StepResult {
	step := &StepResult{Name: name, Status: StatusPass}
	s.Steps = append(s.Steps, step)
	return step
}

// Finish closes the scenario after dur, deriving its status from its steps.
func (s *ScenarioResult) Finish(dur time.Duration) {
	s.Duration = dur
	if s.Status != StatusError {
		s.Status = StatusPass
	}
	for _, step := range s.Steps {
		s.Status = worstOf(s.Status, step.Status)
	}
}

// Fail closes the scenario as errored after dur.
func (s *ScenarioResult) Fail(dur time.Duration, err error) {
	s.Duration = dur
	s.Status = StatusError
	s.Error = errText(err)
}

// RunResult is one invocation of artemis: every scenario it ran.
type RunResult struct {
	// StartedAt is when the run began.
	StartedAt time.Time
	// Duration is how long the whole run took, set by Finish.
	Duration time.Duration
	// Status is derived from the scenarios by Finish.
	Status Status
	// Error is why the run as a whole could not proceed, and empty for every
	// run that got as far as its scenarios.
	//
	// It exists because a run that the check before the run stopped has no
	// scenarios at all, and "no scenarios and a failing status" is also what a
	// suite that matched no file looks like. A consumer has to be able to tell
	// those apart.
	Error string
	// Scenarios are the scenarios it ran, in order.
	Scenarios []*ScenarioResult

	now func() time.Time
}

// NewRun starts a run, stamping the time it began.
func NewRun() *RunResult {
	return newRunAt(time.Now)
}

func newRunAt(now func() time.Time) *RunResult {
	return &RunResult{StartedAt: now(), Status: StatusPass, now: now}
}

// Passed reports whether nothing in the run failed. A run with no scenarios
// passed; it is up to the caller to complain that it found nothing to do.
func (r *RunResult) Passed() bool { return r.Status.Passed() }

// NewScenario appends a scenario to the run and returns it to be filled in.
func (r *RunResult) NewScenario(name, file string) *ScenarioResult {
	sc := &ScenarioResult{Name: name, File: file, Status: StatusPass}
	r.Scenarios = append(r.Scenarios, sc)
	return sc
}

// Finish closes the run, deriving its status from its scenarios and its
// duration from when it started.
func (r *RunResult) Finish() {
	now := r.now
	if now == nil {
		now = time.Now
	}
	r.Duration = now().Sub(r.StartedAt)
	r.Status = StatusPass
	for _, sc := range r.Scenarios {
		r.Status = worstOf(r.Status, sc.Status)
	}
}

// Tally counts things of one level by outcome.
type Tally struct {
	Total   int
	Passed  int
	Failed  int
	Errored int
	Skipped int
}

func (t *Tally) add(s Status) {
	t.Total++
	switch s {
	case StatusPass:
		t.Passed++
	case StatusFail:
		t.Failed++
	case StatusError:
		t.Errored++
	case StatusSkip:
		t.Skipped++
	}
}

// Counts is a run's tallies at every level, so a summary or an exit code does
// not have to walk the tree.
type Counts struct {
	Scenarios  Tally
	Steps      Tally
	Assertions Tally
}

// Counts tallies the run's scenarios, steps and assertions by outcome.
func (r *RunResult) Counts() Counts {
	var c Counts
	for _, sc := range r.Scenarios {
		c.Scenarios.add(sc.Status)
		for _, step := range sc.Steps {
			c.Steps.add(step.Status)
			for _, a := range step.Assertions {
				c.Assertions.add(a.Status)
			}
		}
	}
	return c
}

// Failures returns every assertion in the run that did not pass, in order, for
// a summary or a report to print.
func (r *RunResult) Failures() []AssertionResult {
	var out []AssertionResult
	for _, sc := range r.Scenarios {
		for _, step := range sc.Steps {
			for _, a := range step.Assertions {
				if !a.Passed() {
					out = append(out, a)
				}
			}
		}
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Redacted is what stands in for a value that came from a binding the scenario
// declared `secret`. ART-54.
//
// The length is fixed and deliberately unrelated to the value's. A placeholder
// that kept the length would report the length of a credential, which is a
// useful fact to an attacker.
//
// It lives here rather than in pkg/report because Describe needs it and this
// package imports nothing else from artemis. pkg/report refers to this one, so
// there is a single spelling.
const Redacted = "***"

// Shown returns the two operands as a reader may see them: the real value, or
// Redacted for a side that came from a secret binding.
//
// Everything that prints an operand goes through this -- Describe below, and
// every writer in pkg/report -- so what withholding means is decided once. The
// fields themselves keep the true value, because the tree stays faithful and
// redaction belongs to whatever renders it.
func (a AssertionResult) Shown() (expected, actual any) {
	expected, actual = a.Expected, a.Actual
	if a.ExpectedSecret {
		expected = Redacted
	}
	if a.ActualSecret {
		actual = Redacted
	}
	return expected, actual
}

// Describe renders an assertion as one line, for an error message or a log.
//
// This is the line the console prints under a failed step as the run goes, which
// makes it an operand site like any other: it went through Shown from ART-54
// onward, having leaked a credential before that.
func (a AssertionResult) Describe() string {
	where := a.Path
	if where == "" {
		where = a.Kind
	}
	if a.Error != "" {
		return fmt.Sprintf("%s %s: %s", where, a.Operator, a.Error)
	}
	expected, actual := a.Shown()
	return fmt.Sprintf("%s %s %v, got %v", where, a.Operator, expected, actual)
}

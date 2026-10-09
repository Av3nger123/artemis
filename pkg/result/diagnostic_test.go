package result

import (
	"errors"
	"testing"
	"time"
)

// diagRun builds a run holding one scenario per argument, each already Finished,
// so a test can state the tree it wants and read the diagnostics off it.
func diagRun(t *testing.T, scenarios ...*ScenarioResult) *RunResult {
	t.Helper()
	run := NewRun()
	run.Scenarios = scenarios
	run.Finish()
	return run
}

// failingStep is a step with one failed body assertion on line 13.
func failingStep() *StepResult {
	step := &StepResult{Name: "ping", Line: 5}
	step.Assert(Assertion{
		Kind: "body", Path: "$.status", Operator: "equals",
		Expected: "ok", Actual: "pending", Line: 13,
	}.Fail())
	step.Finish(time.Millisecond)
	return step
}

func TestDiagnosticsJoinsTheFileScenarioStepAndLine(t *testing.T) {
	sc := &ScenarioResult{Name: "status check", File: "suite/ping.yaml"}
	sc.Steps = []*StepResult{failingStep()}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("len(Diagnostics()) = %d, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.File != "suite/ping.yaml" || d.Scenario != "status check" || d.Step != "ping" {
		t.Errorf("Diagnostics()[0] = %+v, want it to name the file, scenario and step", d)
	}
	if d.Line != 13 {
		t.Errorf("Line = %d, want 13 -- the check's own line", d.Line)
	}
	if d.Status != StatusFail {
		t.Errorf("Status = %q, want fail", d.Status)
	}
	if d.Assertion == nil || d.Assertion.Path != "$.status" {
		t.Errorf("Assertion = %+v, want the failed body check", d.Assertion)
	}
	if d.Error != "" {
		t.Errorf("Error = %q, want empty -- the assertion carries its own", d.Error)
	}
}

// Every failing assertion of a step gets a block, not only the first.
func TestDiagnosticsReportsEveryFailingAssertionOfAStep(t *testing.T) {
	step := &StepResult{Name: "ping", Line: 5}
	step.Assert(Assertion{Kind: "body", Path: "$.a", Operator: "equals", Line: 10}.Fail())
	step.Assert(Assertion{Kind: "body", Path: "$.b", Operator: "equals", Line: 12}.Pass())
	step.Assert(Assertion{Kind: "body", Path: "$.c", Operator: "equals", Line: 14}.Errored(errors.New("no such path")))
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 2 {
		t.Fatalf("len(Diagnostics()) = %d, want 2 -- the passing one is not a diagnostic: %+v", len(diags), diags)
	}
	if diags[0].Line != 10 || diags[1].Line != 14 {
		t.Errorf("lines = %d, %d, want 10, 14", diags[0].Line, diags[1].Line)
	}
	if diags[1].Status != StatusError || diags[1].Assertion.Error != "no such path" {
		t.Errorf("Diagnostics()[1] = %+v, want the errored check with its reason", diags[1])
	}
}

// A check with no line of its own -- a status_code the step never wrote -- points
// at the step instead of at nothing.
func TestDiagnosticsFallsBackToTheStepsLine(t *testing.T) {
	step := &StepResult{Name: "ping", Line: 7}
	step.Assert(Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 500}.Fail())
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 1 || diags[0].Line != 7 {
		t.Fatalf("Diagnostics() = %+v, want one diagnostic on line 7", diags)
	}
}

// Nothing in the tree knows a line: the diagnostic says zero, and the renderer is
// what decides to print no location at all.
func TestDiagnosticsLeavesAnUnknownLineAtZero(t *testing.T) {
	step := &StepResult{Name: "ping"}
	step.Assert(Assertion{Kind: "body", Path: "$.a", Operator: "equals"}.Fail())
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	if diags := diagRun(t, sc).Diagnostics(); len(diags) != 1 || diags[0].Line != 0 {
		t.Fatalf("Diagnostics() = %+v, want one diagnostic with no line", diags)
	}
}

// A step that never got as far as asserting is a diagnostic in its own right.
func TestDiagnosticsReportsAStepThatCouldNotRun(t *testing.T) {
	step := &StepResult{Name: "fetch", Line: 9}
	step.Fail(time.Millisecond, errors.New(`rendering request url: unknown variable "host"`))
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("len(Diagnostics()) = %d, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Assertion != nil {
		t.Errorf("Assertion = %+v, want nil -- nothing was checked", d.Assertion)
	}
	if d.Line != 9 || d.Status != StatusError || d.Error == "" {
		t.Errorf("Diagnostics()[0] = %+v, want the step's line, error status and reason", d)
	}
}

// A step whose assertions already say what is wrong must not also be reported as
// a step failure: that is one mistake, and it would read as two.
func TestDiagnosticsDoesNotReportAStepTwice(t *testing.T) {
	step := &StepResult{Name: "login", Line: 5}
	step.Assert(Assertion{Kind: "capture", Path: "token", Operator: "json", Line: 11}.Errored(errors.New("nothing is at that path")))
	step.Finish(time.Millisecond)
	step.Error = "set by hand: a step that both errored and asserted"
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	if diags := diagRun(t, sc).Diagnostics(); len(diags) != 1 {
		t.Fatalf("len(Diagnostics()) = %d, want 1 -- the assertion already explains it: %+v", len(diags), diags)
	}
}

// A scenario whose file would not load has no steps and no name. It is still the
// most actionable thing in the run.
func TestDiagnosticsReportsAScenarioThatCouldNotLoad(t *testing.T) {
	sc := &ScenarioResult{File: "suite/02_broken.yaml"}
	sc.Fail(0, errors.New("parse suite/02_broken.yaml: yaml: unmarshal errors:\n  line 12: field respones not found"))

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("len(Diagnostics()) = %d, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.File != "suite/02_broken.yaml" || d.Step != "" || d.Scenario != "" {
		t.Errorf("Diagnostics()[0] = %+v, want the file and nothing else named", d)
	}
	if d.Error == "" || d.Line != 0 {
		t.Errorf("Diagnostics()[0] = %+v, want the reason and no line", d)
	}
}

// A skipped step is not a failure, so it is not a diagnostic.
func TestDiagnosticsIgnoresASkippedStep(t *testing.T) {
	step := &StepResult{Name: "later", Line: 20}
	step.Skip("nothing to do")
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	if diags := diagRun(t, sc).Diagnostics(); len(diags) != 0 {
		t.Fatalf("Diagnostics() = %+v, want none for a skipped step", diags)
	}
}

func TestDiagnosticsOfAPassingRunIsEmpty(t *testing.T) {
	step := &StepResult{Name: "ping", Line: 5}
	step.Assert(Assertion{Kind: "body", Path: "$.a", Operator: "equals"}.Pass())
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	if diags := diagRun(t, sc).Diagnostics(); len(diags) != 0 {
		t.Fatalf("Diagnostics() = %+v, want none", diags)
	}
}

// Order is the order the run happened in: a reader matches a block against the
// transcript above it.
func TestDiagnosticsAreInRunOrder(t *testing.T) {
	first := &ScenarioResult{Name: "one", File: "01.yaml", Steps: []*StepResult{failingStep()}}
	first.Finish(time.Millisecond)
	second := &ScenarioResult{File: "02.yaml"}
	second.Fail(0, errors.New("will not load"))
	third := &ScenarioResult{Name: "three", File: "03.yaml", Steps: []*StepResult{failingStep()}}
	third.Finish(time.Millisecond)

	diags := diagRun(t, first, second, third).Diagnostics()
	want := []string{"01.yaml", "02.yaml", "03.yaml"}
	if len(diags) != len(want) {
		t.Fatalf("len(Diagnostics()) = %d, want %d: %+v", len(diags), len(want), diags)
	}
	for i, file := range want {
		if diags[i].File != file {
			t.Errorf("Diagnostics()[%d].File = %q, want %q", i, diags[i].File, file)
		}
	}
}

func TestDiagnosticsOfANilRunIsEmpty(t *testing.T) {
	var run *RunResult
	if diags := run.Diagnostics(); diags != nil {
		t.Fatalf("Diagnostics() = %+v, want nil", diags)
	}
}

// A nil scenario or step in the tree is not something the runner produces, and
// Diagnostics still must not take a report down over one. The tree is built by
// hand here rather than Finished, because Finish is the method that would
// dereference the nil step first.
func TestDiagnosticsSkipsNilChildren(t *testing.T) {
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Status: StatusFail, Steps: []*StepResult{nil, failingStep()}}
	run := NewRun()
	run.Scenarios = []*ScenarioResult{nil, sc}
	run.Status = StatusFail

	if diags := run.Diagnostics(); len(diags) != 1 {
		t.Fatalf("len(Diagnostics()) = %d, want 1: %+v", len(diags), diags)
	}
}

// Each diagnostic's assertion is its own copy. A caller that holds one and the
// tree must not share a pointer into the slice the tree owns.
func TestDiagnosticsCopiesEachAssertion(t *testing.T) {
	step := &StepResult{Name: "ping", Line: 5}
	step.Assert(Assertion{Kind: "body", Path: "$.a", Operator: "equals", Line: 10}.Fail())
	step.Assert(Assertion{Kind: "body", Path: "$.b", Operator: "equals", Line: 12}.Fail())
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "s", File: "s.yaml", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 2 {
		t.Fatalf("len(Diagnostics()) = %d, want 2", len(diags))
	}
	if diags[0].Assertion.Path != "$.a" || diags[1].Assertion.Path != "$.b" {
		t.Errorf("assertions = %q, %q, want $.a, $.b -- one loop variable shared by both",
			diags[0].Assertion.Path, diags[1].Assertion.Path)
	}
}

// A step a use brought in from a collection carries the collection's file, and
// a diagnostic about it names that file: its line is a line of that file. A
// check with no line of its own falls back to the step, file and all.
func TestDiagnosticsNameTheFileTheLineIsIn(t *testing.T) {
	step := &StepResult{Name: "auth.login", Line: 2, File: "auth.art"}
	step.Assert(Assertion{Kind: "expect", Line: 6, File: "auth.art"}.Fail())
	step.Assert(Assertion{Kind: "status_code"}.Fail())
	step.Finish(time.Millisecond)
	sc := &ScenarioResult{Name: "checkout", File: "checkout.art", Steps: []*StepResult{step}}
	sc.Finish(time.Millisecond)

	diags := diagRun(t, sc).Diagnostics()
	if len(diags) != 2 {
		t.Fatalf("Diagnostics() = %+v", diags)
	}
	if d := diags[0]; d.File != "auth.art" || d.Line != 6 {
		t.Errorf("the check's own line: %s:%d, want auth.art:6", d.File, d.Line)
	}
	if d := diags[1]; d.File != "auth.art" || d.Line != 2 {
		t.Errorf("the step's line: %s:%d, want auth.art:2", d.File, d.Line)
	}

	errored := &StepResult{Name: "x", Line: 9, File: "auth.art"}
	errored.Fail(time.Millisecond, errors.New("no route"))
	sc2 := &ScenarioResult{Name: "s", File: "s.art", Steps: []*StepResult{errored}}
	sc2.Finish(time.Millisecond)
	if d := diagRun(t, sc2).Diagnostics()[0]; d.File != "auth.art" || d.Line != 9 {
		t.Errorf("a step that could not run: %s:%d, want auth.art:9", d.File, d.Line)
	}
}

package result

// This file is ART-12's half of the result model: the list of things a failed
// run says to go and fix, derived from the tree rather than stored beside it.

// Diagnostic is one thing that went wrong, with everything needed to find it:
// which file, which line of it, which scenario and step, and either the check
// that gave the wrong answer or the reason nothing could be checked.
//
// It is what a reader of a failed run is actually after, and it is produced
// once, by Diagnostics, so the terminal blocks and the JSON report's failures
// array cannot describe the same run differently.
//
// It is deliberately flat. An assertion in the tree knows its step but not its
// file; a scenario knows its file but not which assertion failed. A diagnostic
// is the join, so nothing downstream walks four levels to say one sentence.
type Diagnostic struct {
	// File is the scenario file, as the run was given it.
	File string
	// Scenario is the scenario's name, empty when its file would not load and
	// there was no name to read.
	Scenario string
	// Step is the step's name, empty for a scenario that never ran one.
	Step string
	// Line is the line of File to go and edit, already resolved: the check's own
	// line, or the step's when the check has none. Zero when nothing in the tree
	// knows -- a step built in Go, a scenario converted from Postman.
	Line int
	// Status is fail or error: the thing gave the wrong answer, or could not be
	// done at all.
	Status Status
	// Assertion is the check that did not pass, or nil when there was no check
	// -- a step that could not run, a file that would not load.
	Assertion *AssertionResult
	// Error is the reason, for a diagnostic with no assertion. An errored
	// assertion carries its own reason on Assertion.Error.
	Error string
	// Screenshot is the step's picture, copied off it rather than looked up,
	// because the point of this type is that an entry stands alone: something
	// holding one diagnostic should not have to walk back up the tree to find
	// the image of the page the failure is about. Empty when there is none.
	Screenshot string
}

// Diagnostics is everything the run says to go and fix, in the order it
// happened: scenario by scenario, step by step, assertion by assertion.
//
// What is in it:
//
//   - every assertion that did not pass, one diagnostic each;
//   - every step that could not run at all, when none of its own assertions
//     already explains the failure -- a step whose capture errored has said
//     what is wrong and must not be reported twice;
//   - every scenario that did not pass and ran no step, which is a file artemis
//     could not load.
//
// A run that passed produces none, and a nil run produces none. Each diagnostic
// is self-contained: it repeats the file, the scenario and the step rather than
// relying on a reader having seen the one before it, because the thing reading
// this is as likely to be an agent holding one block as a person scrolling.
func (r *RunResult) Diagnostics() []Diagnostic {
	if r == nil {
		return nil
	}
	var out []Diagnostic
	for _, sc := range r.Scenarios {
		if sc == nil {
			continue
		}
		out = append(out, scenarioDiagnostics(sc)...)
	}
	return out
}

// scenarioDiagnostics is the diagnostics of one scenario.
func scenarioDiagnostics(sc *ScenarioResult) []Diagnostic {
	var out []Diagnostic
	for _, step := range sc.Steps {
		if step == nil {
			continue
		}
		out = append(out, stepDiagnostics(sc, step)...)
	}
	// A scenario that did not pass and ran nothing is a file that would not
	// load. Without this it would fail the run as a tally and nothing else.
	if !sc.Passed() && len(sc.Steps) == 0 {
		out = append(out, Diagnostic{
			File:     sc.File,
			Scenario: sc.Name,
			Status:   sc.Status,
			Error:    sc.Error,
		})
	}
	return out
}

// stepDiagnostics is the diagnostics of one step: one per assertion that did not
// pass, or one for the step itself when it never got as far as asserting.
func stepDiagnostics(sc *ScenarioResult, step *StepResult) []Diagnostic {
	var out []Diagnostic
	for i := range step.Assertions {
		a := step.Assertions[i]
		if a.Passed() {
			continue
		}
		out = append(out, Diagnostic{
			File:       sc.File,
			Scenario:   sc.Name,
			Step:       step.Name,
			Line:       lineOf(a.Line, step.Line),
			Status:     a.Status,
			Assertion:  &a,
			Screenshot: step.Screenshot,
		})
	}
	// Only when nothing above has already said what is wrong: a step that
	// errored after an assertion errored has one reason, not two.
	if len(out) == 0 && !step.Passed() {
		out = append(out, Diagnostic{
			File:       sc.File,
			Scenario:   sc.Name,
			Step:       step.Name,
			Line:       step.Line,
			Status:     step.Status,
			Error:      step.Error,
			Screenshot: step.Screenshot,
		})
	}
	return out
}

// lineOf resolves a check's line against its step's. A check with no line of
// its own -- a `status_code` the step never wrote, a default `exit_code: 0` --
// still points at the step, which is the right place to go.
func lineOf(own, step int) int {
	if own > 0 {
		return own
	}
	return step
}

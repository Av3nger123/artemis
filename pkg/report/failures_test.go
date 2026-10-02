package report

import (
	"errors"
	"strings"
	"testing"
	"time"

	"artemis/pkg/result"
)

// failRun wraps already-built scenarios into a finished run.
func failRun(scenarios ...*result.ScenarioResult) *result.RunResult {
	run := result.NewRun()
	run.Scenarios = scenarios
	run.Finish()
	return run
}

// oneStep is a scenario of one step, already finished.
func oneStep(name, file string, step *result.StepResult) *result.ScenarioResult {
	sc := &result.ScenarioResult{Name: name, File: file, Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	return sc
}

// The block, byte for byte. This is the format ART-12 is about, so a change to it
// has to be a change to this string first.
func TestFailuresPrintsOneBlockPerFailedAssertion(t *testing.T) {
	step := &result.StepResult{Name: "ping", Line: 5}
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.status", Operator: "equals",
		Expected: "ok", Actual: "pending", Line: 14,
	}.Fail())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.count", Operator: "lte",
		Expected: 2, Actual: 3.0, Line: 17,
	}.Fail())
	step.Finish(time.Millisecond)

	got := render(func(c *Console) { c.Failures(failRun(oneStep("status check", "testdata/fail.yaml", step))) })
	want := `
2 failures:

1) testdata/fail.yaml:14
     scenario  status check
     step      ping
     assert    body $.status equals
     expected  "ok"
     actual    "pending"

2) testdata/fail.yaml:17
     scenario  status check
     step      ping
     assert    body $.count lte
     expected  2
     actual    3
`
	if got != want {
		t.Errorf("Failures() =\n%s\nwant\n%s", got, want)
	}
}

// An errored assertion -- a path that did not resolve, a capture that read
// nothing -- has no actual value to print. The reason takes their place.
func TestFailuresPrintsTheReasonForAnErroredAssertion(t *testing.T) {
	step := &result.StepResult{Name: "login", Line: 5}
	step.Assert(result.Assertion{
		Kind: "capture", Path: "authToken", Operator: "json",
		Expected: "$.session.token", Line: 11,
	}.Errored(errors.New(`path "$.session.token": nothing is at that path`)))
	step.Finish(time.Millisecond)

	got := render(func(c *Console) { c.Failures(failRun(oneStep("captures", "cap.yaml", step))) })
	want := `
1 failure:

1) cap.yaml:11
     scenario  captures
     step      login
     assert    capture authToken json
     error     path "$.session.token": nothing is at that path
`
	if got != want {
		t.Errorf("Failures() =\n%s\nwant\n%s", got, want)
	}
}

// A step that never got as far as asserting: its own line, its own reason, and no
// expected or actual, because nothing was compared.
func TestFailuresPrintsAStepThatCouldNotRun(t *testing.T) {
	step := &result.StepResult{Name: "fetch", Line: 6}
	step.Fail(time.Millisecond, errors.New(`rendering request url: unknown variable "host"`))

	got := render(func(c *Console) { c.Failures(failRun(oneStep("template error", "tpl.yaml", step))) })
	want := `
1 failure:

1) tpl.yaml:6
     scenario  template error
     step      fetch
     error     rendering request url: unknown variable "host"
`
	if got != want {
		t.Errorf("Failures() =\n%s\nwant\n%s", got, want)
	}
}

// A file that would not load has no scenario name and no step, so those fields are
// left out rather than printed empty. Its reason is two lines, and the second is
// indented to the value column so the block stays one unit.
func TestFailuresPrintsAScenarioThatCouldNotLoad(t *testing.T) {
	sc := &result.ScenarioResult{File: "suite/02_broken.yaml"}
	sc.Fail(0, errors.New("parse suite/02_broken.yaml: yaml: unmarshal errors:\n  line 12: field respones not found in type models.Step"))

	got := render(func(c *Console) { c.Failures(failRun(sc)) })
	want := `
1 failure:

1) suite/02_broken.yaml
     error     parse suite/02_broken.yaml: yaml: unmarshal errors:
               line 12: field respones not found in type models.Step
`
	if got != want {
		t.Errorf("Failures() =\n%s\nwant\n%s", got, want)
	}
}

// Nothing in the tree knows a line, so no location is printed at all: ":0" would
// send an editor to a line that does not exist.
func TestFailuresOmitsAnUnknownLine(t *testing.T) {
	step := &result.StepResult{Name: "ping"}
	step.Assert(result.Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 500}.Fail())
	step.Finish(time.Millisecond)

	got := render(func(c *Console) { c.Failures(failRun(oneStep("s", "s.yaml", step))) })
	if want := "1) s.yaml\n"; !strings.Contains(got, want) {
		t.Errorf("Failures() =\n%s\nwant a block headed %q", got, want)
	}
	if strings.Contains(got, ":0") {
		t.Errorf("Failures() printed a zero line:\n%s", got)
	}
}

// The mistake this rendering exists for: a scenario that quoted a number, or did
// not quote one. Both sides look the same, so each is told its type.
func TestFailuresNamesTheTypeWhenTheTwoSidesDiffer(t *testing.T) {
	step := &result.StepResult{Name: "ping", Line: 5}
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.code", Operator: "equals",
		Expected: "200", Actual: 200.0, Line: 12,
	}.Fail())
	step.Finish(time.Millisecond)

	got := render(func(c *Console) { c.Failures(failRun(oneStep("s", "s.yaml", step))) })
	for _, want := range []string{`expected  "200"  (string)`, `actual    200  (number)`} {
		if !strings.Contains(got, want) {
			t.Errorf("Failures() is missing %q:\n%s", want, got)
		}
	}
}

// Two sides of the same type get no hint: it would be noise on the common case.
func TestFailuresOmitsTheTypeWhenBothSidesAgree(t *testing.T) {
	step := &result.StepResult{Name: "ping", Line: 5}
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.code", Operator: "equals",
		Expected: 200.0, Actual: 500.0, Line: 12,
	}.Fail())
	step.Finish(time.Millisecond)

	got := render(func(c *Console) { c.Failures(failRun(oneStep("s", "s.yaml", step))) })
	if strings.Contains(got, "(number)") {
		t.Errorf("Failures() named a type both sides share:\n%s", got)
	}
}

// Every value is rendered so that what it is survives, not only what it says: an
// empty string is not a missing one, and a composite is JSON a reader can paste.
func TestRenderValue(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{"ok", `"ok"`},
		{"", `""`},
		{"has space ", `"has space "`},
		{200, "200"},
		{200.0, "200"},
		{2.5, "2.5"},
		{true, "true"},
		{[]any{"a", "b"}, `["a","b"]`},
		{map[string]any{"id": 1.0}, `{"id":1}`},
	} {
		if got := renderValue(c.in); got != c.want {
			t.Errorf("renderValue(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A value encoding/json cannot render must still print something: a report is not
// worth failing a run over.
func TestRenderValueFallsBackForAnUnencodableValue(t *testing.T) {
	if got := renderValue(func() {}); got == "" {
		t.Error("renderValue(func) = \"\", want something printable")
	}
}

// A passing run ends with its summary, not with an empty heading.
func TestFailuresPrintsNothingForAPassingRun(t *testing.T) {
	step := &result.StepResult{Name: "ping", Line: 5}
	step.Assert(result.Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200}.Pass())
	step.Finish(time.Millisecond)

	if got := render(func(c *Console) { c.Failures(failRun(oneStep("s", "s.yaml", step))) }); got != "" {
		t.Errorf("Failures() = %q, want nothing", got)
	}
}

func TestFailuresTakesANilRun(t *testing.T) {
	if got := render(func(c *Console) { c.Failures(nil) }); got != "" {
		t.Errorf("Failures(nil) = %q, want nothing", got)
	}
}

// Failures across two files each name their own file, which is the whole reason a
// block repeats what the scenario header already said.
func TestFailuresNamesEachFileAcrossAFolderRun(t *testing.T) {
	first := &result.StepResult{Name: "a", Line: 5}
	first.Assert(result.Assertion{Kind: "body", Path: "$.x", Operator: "equals", Expected: 1, Actual: 2, Line: 10}.Fail())
	first.Finish(time.Millisecond)
	second := &result.StepResult{Name: "b", Line: 5}
	second.Assert(result.Assertion{Kind: "body", Path: "$.y", Operator: "equals", Expected: 1, Actual: 2, Line: 20}.Fail())
	second.Finish(time.Millisecond)

	got := render(func(c *Console) {
		c.Failures(failRun(oneStep("one", "suite/01.yaml", first), oneStep("two", "suite/02.yaml", second)))
	})
	for _, want := range []string{"1) suite/01.yaml:10", "2) suite/02.yaml:20"} {
		if !strings.Contains(got, want) {
			t.Errorf("Failures() is missing %q:\n%s", want, got)
		}
	}
}

// A single failure reads "1 failure", not "1 failures".
func TestFailuresHeadingAgreesInNumber(t *testing.T) {
	step := &result.StepResult{Name: "ping", Line: 5}
	step.Assert(result.Assertion{Kind: "body", Path: "$.x", Operator: "equals", Expected: 1, Actual: 2, Line: 10}.Fail())
	step.Finish(time.Millisecond)

	if got := render(func(c *Console) { c.Failures(failRun(oneStep("s", "s.yaml", step))) }); !strings.Contains(got, "1 failure:") {
		t.Errorf("Failures() =\n%s\nwant a \"1 failure:\" heading", got)
	}
}

package report

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"

	"artemis/pkg/result"
)

// writeJUnit renders run and fails the test if it could not be written.
func writeJUnit(t *testing.T, run *result.RunResult) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteJUnit(&buf, run); err != nil {
		t.Fatalf("WriteJUnit() = %v, want nil", err)
	}
	return buf.String()
}

// decodeJUnit parses a document back into the wire types, for a test that asks
// about one attribute rather than the whole shape. It is also the check that
// what was written is well-formed XML.
func decodeJUnit(t *testing.T, doc string) xmlTestSuites {
	t.Helper()
	var got xmlTestSuites
	if err := xml.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("the document is not valid XML: %v\n%s", err, doc)
	}
	return got
}

// onlyCase returns the single test case of a single suite, for the many tests
// about one step.
func onlyCase(t *testing.T, doc string) xmlTestCase {
	t.Helper()
	got := decodeJUnit(t, doc)
	if len(got.Suites) != 1 || len(got.Suites[0].Cases) != 1 {
		t.Fatalf("want one suite of one case, got %d suites:\n%s", len(got.Suites), doc)
	}
	return got.Suites[0].Cases[0]
}

// failedStep is a step that ran and got one wrong answer, plus one right one.
func failedStep() *result.StepResult {
	step := &result.StepResult{Name: "get item", Attempts: 1}
	step.Assert(result.Assertion{
		Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200,
	}.Pass())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.status", Operator: "equals",
		Expected: "ready", Actual: "pending",
	}.Fail())
	step.Finish(12*time.Millisecond + 500*time.Microsecond)
	return step
}

// runOf wraps steps in one scenario and one run, finished, so a test can name
// just the steps it cares about. The durations are fixed afterwards, because
// RunResult.Finish derives the run's from the wall clock.
func runOf(steps ...*result.StepResult) *result.RunResult {
	sc := &result.ScenarioResult{Name: "items", File: "suite/items.yaml", Steps: steps}
	sc.Finish(13 * time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Scenarios: []*result.ScenarioResult{sc}}
	run.Finish()
	run.Duration = 14 * time.Millisecond
	return run
}

// The dialect, in full, for the smallest interesting run. Pinned byte for byte
// because the element and attribute names are what every CI reporter keys on --
// a reviewer should see a rename here as a diff, not learn about it from a CI job
// that silently reports no tests.
func TestWriteJUnitPinsTheDialect(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="artemis" tests="1" failures="1" errors="0" skipped="0" time="0.014" timestamp="2026-03-04T05:06:07Z">
  <testsuite name="items" file="suite/items.yaml" tests="1" failures="1" errors="0" skipped="0" time="0.013">
    <testcase name="get item" classname="suite/items.yaml" time="0.013">
      <failure message="$.status equals ready, got pending">$.status equals ready, got pending</failure>
    </testcase>
  </testsuite>
</testsuites>
`
	if got := writeJUnit(t, runOf(failedStep())); got != want {
		t.Errorf("WriteJUnit() =\n%s\nwant:\n%s", got, want)
	}
}

// A scenario is a suite and a step is a case, so a reporter groups failures per
// scenario with no configuration. This is the mapping the issue exists for.
func TestWriteJUnitMapsScenariosToSuitesAndStepsToCases(t *testing.T) {
	first := &result.StepResult{Name: "login"}
	first.Finish(time.Millisecond)
	second := &result.StepResult{Name: "list items"}
	second.Finish(2 * time.Millisecond)
	auth := &result.ScenarioResult{Name: "auth", File: "suite/01_auth.yaml", Steps: []*result.StepResult{first, second}}
	auth.Finish(3 * time.Millisecond)

	items := &result.ScenarioResult{Name: "items", File: "suite/02_items.yaml", Steps: []*result.StepResult{failedStep()}}
	items.Finish(13 * time.Millisecond)

	run := &result.RunResult{StartedAt: startedAt, Scenarios: []*result.ScenarioResult{auth, items}}
	run.Finish()

	got := decodeJUnit(t, writeJUnit(t, run))
	if len(got.Suites) != 2 {
		t.Fatalf("got %d suites, want one per scenario", len(got.Suites))
	}
	if got.Suites[0].Name != "auth" || got.Suites[1].Name != "items" {
		t.Errorf("suite names = %q, %q, want the scenario names in order", got.Suites[0].Name, got.Suites[1].Name)
	}
	if got.Suites[0].File != "suite/01_auth.yaml" {
		t.Errorf("suite file = %q, want the scenario's path", got.Suites[0].File)
	}
	if len(got.Suites[0].Cases) != 2 {
		t.Fatalf("got %d cases, want one per step", len(got.Suites[0].Cases))
	}
	if got.Suites[0].Cases[1].Name != "list items" {
		t.Errorf("case name = %q, want the step's name", got.Suites[0].Cases[1].Name)
	}
	// The file, not the scenario's name: two files that name their scenario
	// the same must not merge into one test in a CI history.
	if cn := got.Suites[1].Cases[0].Classname; cn != "suite/02_items.yaml" {
		t.Errorf("classname = %q, want the scenario's file", cn)
	}
}

// A step that passed has no child element at all. A reporter reads the absence
// as a pass, and a <failure> on a passing case would turn a green run red.
func TestWriteJUnitPassingCaseHasNoChild(t *testing.T) {
	step := &result.StepResult{Name: "health"}
	step.Assert(result.Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200}.Pass())
	step.Finish(time.Millisecond)

	got := onlyCase(t, writeJUnit(t, runOf(step)))
	if got.Failure != nil || got.Error != nil || got.Skipped != nil {
		t.Errorf("a passing case has a child element: %+v", got)
	}
}

// Every failing assertion is in the document, not only the first: one <failure>
// element, because several reporters render only the first child of a case, with
// a line per assertion in its text and a count in its message.
func TestWriteJUnitKeepsEveryFailingAssertion(t *testing.T) {
	step := &result.StepResult{Name: "get item"}
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.status", Operator: "equals", Expected: "ready", Actual: "pending",
	}.Fail())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.count", Operator: "gt", Expected: 10, Actual: 3,
	}.Fail())
	step.Finish(time.Millisecond)

	got := onlyCase(t, writeJUnit(t, runOf(step)))
	if got.Failure == nil {
		t.Fatalf("a failed step has no <failure>: %+v", got)
	}
	if want := "$.status equals ready, got pending (+1 more)"; got.Failure.Message != want {
		t.Errorf("message = %q, want %q", got.Failure.Message, want)
	}
	for _, want := range []string{"$.status equals ready, got pending", "$.count gt 10, got 3"} {
		if !strings.Contains(got.Failure.Text, want) {
			t.Errorf("the failure text does not hold %q:\n%s", want, got.Failure.Text)
		}
	}
	if lines := strings.Count(got.Failure.Text, "\n"); lines != 1 {
		t.Errorf("the failure text has %d newlines, want one assertion per line", lines)
	}
}

// A step that could not run is an <error>, not a <failure>: the distinction ART-1
// draws between "ran and gave the wrong answer" and "could not run at all" is
// one every reporter can show, and a flaky environment should not read as a
// broken API.
func TestWriteJUnitErroredStepIsAnError(t *testing.T) {
	step := &result.StepResult{Name: "get item"}
	step.Fail(time.Millisecond, errors.New("dial tcp: connection refused"))

	got := onlyCase(t, writeJUnit(t, runOf(step)))
	if got.Failure != nil {
		t.Errorf("an errored step has a <failure>: %+v", got.Failure)
	}
	if got.Error == nil {
		t.Fatalf("an errored step has no <error>: %+v", got)
	}
	if want := "dial tcp: connection refused"; got.Error.Message != want {
		t.Errorf("message = %q, want %q", got.Error.Message, want)
	}
}

// An assertion that could not be made at all -- a capture that would not read --
// makes its step an error too, and the reason has to reach the XML even though
// the step itself recorded no error.
func TestWriteJUnitErroredAssertionCarriesItsReason(t *testing.T) {
	step := &result.StepResult{Name: "get item"}
	step.Assert(result.Assertion{
		Kind: "capture", Path: "$.token", Operator: "exists",
	}.Errored(errors.New("no match for $.token")))
	step.Finish(time.Millisecond)

	got := onlyCase(t, writeJUnit(t, runOf(step)))
	if got.Error == nil {
		t.Fatalf("a step with an errored assertion has no <error>: %+v", got)
	}
	if !strings.Contains(got.Error.Message, "no match for $.token") {
		t.Errorf("message = %q, want it to name the assertion's error", got.Error.Message)
	}
}

// A skipped step is a <skipped>, so it shows as skipped rather than passing: a
// step nobody ran must not read as a step that worked.
func TestWriteJUnitSkippedStep(t *testing.T) {
	ran := &result.StepResult{Name: "login"}
	ran.Finish(time.Millisecond)
	skipped := &result.StepResult{Name: "get item"}
	skipped.Skip("a previous step failed")

	got := decodeJUnit(t, writeJUnit(t, runOf(ran, skipped)))
	suite := got.Suites[0]
	if suite.Skipped != 1 || suite.Failures != 0 || suite.Errors != 0 {
		t.Errorf("suite tallies = %+v, want one skip and nothing else", suite)
	}
	child := suite.Cases[1].Skipped
	if child == nil {
		t.Fatalf("a skipped step has no <skipped>: %+v", suite.Cases[1])
	}
	if child.Message != "a previous step failed" {
		t.Errorf("message = %q, want the skip reason", child.Message)
	}
}

// A scenario whose file would not load has no steps, so it would be an empty
// suite -- which a CI UI renders as nothing much, however high its errors
// attribute. It gets one synthetic errored case instead, so the load failure is
// something a person can see and click.
func TestWriteJUnitUnloadableScenarioGetsACase(t *testing.T) {
	sc := &result.ScenarioResult{File: "suite/03_broken.yaml"}
	sc.Fail(0, errors.New("suite/03_broken.yaml: yaml: line 4: did not find expected key"))
	run := &result.RunResult{StartedAt: startedAt, Scenarios: []*result.ScenarioResult{sc}}
	run.Finish()

	got := decodeJUnit(t, writeJUnit(t, run))
	suite := got.Suites[0]
	// No name to show: the path is all anyone knows about this scenario.
	if suite.Name != "suite/03_broken.yaml" {
		t.Errorf("suite name = %q, want the file for a scenario with no name", suite.Name)
	}
	if suite.Tests != 1 || suite.Errors != 1 {
		t.Errorf("suite tallies = %+v, want one case and one error", suite)
	}
	if got.Tests != 1 || got.Errors != 1 {
		t.Errorf("run tallies = tests %d errors %d, want the synthetic case counted", got.Tests, got.Errors)
	}
	one := suite.Cases[0]
	if one.Name != loadFailureCase {
		t.Errorf("case name = %q, want %q", one.Name, loadFailureCase)
	}
	if one.Error == nil || !strings.Contains(one.Error.Message, "did not find expected key") {
		t.Errorf("case = %+v, want an <error> holding the reason the file would not load", one)
	}
}

// The tallies are what a reporter adds up, so they have to count the cases
// actually emitted rather than echo the run's own scenario counts.
func TestWriteJUnitTallies(t *testing.T) {
	ok := &result.StepResult{Name: "login"}
	ok.Finish(time.Millisecond)
	errored := &result.StepResult{Name: "fetch"}
	errored.Fail(time.Millisecond, errors.New("boom"))
	skipped := &result.StepResult{Name: "check"}
	skipped.Skip("a previous step failed")

	mixed := &result.ScenarioResult{Name: "items", File: "suite/items.yaml",
		Steps: []*result.StepResult{ok, failedStep(), errored, skipped}}
	mixed.Finish(20 * time.Millisecond)
	broken := &result.ScenarioResult{File: "suite/broken.yaml"}
	broken.Fail(0, errors.New("suite/broken.yaml: unreadable"))

	run := &result.RunResult{StartedAt: startedAt, Scenarios: []*result.ScenarioResult{mixed, broken}}
	run.Finish()

	got := decodeJUnit(t, writeJUnit(t, run))
	if got.Tests != 5 || got.Failures != 1 || got.Errors != 2 || got.Skipped != 1 {
		t.Errorf("run tallies = tests %d failures %d errors %d skipped %d, want 5/1/2/1",
			got.Tests, got.Failures, got.Errors, got.Skipped)
	}
	if s := got.Suites[0]; s.Tests != 4 || s.Failures != 1 || s.Errors != 1 || s.Skipped != 1 {
		t.Errorf("suite tallies = tests %d failures %d errors %d skipped %d, want 4/1/1/1",
			s.Tests, s.Failures, s.Errors, s.Skipped)
	}
}

// time is seconds, which is what a reporter parses it as. Milliseconds here
// would read as a thousandfold regression in every CI trend graph.
func TestWriteJUnitTimeIsSeconds(t *testing.T) {
	step := &result.StepResult{Name: "slow"}
	step.Finish(1500 * time.Millisecond)
	sc := &result.ScenarioResult{Name: "items", File: "suite/items.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(1600 * time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Duration: 1700 * time.Millisecond,
		Scenarios: []*result.ScenarioResult{sc}}

	got := decodeJUnit(t, writeJUnit(t, run))
	if got.Time != "1.700" {
		t.Errorf("testsuites time = %q, want 1.700", got.Time)
	}
	if got.Suites[0].Time != "1.600" {
		t.Errorf("testsuite time = %q, want 1.600", got.Suites[0].Time)
	}
	if got.Suites[0].Cases[0].Time != "1.500" {
		t.Errorf("testcase time = %q, want 1.500", got.Suites[0].Cases[0].Time)
	}
}

// An empty run is a document saying nothing ran, and a nil one is the same
// rather than a crash: a caller that asked for a report gets one either way.
func TestWriteJUnitEmptyAndNilRun(t *testing.T) {
	for name, run := range map[string]*result.RunResult{
		"empty": {StartedAt: startedAt, Status: result.StatusPass},
		"nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			got := decodeJUnit(t, writeJUnit(t, run))
			if got.Tests != 0 || len(got.Suites) != 0 {
				t.Errorf("got tests %d and %d suites, want an empty report", got.Tests, len(got.Suites))
			}
			if got.Name != junitSuiteName {
				t.Errorf("name = %q, want %q", got.Name, junitSuiteName)
			}
		})
	}
}

// A response body or an exec step's output can hold bytes XML 1.0 forbids. The
// document still has to parse -- an unparseable report is a CI job with no
// results at all, which is worse than one with a mangled message.
func TestWriteJUnitSurvivesBytesXMLForbids(t *testing.T) {
	step := &result.StepResult{Name: "get item"}
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.msg", Operator: "equals",
		Expected: "ok", Actual: "\x1b[31mboom\x00 <&\"'>",
	}.Fail())
	step.Finish(time.Millisecond)

	doc := writeJUnit(t, runOf(step))
	got := onlyCase(t, doc) // parsing it is the assertion
	if got.Failure == nil || !strings.Contains(got.Failure.Text, "boom") {
		t.Errorf("the failure lost its message:\n%s", doc)
	}
	for _, forbidden := range []string{"\x1b", "\x00"} {
		if strings.Contains(doc, forbidden) {
			t.Errorf("the document holds a byte XML 1.0 forbids:\n%q", doc)
		}
	}
}

// A write that fails has to be reported: ART-10's writeReports turns it into a
// failed command, and a report nobody could write must not pass for a written
// one.
func TestWriteJUnitWriteError(t *testing.T) {
	err := WriteJUnit(failingWriter{}, runOf(failedStep()))
	if err == nil {
		t.Fatal("WriteJUnit() = nil, want the write error")
	}
	if !strings.Contains(err.Error(), "JUnit report") {
		t.Errorf("WriteJUnit() = %q, want it to name the report", err)
	}
}

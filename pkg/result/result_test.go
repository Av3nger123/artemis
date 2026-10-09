package result

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatusPassed(t *testing.T) {
	cases := map[Status]bool{
		StatusPass:  true,
		StatusSkip:  true,
		StatusFail:  false,
		StatusError: false,
	}
	for status, want := range cases {
		if got := status.Passed(); got != want {
			t.Errorf("Status(%q).Passed() = %v, want %v", status, got, want)
		}
	}
}

func TestStepFinishDerivesStatusFromAssertions(t *testing.T) {
	tests := []struct {
		name       string
		assertions []Status
		want       Status
	}{
		{"no assertions passes", nil, StatusPass},
		{"all pass", []Status{StatusPass, StatusPass}, StatusPass},
		{"one fail", []Status{StatusPass, StatusFail, StatusPass}, StatusFail},
		{"one error", []Status{StatusPass, StatusError}, StatusError},
		{"error beats fail", []Status{StatusFail, StatusError, StatusFail}, StatusError},
		{"skip does not fail", []Status{StatusPass, StatusSkip}, StatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := (&ScenarioResult{}).NewStep("s")
			for _, s := range tt.assertions {
				step.Assert(AssertionResult{Status: s})
			}
			step.Finish(3 * time.Millisecond)
			if step.Status != tt.want {
				t.Errorf("Status = %q, want %q", step.Status, tt.want)
			}
			if step.Duration != 3*time.Millisecond {
				t.Errorf("Duration = %v, want 3ms", step.Duration)
			}
		})
	}
}

func TestStepAssertStampsStepName(t *testing.T) {
	sc := &ScenarioResult{}
	step := sc.NewStep("create user")
	step.Assert(Assertion{Kind: "body", Path: "$.id", Operator: "exists"}.Pass())
	step.Assert(Assertion{Step: "explicit", Kind: "body"}.Pass())

	if got := step.Assertions[0].Step; got != "create user" {
		t.Errorf("Step = %q, want %q", got, "create user")
	}
	if got := step.Assertions[1].Step; got != "explicit" {
		t.Errorf("Step = %q, want %q -- an explicit step name must survive", got, "explicit")
	}
}

func TestStepFailIsErrorNotFail(t *testing.T) {
	step := (&ScenarioResult{}).NewStep("call")
	step.Fail(time.Second, errors.New("dial tcp: connection refused"))

	if step.Status != StatusError {
		t.Errorf("Status = %q, want %q", step.Status, StatusError)
	}
	if step.Error != "dial tcp: connection refused" {
		t.Errorf("Error = %q", step.Error)
	}
	if step.Passed() {
		t.Error("Passed() = true, want false")
	}
}

func TestStepSkip(t *testing.T) {
	step := (&ScenarioResult{}).NewStep("later")
	step.Skip("depends on a failed step")

	if step.Status != StatusSkip {
		t.Errorf("Status = %q, want %q", step.Status, StatusSkip)
	}
	if !step.Passed() {
		t.Error("Passed() = false, want true -- a skip must not fail the run")
	}
}

func TestScenarioFinishDerivesStatusFromSteps(t *testing.T) {
	tests := []struct {
		name  string
		steps []Status
		want  Status
	}{
		{"no steps passes", nil, StatusPass},
		{"all pass", []Status{StatusPass, StatusPass}, StatusPass},
		{"one fail", []Status{StatusPass, StatusFail}, StatusFail},
		{"errored step among passing ones", []Status{StatusPass, StatusError, StatusPass}, StatusError},
		{"error beats fail", []Status{StatusFail, StatusError}, StatusError},
		{"skip does not fail", []Status{StatusSkip, StatusPass}, StatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := NewRun().NewScenario("login", "login.yaml")
			for _, s := range tt.steps {
				step := sc.NewStep("s")
				step.Status = s
			}
			sc.Finish(10 * time.Millisecond)
			if sc.Status != tt.want {
				t.Errorf("Status = %q, want %q", sc.Status, tt.want)
			}
		})
	}
}

func TestScenarioFailSurvivesFinish(t *testing.T) {
	sc := NewRun().NewScenario("broken", "broken.yaml")
	sc.Fail(0, errors.New("yaml: line 3: mapping values are not allowed"))
	sc.Finish(time.Millisecond)

	if sc.Status != StatusError {
		t.Errorf("Status = %q, want %q -- a scenario that failed to load must stay errored", sc.Status, StatusError)
	}
	if sc.Duration != time.Millisecond {
		t.Errorf("Duration = %v, want 1ms", sc.Duration)
	}
}

func TestRunRollsUpFromDeepestAssertion(t *testing.T) {
	run := NewRun()
	sc := run.NewScenario("orders", "orders.yaml")
	ok := sc.NewStep("list orders")
	ok.Assert(Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200}.Pass())
	ok.Finish(time.Millisecond)
	bad := sc.NewStep("create order")
	bad.Assert(Assertion{Kind: "body", Path: "$.total", Operator: "equals", Expected: 42, Actual: 41}.Fail())
	bad.Finish(time.Millisecond)
	sc.Finish(2 * time.Millisecond)
	run.Finish()

	if bad.Passed() {
		t.Error("step Passed() = true, want false")
	}
	if sc.Passed() {
		t.Error("scenario Passed() = true, want false")
	}
	if run.Passed() {
		t.Error("run Passed() = true, want false")
	}
	if run.Status != StatusFail {
		t.Errorf("run Status = %q, want %q", run.Status, StatusFail)
	}
}

func TestRunFinishSetsDurationFromStartedAt(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	calls := 0
	run := newRunAt(func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(1500 * time.Millisecond)
	})
	run.Finish()

	if !run.StartedAt.Equal(start) {
		t.Errorf("StartedAt = %v, want %v", run.StartedAt, start)
	}
	if run.Duration != 1500*time.Millisecond {
		t.Errorf("Duration = %v, want 1.5s", run.Duration)
	}
}

func TestEmptyRunPasses(t *testing.T) {
	run := NewRun()
	run.Finish()
	if !run.Passed() {
		t.Error("Passed() = false, want true for a run with no scenarios")
	}
	if got := run.Counts(); got != (Counts{}) {
		t.Errorf("Counts() = %+v, want zero", got)
	}
}

// mixedRun is two scenarios: one that passes outright, one holding a failed
// assertion, an errored step and a skipped step.
func mixedRun(t *testing.T) *RunResult {
	t.Helper()
	run := NewRun()

	green := run.NewScenario("health", "health.yaml")
	ping := green.NewStep("ping")
	ping.Assert(Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200}.Pass())
	ping.Finish(time.Millisecond)
	green.Finish(time.Millisecond)

	red := run.NewScenario("checkout", "checkout.yaml")
	failing := red.NewStep("place order")
	failing.Assert(Assertion{Kind: "status_code", Operator: "equals", Expected: 201, Actual: 201}.Pass())
	failing.Assert(Assertion{Kind: "body", Path: "$.status", Operator: "equals", Expected: "paid", Actual: "pending"}.Fail())
	failing.Finish(2 * time.Millisecond)
	broken := red.NewStep("fetch receipt")
	broken.Assert(Assertion{Kind: "body", Path: "$.[", Operator: "exists"}.Errored(errors.New("bad jsonpath")))
	broken.Finish(time.Millisecond)
	red.NewStep("email receipt").Skip("previous step failed")
	red.Finish(3 * time.Millisecond)

	run.Finish()
	return run
}

func TestCountsOnMixedRun(t *testing.T) {
	run := mixedRun(t)
	got := run.Counts()

	want := Counts{
		Scenarios:  Tally{Total: 2, Passed: 1, Errored: 1},
		Steps:      Tally{Total: 4, Passed: 1, Failed: 1, Errored: 1, Skipped: 1},
		Assertions: Tally{Total: 4, Passed: 2, Failed: 1, Errored: 1},
	}

	if got != want {
		t.Errorf("Counts() = %+v, want %+v", got, want)
	}
	if run.Status != StatusError {
		t.Errorf("run Status = %q, want %q -- an errored step must beat a failed one", run.Status, StatusError)
	}
}

func TestFailuresListsEveryNonPassingAssertionInOrder(t *testing.T) {
	failures := mixedRun(t).Failures()

	if len(failures) != 2 {
		t.Fatalf("len(Failures()) = %d, want 2: %+v", len(failures), failures)
	}
	if failures[0].Path != "$.status" || failures[0].Status != StatusFail {
		t.Errorf("Failures()[0] = %+v", failures[0])
	}
	if failures[1].Path != "$.[" || failures[1].Status != StatusError {
		t.Errorf("Failures()[1] = %+v", failures[1])
	}
	if failures[0].Step != "place order" {
		t.Errorf("Failures()[0].Step = %q, want %q", failures[0].Step, "place order")
	}
}

func TestAssertionCarriesNonStringValues(t *testing.T) {
	step := (&ScenarioResult{}).NewStep("numbers")
	step.Assert(Assertion{
		Kind:     "body",
		Path:     "$.count",
		Operator: "gt",
		Expected: 10,
		Actual:   3.5,
	}.Fail())
	step.Assert(Assertion{
		Kind:     "body",
		Path:     "$.active",
		Operator: "equals",
		Expected: true,
		Actual:   nil,
	}.Fail())
	step.Finish(time.Millisecond)

	if got := step.Assertions[0].Expected; got != 10 {
		t.Errorf("Expected = %#v, want int 10", got)
	}
	if got := step.Assertions[0].Actual; got != 3.5 {
		t.Errorf("Actual = %#v, want float64 3.5", got)
	}
	if got := step.Assertions[1].Actual; got != nil {
		t.Errorf("Actual = %#v, want nil for an unresolved path", got)
	}
	if got := step.Assertions[1].Expected; got != true {
		t.Errorf("Expected = %#v, want bool true", got)
	}
}

func TestAssertionDescribe(t *testing.T) {
	tests := []struct {
		name string
		a    AssertionResult
		want string
	}{
		{
			"value mismatch",
			Assertion{Kind: "body", Path: "$.status", Operator: "equals", Expected: "paid", Actual: "pending"}.Fail(),
			"$.status equals paid, got pending",
		},
		{
			"no path falls back to kind",
			Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 500}.Fail(),
			"status_code equals 200, got 500",
		},
		{
			"errored assertion shows the error",
			Assertion{Kind: "body", Path: "$.[", Operator: "exists"}.Errored(errors.New("bad jsonpath")),
			"$.[ exists: bad jsonpath",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Describe(); got != tt.want {
				t.Errorf("Describe() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAllPassed(t *testing.T) {
	pass := Assertion{Kind: "status_code", Operator: "equals"}.Pass()
	fail := Assertion{Kind: "status_code", Operator: "equals"}.Fail()
	errored := Assertion{Kind: "capture", Operator: "exists"}.Errored(errors.New("no path given"))
	skipped := AssertionResult{Status: StatusSkip}

	cases := []struct {
		name string
		as   []AssertionResult
		want bool
	}{
		{"none", nil, true},
		{"all pass", []AssertionResult{pass, pass}, true},
		{"a skip still passes", []AssertionResult{pass, skipped}, true},
		{"one fails", []AssertionResult{pass, fail}, false},
		{"one errors", []AssertionResult{pass, errored}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllPassed(tc.as); got != tc.want {
				t.Errorf("AllPassed() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ART-54: the two secret marks have to survive the Assertion -> AssertionResult
// constructors, which is the one thing result does with them.
func TestAssertionCarriesSecretMarks(t *testing.T) {
	base := Assertion{
		Step: "one", Kind: "expect", Path: "pw", Operator: "equals",
		Expected: "wanted", Actual: "hunter2",
		ActualSecret: true,
	}
	for _, tc := range []struct {
		name string
		got  AssertionResult
	}{
		{"Fail", base.Fail()},
		{"Pass", base.Pass()},
		{"Errored", base.Errored(errors.New("boom"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.got.ActualSecret {
				t.Error("ActualSecret did not survive")
			}
			if tc.got.ExpectedSecret {
				t.Error("ExpectedSecret must stay false")
			}
			// The tree keeps the true value; redaction belongs to the writers.
			if tc.got.Actual != "hunter2" {
				t.Errorf("Actual = %v, want the true value", tc.got.Actual)
			}
		})
	}
}

// An assertion with no marks must be exactly what it was before ART-54, which is
// what keeps every existing golden byte-identical.
func TestAssertionWithoutMarksIsUnchanged(t *testing.T) {
	got := Assertion{Step: "one", Kind: "expect", Expected: 200, Actual: 404}.Fail()
	if got.ExpectedSecret || got.ActualSecret {
		t.Errorf("marks default to true: %+v", got)
	}
}

// Describe is the line the console prints under a failed step while the run is
// still going, and it leaked a credential until ART-54. This is the regression.
func TestDescribeRedactsASecretOperand(t *testing.T) {
	a := Assertion{
		Kind: "expect", Path: "body.given", Operator: "==",
		Expected: "hunter2", Actual: "not-the-password",
		ExpectedSecret: true,
	}.Fail()
	got := a.Describe()
	if strings.Contains(got, "hunter2") {
		t.Errorf("Describe() = %q, which leaks the credential", got)
	}
	if !strings.Contains(got, Redacted) {
		t.Errorf("Describe() = %q, want %q in it", got, Redacted)
	}
	if !strings.Contains(got, "not-the-password") {
		t.Errorf("Describe() = %q, want the other operand kept", got)
	}
}

// An assertion with no marks describes exactly as it did before ART-54.
func TestDescribeUnchangedWithoutMarks(t *testing.T) {
	a := Assertion{Kind: "expect", Path: "status", Operator: "==", Expected: 200, Actual: 404}.Fail()
	if got, want := a.Describe(), "status == 200, got 404"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

// Shown never alters the stored values: the tree stays faithful and redaction
// belongs to whatever renders it.
func TestShownLeavesTheTreeIntact(t *testing.T) {
	a := Assertion{Expected: "e", Actual: "a", ExpectedSecret: true, ActualSecret: true}.Fail()
	if e, ac := a.Shown(); e != Redacted || ac != Redacted {
		t.Errorf("Shown() = %v, %v, want both redacted", e, ac)
	}
	if a.Expected != "e" || a.Actual != "a" {
		t.Errorf("Shown() mutated the assertion: %+v", a)
	}
}

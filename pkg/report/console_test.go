package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"artemis/pkg/result"
)

// render runs f against a Console and returns everything it wrote.
func render(f func(c *Console)) string {
	var buf bytes.Buffer
	f(NewConsole(&buf))
	return buf.String()
}

func TestScenarioNamesTheFile(t *testing.T) {
	got := render(func(c *Console) {
		c.Scenario(&result.ScenarioResult{Name: "checkout", File: "checkout.yaml"})
	})

	if want := "scenario: checkout (checkout.yaml)\n"; got != want {
		t.Errorf("Scenario() = %q, want %q", got, want)
	}
}

func TestScenarioWithoutAFileOmitsTheParentheses(t *testing.T) {
	got := render(func(c *Console) {
		c.Scenario(&result.ScenarioResult{Name: "checkout"})
	})

	if want := "scenario: checkout\n"; got != want {
		t.Errorf("Scenario() = %q, want %q", got, want)
	}
}

// A scenario whose file would not load has no name: the walk never got far
// enough to read one, so the path is what identifies it.
func TestScenarioWithoutANamePrintsItsFile(t *testing.T) {
	got := render(func(c *Console) {
		c.Scenario(&result.ScenarioResult{File: "suite/broken.yaml"})
	})

	if want := "scenario: suite/broken.yaml\n"; got != want {
		t.Errorf("Scenario() = %q, want %q", got, want)
	}
}

func TestScenarioFailedPrintsWhyItNeverRan(t *testing.T) {
	got := render(func(c *Console) {
		c.ScenarioFailed(&result.ScenarioResult{
			File:   "suite/broken.yaml",
			Status: result.StatusError,
			Error:  "parse suite/broken.yaml: field respones not found",
		})
	})

	if want := "  ERROR parse suite/broken.yaml: field respones not found\n"; got != want {
		t.Errorf("ScenarioFailed() = %q, want %q", got, want)
	}
}

// A scenario that ran has no error to print, and its steps have already said
// everything: ScenarioFailed must add nothing.
func TestScenarioFailedPrintsNothingWithoutAnError(t *testing.T) {
	got := render(func(c *Console) {
		c.ScenarioFailed(&result.ScenarioResult{Name: "checkout", Status: result.StatusPass})
		c.ScenarioFailed(nil)
	})

	if got != "" {
		t.Errorf("ScenarioFailed() = %q, want nothing", got)
	}
}

func TestStepPassingIsOneLine(t *testing.T) {
	step := &result.StepResult{Name: "login", Status: result.StatusPass, Duration: 48 * time.Millisecond, Attempts: 1}
	step.Assertions = []result.AssertionResult{{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200, Status: result.StatusPass}}

	got := render(func(c *Console) { c.Step(step) })

	if lines := strings.Count(got, "\n"); lines != 1 {
		t.Fatalf("Step() printed %d lines, want 1:\n%s", lines, got)
	}
	for _, want := range []string{"ok", "login", "48ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("Step() = %q, want it to contain %q", got, want)
		}
	}
}

func TestStepFailedAssertionIsPrintedUnderTheStep(t *testing.T) {
	step := &result.StepResult{Name: "create order", Status: result.StatusFail, Duration: 12 * time.Millisecond, Attempts: 1}
	step.Assertions = []result.AssertionResult{
		{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200, Status: result.StatusPass},
		{Kind: "body", Path: "$.status", Operator: "equals", Expected: "ok", Actual: "pending", Status: result.StatusFail},
	}

	got := render(func(c *Console) { c.Step(step) })

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("Step() printed %d lines, want 2 (the step and its one failure):\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "FAIL") || !strings.Contains(lines[0], "create order") {
		t.Errorf("first line = %q, want the step marked FAIL", lines[0])
	}
	if !strings.HasPrefix(lines[1], " ") {
		t.Errorf("second line = %q, want it indented under the step", lines[1])
	}
	for _, want := range []string{"$.status", "equals", "ok", "pending"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("failure line = %q, want it to contain %q", lines[1], want)
		}
	}
}

func TestStepErroredPrintsItsError(t *testing.T) {
	step := &result.StepResult{Name: "fetch order", Status: result.StatusError, Duration: 2 * time.Millisecond, Attempts: 1,
		Error: `Get "http://127.0.0.1:1/orders": connection refused`}

	got := render(func(c *Console) { c.Step(step) })

	if !strings.Contains(got, "ERROR") {
		t.Errorf("Step() = %q, want it marked ERROR", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Step() = %q, want it to print the step's error", got)
	}
}

func TestStepSkippedIsMarkedSkip(t *testing.T) {
	step := &result.StepResult{Name: "cleanup", Status: result.StatusSkip}

	got := render(func(c *Console) { c.Step(step) })

	if !strings.Contains(got, "skip") {
		t.Errorf("Step() = %q, want it marked skip", got)
	}
}

func TestStepRetriedSaysHowManyAttempts(t *testing.T) {
	step := &result.StepResult{Name: "flaky", Status: result.StatusPass, Duration: time.Second, Attempts: 3}

	got := render(func(c *Console) { c.Step(step) })

	if !strings.Contains(got, "3 attempts") {
		t.Errorf("Step() = %q, want it to say it took 3 attempts", got)
	}
	if one := render(func(c *Console) {
		c.Step(&result.StepResult{Name: "steady", Status: result.StatusPass, Attempts: 1})
	}); strings.Contains(one, "attempt") {
		t.Errorf("Step() = %q, want no attempt count for a step that ran once", one)
	}
}

// passingRun is one scenario, two steps, three passing assertions.
func passingRun() *result.RunResult {
	run := &result.RunResult{Status: result.StatusPass, Duration: 48 * time.Millisecond}
	sc := run.NewScenario("checkout", "checkout.yaml")
	for _, name := range []string{"login", "order"} {
		step := sc.NewStep(name)
		step.Assertions = []result.AssertionResult{{Kind: "status_code", Status: result.StatusPass}}
	}
	sc.Steps[1].Assertions = append(sc.Steps[1].Assertions, result.AssertionResult{Kind: "body", Status: result.StatusPass})
	sc.Status = result.StatusPass
	return run
}

func TestSummaryOfAPassingRun(t *testing.T) {
	got := render(func(c *Console) { c.Summary(passingRun()) })

	for _, want := range []string{"Scenarios     1  (1 passed)", "Steps         2  (2 passed)", "Assertions    3  (3 passed)", "PASS in 48ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "failed") || strings.Contains(got, "skipped") {
		t.Errorf("Summary() names a category with nothing in it:\n%s", got)
	}
}

func TestSummaryCountsEveryOutcome(t *testing.T) {
	run := &result.RunResult{Status: result.StatusError, Duration: 62 * time.Millisecond}
	sc := run.NewScenario("checkout", "checkout.yaml")
	sc.Status = result.StatusError

	ok := sc.NewStep("login")
	ok.Assertions = []result.AssertionResult{{Status: result.StatusPass}}

	failed := sc.NewStep("order")
	failed.Status = result.StatusFail
	failed.Assertions = []result.AssertionResult{{Status: result.StatusPass}, {Status: result.StatusFail}}

	errored := sc.NewStep("fetch")
	errored.Status = result.StatusError

	skipped := sc.NewStep("cleanup")
	skipped.Status = result.StatusSkip

	got := render(func(c *Console) { c.Summary(run) })

	for _, want := range []string{
		"Scenarios     1  (1 errored)",
		"Steps         4  (1 passed, 1 failed, 1 errored, 1 skipped)",
		"Assertions    3  (2 passed, 1 failed)",
		"FAIL in 62ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() is missing %q:\n%s", want, got)
		}
	}
}

func TestSummaryOfAnEmptyRunPrintsZeros(t *testing.T) {
	got := render(func(c *Console) { c.Summary(&result.RunResult{Status: result.StatusPass}) })

	for _, want := range []string{"Scenarios     0", "Steps         0", "Assertions    0", "PASS in 0s"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() is missing %q:\n%s", want, got)
		}
	}
}

// A sub-millisecond step is the normal case against a local server; it must not
// read as having taken no time at all.
func TestSubMillisecondDurationsAreNotPrintedAsZero(t *testing.T) {
	got := render(func(c *Console) {
		c.Step(&result.StepResult{Name: "fast", Status: result.StatusPass, Duration: 400 * time.Microsecond, Attempts: 1})
	})

	if !strings.Contains(got, "<1ms") {
		t.Errorf("Step() = %q, want a sub-millisecond duration shown as <1ms", got)
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	// Nothing to assert but that it does not panic and has somewhere to write.
	c := Discard()
	c.Scenario(&result.ScenarioResult{Name: "x"})
	c.Step(&result.StepResult{Name: "y", Status: result.StatusPass})
	c.Summary(passingRun())
}

func TestNilArgumentsPrintNothing(t *testing.T) {
	got := render(func(c *Console) {
		c.Scenario(nil)
		c.Step(nil)
		c.Summary(nil)
	})

	if got != "" {
		t.Errorf("printed %q for nil results, want nothing", got)
	}
}

// Package report turns an ART-1 run result into something a person or another
// program can read. Today that is the terminal; the --report writers land beside
// it.
//
// It imports only pkg/result, so nothing in the runner has to know how a run is
// rendered.
package report

import (
	"fmt"
	"io"
	"time"

	"artemis/pkg/result"
)

// Console prints a run as it happens: a line per step, the assertions that did
// not pass under the step that made them, and a summary at the end.
//
// Output is plain ASCII with no colour and no terminal probing, so a run reads
// the same in a terminal and in a CI log.
type Console struct {
	out io.Writer
}

// NewConsole returns a Console writing to out.
func NewConsole(out io.Writer) *Console {
	return &Console{out: out}
}

// Discard returns a Console that prints nothing, for a caller that only wants
// the result tree.
func Discard() *Console {
	return NewConsole(io.Discard)
}

// marker is the fixed-width word in front of a step: the eye scans this column.
func marker(s result.Status) string {
	switch s {
	case result.StatusPass:
		return "ok   "
	case result.StatusFail:
		return "FAIL "
	case result.StatusError:
		return "ERROR"
	case result.StatusSkip:
		return "skip "
	default:
		return "?    "
	}
}

// dur renders a duration at millisecond resolution, which is as precisely as
// anyone reads a step's time.
func dur(d time.Duration) string {
	if d > 0 && d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

// Scenario announces a scenario before its steps run.
func (c *Console) Scenario(sc *result.ScenarioResult) {
	if sc == nil {
		return
	}
	switch {
	case sc.Name == "":
		// A scenario whose file would not load has no name to print: where it
		// lives is all anyone knows about it.
		fmt.Fprintf(c.out, "scenario: %s\n", sc.File)
	case sc.File != "":
		fmt.Fprintf(c.out, "scenario: %s (%s)\n", sc.Name, sc.File)
	default:
		fmt.Fprintf(c.out, "scenario: %s\n", sc.Name)
	}
}

// ScenarioFailed prints why a scenario never ran a step -- a file artemis could
// not load. Without it a folder run reports the failure only as a tally, and
// the reader is left to guess which file and why.
func (c *Console) ScenarioFailed(sc *result.ScenarioResult) {
	if sc == nil || sc.Error == "" {
		return
	}
	fmt.Fprintf(c.out, "  %s %s\n", marker(sc.Status), sc.Error)
}

// Step prints one finished step, and under it whatever went wrong: the step's
// own error when it never got as far as asserting, otherwise every assertion
// that did not pass.
func (c *Console) Step(step *result.StepResult) {
	if step == nil {
		return
	}
	fmt.Fprintf(c.out, "  %s %-24s %8s", marker(step.Status), step.Name, dur(step.Duration))
	if step.Attempts > 1 {
		fmt.Fprintf(c.out, "  (%d attempts)", step.Attempts)
	}
	fmt.Fprintln(c.out)

	if step.Error != "" {
		fmt.Fprintf(c.out, "         %s\n", step.Error)
	}
	for _, a := range step.Assertions {
		if a.Passed() {
			continue
		}
		fmt.Fprintf(c.out, "         %s\n", a.Describe())
	}
}

// Summary prints the run's tallies and its verdict. It is the last thing a run
// writes, and the one line a reader who scrolled past everything else needs.
func (c *Console) Summary(run *result.RunResult) {
	if run == nil {
		return
	}
	counts := run.Counts()
	fmt.Fprintln(c.out)
	c.tally("Scenarios", counts.Scenarios)
	c.tally("Steps", counts.Steps)
	c.tally("Assertions", counts.Assertions)

	verdict := "FAIL"
	if run.Passed() {
		verdict = "PASS"
	}
	fmt.Fprintf(c.out, "%s in %s\n", verdict, dur(run.Duration))
}

// tally prints one level's total with its breakdown. Categories with nothing in
// them are left out: a clean run should read "3 (3 passed)", not list four zeros.
func (c *Console) tally(label string, t result.Tally) {
	fmt.Fprintf(c.out, "%-11s %3d", label, t.Total)
	parts := []string{}
	for _, p := range []struct {
		n    int
		word string
	}{
		{t.Passed, "passed"},
		{t.Failed, "failed"},
		{t.Errored, "errored"},
		{t.Skipped, "skipped"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(c.out, "  (")
		for i, p := range parts {
			if i > 0 {
				fmt.Fprint(c.out, ", ")
			}
			fmt.Fprint(c.out, p)
		}
		fmt.Fprint(c.out, ")")
	}
	fmt.Fprintln(c.out)
}

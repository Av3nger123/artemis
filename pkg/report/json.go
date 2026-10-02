package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"artemis/pkg/result"
)

// SchemaVersion is the version of the JSON report document. It goes up when a
// key is removed or its meaning changes; adding a key does not change it.
//
// A consumer that has to guess whether a missing key means "this passed" or
// "this is an older artemis" is a consumer that breaks silently, so the version
// is the first thing in the document.
const SchemaVersion = 1

// The JSON report is one document per run, mirroring the result tree: a run of
// scenarios, a scenario of steps, a step of assertions. It holds everything that
// ran, not only what failed, so a reader can tell "no assertion failed" from
// "no assertion was made".
//
// The wire types below exist instead of `json:` tags on pkg/result because the
// document is a contract other programs parse while the tree's field names are
// ours to rename. The mapping is fromRun, and pkg/cli/testdata/report_json.golden
// is a whole document to read.
//
// Conventions, which hold at every level:
//
//   - Keys are snake_case, like the DSL and like the assertion kinds the tree
//     already carries ("status_code").
//   - status is one of "pass", "fail", "error", "skip".
//   - A duration is <thing>_duration_ms: a number of milliseconds, to microsecond
//     precision. Nanosecond integers are what time.Duration marshals to and
//     nobody reads them.
//   - A time is RFC 3339.
//   - Every key is always present with its zero value -- "error": "" -- so a jq
//     expression never has to tell absent from empty. expected and actual are the
//     exception: they are null when the assertion had no such value, because
//     either may legitimately be any JSON type.
//
// What is not here is anything the result tree does not hold: no request or
// response bodies, no headers, no line numbers. That is ART-12's ground.
type jsonRun struct {
	SchemaVersion int            `json:"schema_version"`
	StartedAt     string         `json:"started_at"`
	DurationMS    float64        `json:"duration_ms"`
	Status        string         `json:"status"`
	Passed        bool           `json:"passed"`
	Counts        jsonCounts     `json:"counts"`
	Scenarios     []jsonScenario `json:"scenarios"`
}

// jsonCounts is the run's tallies at each level, so a consumer does not have to
// walk the tree to say "3 of 40 assertions failed".
type jsonCounts struct {
	Scenarios  jsonTally `json:"scenarios"`
	Steps      jsonTally `json:"steps"`
	Assertions jsonTally `json:"assertions"`
}

type jsonTally struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
	Skipped int `json:"skipped"`
}

type jsonScenario struct {
	Name       string     `json:"name"`
	File       string     `json:"file"`
	Status     string     `json:"status"`
	DurationMS float64    `json:"duration_ms"`
	Error      string     `json:"error"`
	Steps      []jsonStep `json:"steps"`
}

type jsonStep struct {
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	DurationMS float64         `json:"duration_ms"`
	Attempts   int             `json:"attempts"`
	Error      string          `json:"error"`
	Assertions []jsonAssertion `json:"assertions"`
}

// jsonAssertion drops the tree's Step field: an assertion is already nested
// under the step that made it, and repeating the name in every one of them is
// noise a consumer has to learn to ignore.
type jsonAssertion struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}

// WriteJSON writes run to w as one JSON document, indented, with a trailing
// newline.
//
// Indented rather than compact: the document is read by people in review as
// often as by programs, and one that diffs line by line is worth the bytes.
//
// A nil run is an empty run, not a crash: a caller that asked for a report
// deserves a document saying nothing ran.
func WriteJSON(w io.Writer, run *result.RunResult) error {
	raw, err := json.MarshalIndent(fromRun(run), "", "  ")
	if err != nil {
		// Reachable only if a scenario put something unmarshalable in an
		// assertion's expected or actual value.
		return fmt.Errorf("encoding the JSON report: %w", err)
	}
	if _, err := w.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("writing the JSON report: %w", err)
	}
	return nil
}

// fromRun maps the result tree onto the wire types. Every slice is built with a
// non-nil backing array so an empty level marshals as [] rather than null: a
// consumer iterating scenarios should not have to handle both.
func fromRun(run *result.RunResult) jsonRun {
	if run == nil {
		run = &result.RunResult{}
	}
	out := jsonRun{
		SchemaVersion: SchemaVersion,
		StartedAt:     rfc3339(run.StartedAt),
		DurationMS:    millis(run.Duration),
		Status:        run.Status.String(),
		Passed:        run.Passed(),
		Counts:        fromCounts(run.Counts()),
		Scenarios:     make([]jsonScenario, 0, len(run.Scenarios)),
	}
	for _, sc := range run.Scenarios {
		if sc == nil {
			continue
		}
		out.Scenarios = append(out.Scenarios, fromScenario(sc))
	}
	return out
}

func fromScenario(sc *result.ScenarioResult) jsonScenario {
	out := jsonScenario{
		Name:       sc.Name,
		File:       sc.File,
		Status:     sc.Status.String(),
		DurationMS: millis(sc.Duration),
		Error:      sc.Error,
		Steps:      make([]jsonStep, 0, len(sc.Steps)),
	}
	for _, step := range sc.Steps {
		if step == nil {
			continue
		}
		out.Steps = append(out.Steps, fromStep(step))
	}
	return out
}

func fromStep(step *result.StepResult) jsonStep {
	out := jsonStep{
		Name:       step.Name,
		Status:     step.Status.String(),
		DurationMS: millis(step.Duration),
		Attempts:   step.Attempts,
		Error:      step.Error,
		Assertions: make([]jsonAssertion, 0, len(step.Assertions)),
	}
	for _, a := range step.Assertions {
		out.Assertions = append(out.Assertions, jsonAssertion{
			Kind:     a.Kind,
			Path:     a.Path,
			Operator: a.Operator,
			Expected: a.Expected,
			Actual:   a.Actual,
			Status:   a.Status.String(),
			Error:    a.Error,
		})
	}
	return out
}

func fromCounts(c result.Counts) jsonCounts {
	return jsonCounts{
		Scenarios:  fromTally(c.Scenarios),
		Steps:      fromTally(c.Steps),
		Assertions: fromTally(c.Assertions),
	}
}

func fromTally(t result.Tally) jsonTally {
	return jsonTally{
		Total:   t.Total,
		Passed:  t.Passed,
		Failed:  t.Failed,
		Errored: t.Errored,
		Skipped: t.Skipped,
	}
}

// millis renders a duration as milliseconds to microsecond precision. Three
// decimal places is as fine as anyone compares two runs, and rounding keeps the
// number short enough to read.
func millis(d time.Duration) float64 {
	return float64(d.Round(time.Microsecond)) / float64(time.Millisecond)
}

// rfc3339 renders a time, and an unset one as the empty string rather than as
// year one.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

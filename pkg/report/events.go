package report

import (
	"encoding/json"
	"io"
	"sync"

	"artemis/pkg/result"
)

// Events writes a run as it happens, one JSON object per line: `artemis run
// --events ndjson`. It is for a program watching a run -- a UI that ticks steps
// off as they finish -- where the JSON report is for one reading the outcome,
// and it is the report's companion rather than its replacement: the report is
// still the record of the run.
//
// Every result in an event is the JSON report's own wire type, so a consumer
// has one model for both: a step-end's result is a step of the report, a
// scenario-end's is a scenario of the report without its steps (they arrived
// one at a time), and run-end's is the report without its scenarios and
// failures.
//
// The events, in the order a run writes them:
//
//	run-start       the files the run will try, in run order
//	scenario-start  a scenario and the names of its steps, before any runs
//	step-start      a step about to run, by its index in the scenario
//	step-end        that step's result, assertions and all
//	scenario-end    the scenario's result
//	run-end         the run's summary
//
// A file that does not load is a scenario-start with no name and no steps,
// then a scenario-end with status "error" and the reason in error -- the
// errored scenario the report holds for it.
//
// Each event is one Write and nothing is buffered, so a reader at the other end
// of a pipe sees an event when it happens rather than when a buffer fills.
//
// A nil *Events writes nothing. That is what a run without --events has, and
// it is what lets the runner call these unconditionally.
//
// Under --jobs several files run at once and share one Events, so each write
// holds a mutex: lines from different files interleave, but never mix within a
// line. Every event names its file and scenario, which is how a reader tells
// them apart; the order within one file is the order above.
type Events struct {
	mu  sync.Mutex
	out io.Writer
}

// NewEvents returns an Events writing to out.
func NewEvents(out io.Writer) *Events {
	return &Events{out: out}
}

// The event types. "event" is first on every one, so a reader skimming a
// stream sees what each line is before what it holds.
type runStartEvent struct {
	Event string   `json:"event"`
	Files []string `json:"files"`
}

type scenarioStartEvent struct {
	Event    string   `json:"event"`
	File     string   `json:"file"`
	Scenario string   `json:"scenario"`
	Steps    []string `json:"steps"`
}

type stepStartEvent struct {
	Event    string `json:"event"`
	File     string `json:"file"`
	Scenario string `json:"scenario"`
	Step     string `json:"step"`
	Index    int    `json:"index"`
}

type stepEndEvent struct {
	Event    string   `json:"event"`
	File     string   `json:"file"`
	Scenario string   `json:"scenario"`
	Index    int      `json:"index"`
	Result   jsonStep `json:"result"`
}

type scenarioEndEvent struct {
	Event    string              `json:"event"`
	File     string              `json:"file"`
	Scenario string              `json:"scenario"`
	Result   jsonScenarioSummary `json:"result"`
}

type runEndEvent struct {
	Event  string         `json:"event"`
	Result jsonRunSummary `json:"result"`
}

// RunStart announces the files the run will try, in the order it will try them.
func (e *Events) RunStart(files []string) {
	if files == nil {
		files = []string{}
	}
	e.emit(runStartEvent{Event: "run-start", Files: files})
}

// ScenarioStart announces a scenario and the names of its steps, so a watcher
// can lay the whole scenario out before its first step runs. A file that would
// not load passes nil, which is written as [].
func (e *Events) ScenarioStart(sc *result.ScenarioResult, steps []string) {
	if steps == nil {
		steps = []string{}
	}
	e.emit(scenarioStartEvent{Event: "scenario-start", File: sc.File, Scenario: sc.Name, Steps: steps})
}

// StepStart announces the step at index, about to run.
func (e *Events) StepStart(sc *result.ScenarioResult, index int, name string) {
	e.emit(stepStartEvent{Event: "step-start", File: sc.File, Scenario: sc.Name, Step: name, Index: index})
}

// StepEnd is the step at index, finished. Its assertions are in its result:
// they are only known once the step ends.
func (e *Events) StepEnd(sc *result.ScenarioResult, index int, step *result.StepResult) {
	e.emit(stepEndEvent{Event: "step-end", File: sc.File, Scenario: sc.Name, Index: index, Result: fromStep(step)})
}

// ScenarioEnd is the scenario, finished, without its steps.
func (e *Events) ScenarioEnd(sc *result.ScenarioResult) {
	e.emit(scenarioEndEvent{Event: "scenario-end", File: sc.File, Scenario: sc.Name, Result: fromScenarioSummary(sc)})
}

// RunEnd is the run's summary: the report without its scenarios and failures.
func (e *Events) RunEnd(run *result.RunResult) {
	e.emit(runEndEvent{Event: "run-end", Result: fromRunSummary(run)})
}

// emit writes one event as one line.
func (e *Events) emit(v any) {
	if e == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		// Reachable only as WriteJSON's error is: an assertion holding a value
		// that will not marshal. The line is dropped rather than half written,
		// and a report of the same run fails on the same value and says so.
		return
	}
	// Unchecked, as the console's writes are: a watcher that has gone away
	// is not a reason to stop the run it was watching.
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.out.Write(append(raw, '\n'))
}

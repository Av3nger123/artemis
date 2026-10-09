package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"artemis/pkg/result"
)

// eventTree is the smallest interesting run, the one TestWriteJSONPinsTheSchema
// pins as a document: one scenario, one step that failed, one assertion that
// passed and one that did not.
func eventTree() (*result.RunResult, *result.ScenarioResult, *result.StepResult) {
	step := &result.StepResult{Name: "get item", Attempts: 1, Line: 5}
	step.Assert(result.Assertion{
		Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200, Line: 11,
	}.Pass())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.status", Operator: "equals",
		Expected: "ready", Actual: "pending", Line: 14,
	}.Fail())
	step.Finish(12*time.Millisecond + 500*time.Microsecond)

	sc := &result.ScenarioResult{Name: "items", File: "suite/items.art", Steps: []*result.StepResult{step}}
	sc.Finish(13 * time.Millisecond)

	run := &result.RunResult{
		StartedAt: startedAt,
		Duration:  14 * time.Millisecond,
		Status:    result.StatusFail,
		Scenarios: []*result.ScenarioResult{sc},
	}
	return run, sc, step
}

// Every event of a whole run, pinned byte for byte. The key names are the
// JSON report's, and a consumer reads one model for both, so a rename here
// has to show up as a diff rather than as a broken watcher.
func TestEventsPinTheStream(t *testing.T) {
	run, sc, step := eventTree()

	var buf bytes.Buffer
	e := NewEvents(&buf)
	e.RunStart([]string{"suite/items.art"})
	e.ScenarioStart(sc, []string{"get item"})
	e.StepStart(sc, 0, "get item")
	e.StepEnd(sc, 0, step)
	e.ScenarioEnd(sc)
	e.RunEnd(run)

	want := strings.Join([]string{
		`{"event":"run-start","files":["suite/items.art"]}`,
		`{"event":"scenario-start","file":"suite/items.art","scenario":"items","steps":["get item"]}`,
		`{"event":"step-start","file":"suite/items.art","scenario":"items","step":"get item","index":0}`,
		`{"event":"step-end","file":"suite/items.art","scenario":"items","index":0,"result":{"name":"get item","status":"fail","duration_ms":12.5,"attempts":1,"line":5,"error":"","screenshot":"","assertions":[{"kind":"status_code","path":"","operator":"equals","expected":200,"actual":200,"status":"pass","error":"","line":11},{"kind":"body","path":"$.status","operator":"equals","expected":"ready","actual":"pending","status":"fail","error":"","line":14}]}}`,
		`{"event":"scenario-end","file":"suite/items.art","scenario":"items","result":{"name":"items","file":"suite/items.art","status":"fail","duration_ms":13,"error":""}}`,
		`{"event":"run-end","result":{"schema_version":1,"started_at":"2026-03-04T05:06:07Z","duration_ms":14,"status":"fail","passed":false,"error":"","counts":{"scenarios":{"total":1,"passed":0,"failed":1,"errored":0,"skipped":0},"steps":{"total":1,"passed":0,"failed":1,"errored":0,"skipped":0},"assertions":{"total":2,"passed":1,"failed":1,"errored":0,"skipped":0}}}}`,
	}, "\n") + "\n"
	if got := buf.String(); got != want {
		t.Errorf("the stream does not match\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// A file that would not load has no name and no steps: the start says so with
// "" and [] -- never null, so a consumer iterating steps has one case -- and the
// end carries the reason, as the report's errored scenario does.
func TestEventsOfAFileThatWouldNotLoad(t *testing.T) {
	sc := &result.ScenarioResult{File: "broken.art", Status: result.StatusPass}
	sc.Fail(0, errors.New(`broken.art:4:5: unknown field "timeot"`))

	var buf bytes.Buffer
	e := NewEvents(&buf)
	e.ScenarioStart(sc, nil)
	e.ScenarioEnd(sc)

	want := `{"event":"scenario-start","file":"broken.art","scenario":"","steps":[]}` + "\n" +
		`{"event":"scenario-end","file":"broken.art","scenario":"","result":{"name":"","file":"broken.art","status":"error","duration_ms":0,"error":"broken.art:4:5: unknown field \"timeot\""}}` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("the stream does not match\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// A run with no files is still a list, not null.
func TestEventsRunStartWithNoFilesIsAnEmptyList(t *testing.T) {
	var buf bytes.Buffer
	NewEvents(&buf).RunStart(nil)
	if got, want := buf.String(), `{"event":"run-start","files":[]}`+"\n"; got != want {
		t.Errorf("RunStart(nil) wrote %q, want %q", got, want)
	}
}

// scenario-end's result is the report's scenario without its steps, and
// run-end's is the report without its scenarios and failures: the same keys
// with the same values. Asserted against WriteJSON itself, so the two cannot
// drift apart without this failing.
func TestEventsCarryTheReportsOwnSummaries(t *testing.T) {
	run, sc, _ := eventTree()

	doc := decode(t, writeJSON(t, run))

	var buf bytes.Buffer
	e := NewEvents(&buf)
	e.ScenarioEnd(sc)
	e.RunEnd(run)
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}

	wantScenario := doc["scenarios"].([]any)[0].(map[string]any)
	delete(wantScenario, "steps")
	if got := decode(t, lines[0])["result"]; !reflect.DeepEqual(got, wantScenario) {
		t.Errorf("scenario-end result = %v, want the report's scenario without steps %v", got, wantScenario)
	}

	delete(doc, "scenarios")
	delete(doc, "failures")
	if got := decode(t, lines[1])["result"]; !reflect.DeepEqual(got, doc) {
		t.Errorf("run-end result = %v, want the report without scenarios and failures %v", got, doc)
	}
}

// A run without --events has a nil *Events, and the runner calls it anyway.
func TestNilEventsWritesNothing(t *testing.T) {
	run, sc, step := eventTree()
	var e *Events
	e.RunStart([]string{"a.art"})
	e.ScenarioStart(sc, []string{"get item"})
	e.StepStart(sc, 0, "get item")
	e.StepEnd(sc, 0, step)
	e.ScenarioEnd(sc)
	e.RunEnd(run)
}

// Each line is a whole JSON object a reader can decode on its own.
func TestEventsAreOneObjectPerLine(t *testing.T) {
	run, sc, step := eventTree()
	var buf bytes.Buffer
	e := NewEvents(&buf)
	e.ScenarioStart(sc, []string{"get item"})
	e.StepEnd(sc, 0, step)
	e.RunEnd(run)
	for i, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not one JSON object: %v\n%s", i+1, err, line)
		}
	}
}

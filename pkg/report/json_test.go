package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"artemis/pkg/result"
)

// startedAt is a fixed time so a document can be compared byte for byte.
var startedAt = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// writeJSON renders run and fails the test if it could not be written.
func writeJSON(t *testing.T, run *result.RunResult) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteJSON(&buf, run); err != nil {
		t.Fatalf("WriteJSON() = %v, want nil", err)
	}
	return buf.String()
}

// decode parses a document into a map, for a test that asks about one key
// rather than the whole shape.
func decode(t *testing.T, doc string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("the document is not valid JSON: %v\n%s", err, doc)
	}
	return got
}

// The schema, in full, for the smallest interesting run: one scenario, one step
// that failed, one assertion that passed and one that did not. Pinned byte for
// byte because the key names are a published contract -- a reviewer should see
// a rename here as a diff, not discover it from a broken consumer.
func TestWriteJSONPinsTheSchema(t *testing.T) {
	step := &result.StepResult{Name: "get item", Attempts: 1, Line: 5}
	step.Assert(result.Assertion{
		Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200, Line: 11,
	}.Pass())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.status", Operator: "equals",
		Expected: "ready", Actual: "pending", Line: 14,
	}.Fail())
	step.Finish(12*time.Millisecond + 500*time.Microsecond)

	sc := &result.ScenarioResult{Name: "items", File: "suite/items.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(13 * time.Millisecond)

	run := &result.RunResult{
		StartedAt: startedAt,
		Duration:  14 * time.Millisecond,
		Status:    result.StatusFail,
		Scenarios: []*result.ScenarioResult{sc},
	}

	want := `{
  "schema_version": 1,
  "started_at": "2026-03-04T05:06:07Z",
  "duration_ms": 14,
  "status": "fail",
  "passed": false,
  "error": "",
  "counts": {
    "scenarios": {
      "total": 1,
      "passed": 0,
      "failed": 1,
      "errored": 0,
      "skipped": 0
    },
    "steps": {
      "total": 1,
      "passed": 0,
      "failed": 1,
      "errored": 0,
      "skipped": 0
    },
    "assertions": {
      "total": 2,
      "passed": 1,
      "failed": 1,
      "errored": 0,
      "skipped": 0
    }
  },
  "scenarios": [
    {
      "name": "items",
      "file": "suite/items.yaml",
      "status": "fail",
      "duration_ms": 13,
      "error": "",
      "steps": [
        {
          "name": "get item",
          "status": "fail",
          "duration_ms": 12.5,
          "attempts": 1,
          "line": 5,
          "error": "",
          "screenshot": "",
          "trace": "",
          "assertions": [
            {
              "kind": "status_code",
              "path": "",
              "operator": "equals",
              "expected": 200,
              "actual": 200,
              "status": "pass",
              "error": "",
              "line": 11
            },
            {
              "kind": "body",
              "path": "$.status",
              "operator": "equals",
              "expected": "ready",
              "actual": "pending",
              "status": "fail",
              "error": "",
              "line": 14
            }
          ]
        }
      ]
    }
  ],
  "failures": [
    {
      "file": "suite/items.yaml",
      "line": 14,
      "scenario": "items",
      "step": "get item",
      "status": "fail",
      "kind": "body",
      "path": "$.status",
      "operator": "equals",
      "expected": "ready",
      "actual": "pending",
      "error": "",
      "screenshot": ""
    }
  ]
}
`
	if got := writeJSON(t, run); got != want {
		t.Errorf("WriteJSON() document does not match\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// A passing run says so at the top in two ways: the status word and the boolean
// a consumer can branch on without knowing the vocabulary.
func TestWriteJSONReportsAPassingRun(t *testing.T) {
	step := &result.StepResult{Name: "ping", Attempts: 1}
	step.Finish(time.Millisecond)
	sc := &result.ScenarioResult{Name: "health", File: "health.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusPass, Scenarios: []*result.ScenarioResult{sc}}

	got := decode(t, writeJSON(t, run))
	if got["status"] != "pass" {
		t.Errorf("status = %v, want pass", got["status"])
	}
	if got["passed"] != true {
		t.Errorf("passed = %v, want true", got["passed"])
	}
}

// A scenario whose file would not load has no steps and carries the reason. The
// document must hold both, or the one thing the reader needs is missing.
func TestWriteJSONKeepsAnErroredScenarioWithNoSteps(t *testing.T) {
	sc := &result.ScenarioResult{File: "suite/broken.yaml"}
	sc.Fail(0, errors.New("parse suite/broken.yaml: field respones not found"))
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusError, Scenarios: []*result.ScenarioResult{sc}}

	doc := writeJSON(t, run)
	for _, want := range []string{
		`"file": "suite/broken.yaml"`,
		`"status": "error"`,
		`"error": "parse suite/broken.yaml: field respones not found"`,
		`"steps": []`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document does not contain %s:\n%s", want, doc)
		}
	}
}

// A step that never asserted anything still reports why, and a skipped step is
// in the document rather than absent from it: "it did not run" and "it was never
// written" are different things.
func TestWriteJSONKeepsErroredAndSkippedSteps(t *testing.T) {
	bad := &result.StepResult{Name: "login", Attempts: 3}
	bad.Fail(900*time.Millisecond, errors.New("Post \"http://x/login\": connection refused"))
	skipped := &result.StepResult{Name: "logout"}
	skipped.Skip("login did not run")

	sc := &result.ScenarioResult{Name: "auth", File: "auth.yaml", Steps: []*result.StepResult{bad, skipped}}
	sc.Finish(900 * time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusError, Scenarios: []*result.ScenarioResult{sc}}

	doc := writeJSON(t, run)
	for _, want := range []string{
		`"error": "Post \"http://x/login\": connection refused"`,
		`"attempts": 3`,
		`"duration_ms": 900`,
		`"name": "logout"`,
		`"status": "skip"`,
		`"error": "login did not run"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document does not contain %s:\n%s", want, doc)
		}
	}
}

// An assertion's expected and actual keep the type they had: a consumer reading
// `.expected` for a numeric check must get a number, and an object must survive
// as an object. A value that was never there is null, not an empty string.
func TestWriteJSONPreservesAssertionValueTypes(t *testing.T) {
	step := &result.StepResult{Name: "shapes", Attempts: 1}
	step.Assert(result.Assertion{Kind: "body", Path: "$.count", Operator: "gt", Expected: 2, Actual: float64(3)}.Pass())
	step.Assert(result.Assertion{Kind: "body", Path: "$.ok", Operator: "equals", Expected: true, Actual: false}.Fail())
	step.Assert(result.Assertion{
		Kind: "body", Path: "$.item", Operator: "equals",
		Expected: map[string]any{"id": float64(1)}, Actual: []any{float64(1), "two"},
	}.Fail())
	step.Assert(result.Assertion{Kind: "body", Path: "$.missing", Operator: "exists", Expected: true}.Errored(errors.New("no match for $.missing")))
	step.Finish(time.Millisecond)

	sc := &result.ScenarioResult{Name: "shapes", File: "shapes.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusFail, Scenarios: []*result.ScenarioResult{sc}}

	doc := decode(t, writeJSON(t, run))
	scenarios, ok := doc["scenarios"].([]any)
	if !ok || len(scenarios) != 1 {
		t.Fatalf("scenarios = %v, want one", doc["scenarios"])
	}
	steps := scenarios[0].(map[string]any)["steps"].([]any)
	asserts := steps[0].(map[string]any)["assertions"].([]any)
	if len(asserts) != 4 {
		t.Fatalf("got %d assertions, want 4", len(asserts))
	}

	first := asserts[0].(map[string]any)
	if first["expected"] != float64(2) || first["actual"] != float64(3) {
		t.Errorf("numeric assertion = %v, want numbers 2 and 3", first)
	}
	second := asserts[1].(map[string]any)
	if second["expected"] != true || second["actual"] != false {
		t.Errorf("boolean assertion = %v, want booleans", second)
	}
	third := asserts[2].(map[string]any)
	if _, ok := third["expected"].(map[string]any); !ok {
		t.Errorf("expected = %#v, want an object", third["expected"])
	}
	if _, ok := third["actual"].([]any); !ok {
		t.Errorf("actual = %#v, want an array", third["actual"])
	}
	fourth := asserts[3].(map[string]any)
	if fourth["actual"] != nil {
		t.Errorf("actual = %#v, want null for an assertion that found nothing", fourth["actual"])
	}
	if fourth["status"] != "error" || fourth["error"] != "no match for $.missing" {
		t.Errorf("errored assertion = %v, want the reason", fourth)
	}
}

// A run with nothing in it is still a document, and its empty levels are arrays:
// a consumer iterating scenarios should never have to handle null as well.
func TestWriteJSONRendersAnEmptyRunAsArraysNotNull(t *testing.T) {
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusPass}

	doc := writeJSON(t, run)
	if !strings.Contains(doc, `"scenarios": []`) {
		t.Errorf("document does not contain an empty scenarios array:\n%s", doc)
	}
	if strings.Contains(doc, "null") {
		t.Errorf("document contains null:\n%s", doc)
	}
}

// Nil is the one thing a caller can hand over by accident, and it must produce a
// document saying nothing ran rather than a panic.
func TestWriteJSONOfNilIsAnEmptyRun(t *testing.T) {
	doc := decode(t, writeJSON(t, nil))
	if doc["schema_version"] != float64(SchemaVersion) {
		t.Errorf("schema_version = %v, want %d", doc["schema_version"], SchemaVersion)
	}
	if doc["started_at"] != "" {
		t.Errorf("started_at = %v, want empty for a run that never began", doc["started_at"])
	}
}

// A gated run and a run of an empty suite both hold no scenarios, and until
// now both produced the same document. error is what tells them apart, so it
// has to carry the gate's text and not move schema_version doing it -- adding
// a key is not the kind of change that bumps the version.
func TestWriteJSONCarriesARunLevelError(t *testing.T) {
	run := result.NewRun()
	run.Error = "the environment is not complete: no value for API_URL"
	run.Finish()

	doc := decode(t, writeJSON(t, run))
	if doc["error"] != run.Error {
		t.Errorf("error = %q, want %q", doc["error"], run.Error)
	}
	if doc["schema_version"] != float64(SchemaVersion) {
		t.Errorf("schema_version = %v, want %d; a new key does not move it", doc["schema_version"], SchemaVersion)
	}
}

// failingWriter reports an error on every write, standing in for a full disk or
// a closed pipe.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A document that could not be written is an error the caller must be able to
// fail on: a CI job whose report silently vanished is worse than one that failed.
func TestWriteJSONReturnsAWriteError(t *testing.T) {
	err := WriteJSON(failingWriter{}, &result.RunResult{StartedAt: startedAt})
	if err == nil {
		t.Fatal("WriteJSON() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("WriteJSON() = %v, want it to name the underlying error", err)
	}
}

// Sub-millisecond work is a number, not "<1ms": the console rounds for a reader,
// a document must stay comparable.
func TestWriteJSONRendersSubMillisecondDurations(t *testing.T) {
	step := &result.StepResult{Name: "fast", Attempts: 1}
	step.Finish(250 * time.Microsecond)
	sc := &result.ScenarioResult{Name: "fast", File: "fast.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(250 * time.Microsecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusPass, Scenarios: []*result.ScenarioResult{sc}}

	if doc := writeJSON(t, run); !strings.Contains(doc, `"duration_ms": 0.25`) {
		t.Errorf("document does not contain 0.25 ms:\n%s", doc)
	}
}

// A passing run's failures array is [], never null: a consumer iterating it
// should not have to handle both.
func TestWriteJSONFailuresIsAnEmptyArrayForAPassingRun(t *testing.T) {
	step := &result.StepResult{Name: "ping", Attempts: 1, Line: 5}
	step.Assert(result.Assertion{Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200, Line: 11}.Pass())
	step.Finish(time.Millisecond)
	sc := &result.ScenarioResult{Name: "s", File: "s.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusPass, Scenarios: []*result.ScenarioResult{sc}}

	doc := writeJSON(t, run)
	if !strings.Contains(doc, `"failures": []`) {
		t.Errorf("WriteJSON() does not carry an empty failures array:\n%s", doc)
	}
}

// A step that could not run, and a file that would not load, are both in failures
// with their reason and no check behind them.
func TestWriteJSONFailuresCoversAStepAndAScenarioThatCouldNotRun(t *testing.T) {
	step := &result.StepResult{Name: "fetch", Line: 6}
	step.Fail(time.Millisecond, errors.New("connection refused"))
	ran := &result.ScenarioResult{Name: "s", File: "s.yaml", Steps: []*result.StepResult{step}}
	ran.Finish(time.Millisecond)
	broken := &result.ScenarioResult{File: "broken.yaml"}
	broken.Fail(0, errors.New("will not parse"))

	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusError,
		Scenarios: []*result.ScenarioResult{ran, broken}}

	failures, ok := decode(t, writeJSON(t, run))["failures"].([]any)
	if !ok || len(failures) != 2 {
		t.Fatalf("failures = %v, want two entries", decode(t, writeJSON(t, run))["failures"])
	}
	first := failures[0].(map[string]any)
	if first["step"] != "fetch" || first["line"] != 6.0 || first["error"] != "connection refused" {
		t.Errorf("failures[0] = %v, want the step, its line and its reason", first)
	}
	if first["kind"] != "" || first["expected"] != nil {
		t.Errorf("failures[0] = %v, want no check behind it", first)
	}
	second := failures[1].(map[string]any)
	if second["file"] != "broken.yaml" || second["step"] != "" || second["error"] != "will not parse" {
		t.Errorf("failures[1] = %v, want the file and its reason", second)
	}
}

// The line on a step and on an assertion is the tree's, verbatim: zero when
// artemis does not know, not omitted and not guessed at.
func TestWriteJSONCarriesAnUnknownLineAsZero(t *testing.T) {
	step := &result.StepResult{Name: "ping", Attempts: 1}
	step.Assert(result.Assertion{Kind: "body", Path: "$.x", Operator: "equals", Expected: 1, Actual: 2}.Fail())
	step.Finish(time.Millisecond)
	sc := &result.ScenarioResult{Name: "s", File: "s.yaml", Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Status: result.StatusFail, Scenarios: []*result.ScenarioResult{sc}}

	doc := decode(t, writeJSON(t, run))
	scenarios := doc["scenarios"].([]any)
	gotStep := scenarios[0].(map[string]any)["steps"].([]any)[0].(map[string]any)
	if gotStep["line"] != 0.0 {
		t.Errorf("step line = %v, want 0", gotStep["line"])
	}
	if a := gotStep["assertions"].([]any)[0].(map[string]any); a["line"] != 0.0 {
		t.Errorf("assertion line = %v, want 0", a["line"])
	}
}

// ART-54: report.json is the file a CI job uploads as an artifact, so a
// credential reaching it is the leak this feature exists to stop. Both places
// json.go writes an operand are covered: the assertion list and the failure
// list.
func TestJSONRedactsASecretOperand(t *testing.T) {
	step := &result.StepResult{Name: "compare", Line: 4}
	step.Assert(result.Assertion{
		Kind: "expect", Path: "pw", Operator: "equals",
		Expected: "wanted", Actual: "hunter2", Line: 6,
		ActualSecret: true,
	}.Fail())
	step.Finish(time.Millisecond)

	sc := &result.ScenarioResult{Name: "secrets", File: "s.art", Steps: []*result.StepResult{step}}
	sc.Finish(time.Millisecond)
	run := result.NewRun()
	run.Scenarios = []*result.ScenarioResult{sc}
	run.Finish()

	var buf bytes.Buffer
	if err := WriteJSON(&buf, run); err != nil {
		t.Fatalf("WriteJSON() = %v, want nil", err)
	}
	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Errorf("the credential reached report.json:\n%s", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Errorf("want %q in report.json:\n%s", Redacted, out)
	}
	if !strings.Contains(out, "wanted") {
		t.Errorf("the other operand must survive:\n%s", out)
	}
	// Two writes, so two placeholders: the assertion entry and the failure
	// entry. A redaction that covered only one would still leak.
	if n := strings.Count(out, Redacted); n < 2 {
		t.Errorf("found %d placeholders, want the assertion entry and the failure entry:\n%s", n, out)
	}
}

// A step and an assertion a use brought in from a collection carry a file key;
// one written in the scenario carries none, so a run that never meets a
// collection writes the document it always did.
func TestWriteJSONNamesAFileOnlyWhenItIsNotTheScenarios(t *testing.T) {
	from := &result.StepResult{Name: "auth.login", Line: 2, File: "auth.art"}
	from.Assert(result.Assertion{Kind: "expect", Line: 6, File: "auth.art"}.Fail())
	from.Finish(time.Millisecond)
	own := &result.StepResult{Name: "orders", Line: 9}
	own.Assert(result.Assertion{Kind: "expect", Line: 10}.Pass())
	own.Finish(time.Millisecond)
	sc := &result.ScenarioResult{Name: "checkout", File: "checkout.art", Steps: []*result.StepResult{from, own}}
	sc.Finish(time.Millisecond)
	run := result.NewRun()
	run.Scenarios = []*result.ScenarioResult{sc}
	run.Finish()

	doc := decode(t, writeJSON(t, run))
	steps := doc["scenarios"].([]any)[0].(map[string]any)["steps"].([]any)
	first, second := steps[0].(map[string]any), steps[1].(map[string]any)
	if first["file"] != "auth.art" || first["assertions"].([]any)[0].(map[string]any)["file"] != "auth.art" {
		t.Errorf("the used step = %v", first)
	}
	if _, has := second["file"]; has {
		t.Errorf("a step written in the scenario has a file key: %v", second)
	}
	if _, has := second["assertions"].([]any)[0].(map[string]any)["file"]; has {
		t.Errorf("an assertion written in the scenario has a file key: %v", second)
	}
	if f := doc["failures"].([]any)[0].(map[string]any); f["file"] != "auth.art" || f["line"] != float64(6) {
		t.Errorf("failure = %v", f)
	}
}

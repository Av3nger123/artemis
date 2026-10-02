package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var initOnce sync.Once

// initLog points the package logger at a throwaway file, so a test that wants to
// see the records has somewhere to find them. It is not needed to keep the
// runner from panicking: logger.Logger discards until it is pointed somewhere.
func initLog(t *testing.T) {
	t.Helper()
	closer, err := logger.InitLog(filepath.Join(t.TempDir(), "test.log"))
	if err != nil {
		t.Fatalf("InitLog() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = closer.Close() })
}

// runScenario runs a scenario with its output thrown away, against the registry
// the binary uses. The tests that care about what is printed build their own
// Console; everything else only wants the result tree.
func runScenario(config models.Config, filePath string) *result.RunResult {
	return executeSteps(executor.Default(), config, filePath, report.Discard())
}

// okServer answers every request with the given status and body.
func okServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func apiStep(name, url string, wantStatus int, checks ...models.BodyCheck) models.Step {
	return models.Step{
		Name:     name,
		Type:     "api",
		Request:  models.Request{URL: url, Method: http.MethodGet},
		Response: models.Response{StatusCode: wantStatus, Body: checks},
	}
}

func scenario(steps ...models.Step) models.Config {
	return models.Config{Name: "scenario", Type: "functional", Steps: steps}
}

func TestExecuteStepsPassingScenarioPasses(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)

	run := runScenario(scenario(apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})), "scenario.yaml")

	if !run.Passed() {
		t.Fatalf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	if got := run.Counts(); got.Assertions.Total != 2 || got.Assertions.Passed != 2 {
		t.Errorf("assertion tally = %+v, want 2 passed (status code and body)", got.Assertions)
	}
	if run.Scenarios[0].Steps[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", run.Scenarios[0].Steps[0].Attempts)
	}
}

func TestExecuteStepsFailedBodyAssertionFailsTheRun(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"pending"}`)

	run := runScenario(scenario(apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for a failing body assertion")
	}
	if run.Status != result.StatusFail {
		t.Errorf("run.Status = %q, want %q", run.Status, result.StatusFail)
	}
	failures := run.Failures()
	if len(failures) != 1 || failures[0].Path != "$.status" || failures[0].Actual != "pending" {
		t.Fatalf("Failures() = %+v, want one $.status mismatch with actual %q", failures, "pending")
	}
	if got := runFailedError(run).Error(); !strings.Contains(got, "1 of 1 steps failed") {
		t.Errorf("error = %q, want it to name the failed step count", got)
	}
}

func TestExecuteStepsWrongStatusCodeFailsTheRun(t *testing.T) {
	initLog(t)
	srv := okServer(t, 500, `{"error":"boom"}`)

	run := runScenario(scenario(apiStep("ping", srv.URL, 200)), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for a 500 where 200 was expected")
	}
	failures := run.Failures()
	if len(failures) != 1 || failures[0].Kind != "status_code" {
		t.Fatalf("Failures() = %+v, want one status_code failure", failures)
	}
	if failures[0].Expected != 200 || failures[0].Actual != 500 {
		t.Errorf("expected/actual = %v/%v, want 200/500", failures[0].Expected, failures[0].Actual)
	}
}

func TestExecuteStepsUnreachableURLErrorsTheStep(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{}`)
	url := srv.URL
	srv.Close() // nothing is listening there any more

	run := runScenario(scenario(apiStep("ping", url, 200)), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false when the request cannot be made")
	}
	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusError {
		t.Errorf("step.Status = %q, want %q", step.Status, result.StatusError)
	}
	if step.Error == "" {
		t.Error("step.Error is empty, want the transport error")
	}
	if run.Status != result.StatusError {
		t.Errorf("run.Status = %q, want %q", run.Status, result.StatusError)
	}
}

// recordSleeps replaces the retry loop's sleep with one that records what it
// was asked to wait, so a retry test runs in microseconds and can assert the
// exact delays.
func recordSleeps(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	original := sleep
	sleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { sleep = original })
	return &slept
}

// countingServer answers with the given statuses in order, repeating the last
// one, and counts the requests it saw. The count is read while the server may
// still be serving, so it is atomic.
func countingServer(t *testing.T, statuses ...int) (*httptest.Server, func() int) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		status := statuses[len(statuses)-1]
		if n < len(statuses) {
			status = statuses[n]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { return int(atomic.LoadInt32(&calls)) }
}

func TestExecuteStepsOmittedRetryStillSendsTheRequestOnce(t *testing.T) {
	initLog(t)
	slept := recordSleeps(t)
	srv, calls := countingServer(t, 200)

	step := apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})
	step.Retry = models.Retry{} // what an omitted retry: parses to

	run := runScenario(scenario(step), "scenario.yaml")

	if !run.Passed() {
		t.Fatalf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	if calls() != 1 {
		t.Errorf("server saw %d calls, want 1", calls())
	}
	if got := run.Scenarios[0].Steps[0].Attempts; got != 1 {
		t.Errorf("Attempts = %d, want 1", got)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleep at all", *slept)
	}
}

func TestExecuteStepsRetriesEveryAttemptAndSleepsBetweenThem(t *testing.T) {
	initLog(t)
	slept := recordSleeps(t)
	srv, calls := countingServer(t, 500)

	step := apiStep("ping", srv.URL, 200)
	step.Retry = models.Retry{Times: 3, Delay: "10ms"}

	run := runScenario(scenario(step), "scenario.yaml")

	if run.Passed() {
		t.Error("run.Passed() = true, want false -- every attempt returned 500")
	}
	if calls() != 3 {
		t.Errorf("server saw %d calls, want 3", calls())
	}
	if got := run.Scenarios[0].Steps[0].Attempts; got != 3 {
		t.Errorf("Attempts = %d, want 3", got)
	}
	want := []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	if len(*slept) != len(want) || (*slept)[0] != want[0] || (*slept)[1] != want[1] {
		t.Errorf("slept %v, want %v -- between attempts only", *slept, want)
	}
}

func TestExecuteStepsStopsRetryingOnceAnAttemptPasses(t *testing.T) {
	initLog(t)
	slept := recordSleeps(t)
	srv, calls := countingServer(t, 500, 200, 200)

	step := apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})
	step.Retry = models.Retry{Times: 3, Delay: "10ms"}

	run := runScenario(scenario(step), "scenario.yaml")

	if !run.Passed() {
		t.Fatalf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	if calls() != 2 {
		t.Errorf("server saw %d calls, want 2 -- attempt 2 passed", calls())
	}
	if got := run.Scenarios[0].Steps[0].Attempts; got != 2 {
		t.Errorf("Attempts = %d, want 2", got)
	}
	if len(*slept) != 1 {
		t.Errorf("slept %v, want one 10ms wait", *slept)
	}
	if got := run.Counts(); got.Assertions.Failed != 0 || got.Assertions.Passed != 2 {
		t.Errorf("assertion tally = %+v, want only the passing attempt's 2", got.Assertions)
	}
}

func TestExecuteStepsRetryWithoutDelayDoesNotSleep(t *testing.T) {
	initLog(t)
	slept := recordSleeps(t)
	srv, calls := countingServer(t, 500)

	step := apiStep("ping", srv.URL, 200)
	step.Retry = models.Retry{Times: 2}

	runScenario(scenario(step), "scenario.yaml")

	if calls() != 2 {
		t.Errorf("server saw %d calls, want 2", calls())
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleep -- there is no default delay", *slept)
	}
}

func TestExecuteStepsBadRetryDelayFailsTheStepBeforeAnyRequest(t *testing.T) {
	initLog(t)
	srv, calls := countingServer(t, 200)

	step := apiStep("ping", srv.URL, 200)
	step.Retry = models.Retry{Times: 2, Delay: "soon"}

	run := runScenario(scenario(step), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for an unparseable retry delay")
	}
	got := run.Scenarios[0].Steps[0]
	if got.Status != result.StatusError {
		t.Errorf("step.Status = %q, want %q", got.Status, result.StatusError)
	}
	if !strings.Contains(got.Error, "soon") {
		t.Errorf("step.Error = %q, want it to name the bad delay", got.Error)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want 0 -- nothing should be sent", calls())
	}
}

func TestExecuteStepsReportsTheKeptAttemptsTransportError(t *testing.T) {
	initLog(t)
	recordSleeps(t)

	// The first request is answered 500, failing attempt 1's status assertion;
	// every one after that has its connection dropped without an answer, so
	// attempt 2 cannot complete at all.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack() = %v, want nil", err)
			return
		}
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	step := apiStep("ping", srv.URL, 200)
	step.Retry = models.Retry{Times: 2, Delay: "10ms"}

	run := runScenario(scenario(step), "scenario.yaml")

	got := run.Scenarios[0].Steps[0]
	if got.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", got.Attempts)
	}
	if got.Status != result.StatusError {
		t.Fatalf("step.Status = %q, want %q -- the kept attempt errored", got.Status, result.StatusError)
	}
	if got.Error == "" {
		t.Error("step.Error is empty, want the final attempt's transport error")
	}
	if len(got.Assertions) != 0 {
		t.Errorf("Assertions = %+v, want none -- an errored attempt asserts nothing", got.Assertions)
	}
}

func TestExecuteStepsCaptureFromANonJSONBodyIsAnErroredAssertion(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `not json at all`)

	step := apiStep("ping", srv.URL, 200)
	step.Capture = map[string]models.Capture{"token": {JSON: "$.token"}}

	run := runScenario(scenario(step), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false when a capture has nothing to read")
	}
	failures := run.Failures()
	if len(failures) != 1 || failures[0].Kind != "capture" {
		t.Fatalf("Failures() = %+v, want one capture error", failures)
	}
	if !strings.Contains(failures[0].Error, "no parsed JSON output") {
		t.Errorf("error = %q, want it to say there was no body to capture from", failures[0].Error)
	}
}

// A parsed scenario can never get here -- models.Config.Validate rejects an
// unknown type -- but a Config built in code must not be the one path where a
// step artemis cannot execute still reports a pass. A skip would: it counts as
// passed.
func TestExecuteStepsUnknownStepTypeErrorsTheStep(t *testing.T) {
	initLog(t)

	run := runScenario(scenario(models.Step{Name: "query", Type: "db"}), "scenario.yaml")

	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusError {
		t.Errorf("step.Status = %q, want %q", step.Status, result.StatusError)
	}
	if !strings.Contains(step.Error, `unknown step type "db"`) {
		t.Errorf("step.Error = %q, want it to name the unknown type", step.Error)
	}
	if run.Passed() {
		t.Error("run.Passed() = true, want false -- a step artemis cannot execute must fail the run (ART-7)")
	}
}

// execute runs the real root command, the way main does.
func execute(t *testing.T, yaml string) error {
	t.Helper()
	initOnce.Do(Init)

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return executeFile(t, path)
}

func executeFile(t *testing.T, path string) error {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	dir := t.TempDir()
	RootCmd.SetArgs([]string{"run", path, "-l", filepath.Join(dir, "app.log"), "-e", filepath.Join(dir, ".env")})
	RootCmd.SetOut(os.Stderr)
	return RootCmd.Execute()
}

func scenarioYAML(url string, wantStatus int, wantBody string) string {
	return fmt.Sprintf(`name: "exit code"
type: functional
variables: []
steps:
  - name: "ping"
    type: api
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: %d
      body:
        - path: "$.status"
          value: "%s"
`, url, wantStatus, wantBody)
}

func TestExecutePassingScenarioReturnsNil(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	if err := execute(t, scenarioYAML(srv.URL, 200, "ok")); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
}

func TestExecuteFailingAssertionReturnsError(t *testing.T) {
	srv := okServer(t, 200, `{"status":"pending"}`)

	err := execute(t, scenarioYAML(srv.URL, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error so the process exits 1")
	}
	if !strings.Contains(err.Error(), "assertions failed") {
		t.Errorf("error = %q, want it to mention the failed assertions", err)
	}
}

func TestExecuteUnparseableYAMLReturnsError(t *testing.T) {
	err := execute(t, "name: [unterminated\n\tsteps: nope\n")
	if err == nil {
		t.Fatal("Execute() = nil, want an error for unparseable YAML")
	}
	if !strings.Contains(err.Error(), "scenario.yaml") {
		t.Errorf("error = %q, want it to name the file", err)
	}
}

func TestExecuteMissingFileReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")

	err := executeFile(t, missing)
	if err == nil {
		t.Fatal("Execute() = nil, want an error for a file that does not exist")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
	}
}

// strictYAML is a scenario whose one step points at url, with extra lines
// spliced in wherever a test wants a typo.
func strictYAML(url, stepExtra string) string {
	return fmt.Sprintf(`name: "strict"
type: functional
variables: []
steps:
  - name: "ping"
    type: api
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: 200
%s`, url, stepExtra)
}

func TestExecuteUnknownTopLevelKeyReturnsError(t *testing.T) {
	srv, calls := countingServer(t, 200)

	err := execute(t, strictYAML(srv.URL, "")+"varaibles: []\n")
	if err == nil {
		t.Fatal("Execute() = nil, want an error for an unknown top-level key")
	}
	if !strings.Contains(err.Error(), "varaibles") {
		t.Errorf("error = %q, want it to name the unknown key", err)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none -- the file never loaded", calls())
	}
}

// The typo this issue is named after: `respones:` instead of `response:` used
// to drop every assertion in the step and leave a scenario that passed.
func TestExecuteMisspelledResponseKeyReturnsError(t *testing.T) {
	srv, calls := countingServer(t, 500)

	yaml := `name: "strict"
type: functional
variables: []
steps:
  - name: "ping"
    type: api
    request:
      url: "` + srv.URL + `"
      method: "GET"
    respones:
      status_code: 200
`
	err := execute(t, yaml)
	if err == nil {
		t.Fatal("Execute() = nil, want an error -- a dropped response block is a scenario that asserts nothing")
	}
	if !strings.Contains(err.Error(), "respones") {
		t.Errorf("error = %q, want it to name the misspelled key", err)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none", calls())
	}
}

func TestExecuteUnknownNestedKeyReturnsError(t *testing.T) {
	srv, calls := countingServer(t, 200)

	err := execute(t, strictYAML(srv.URL, "      body:\n        - path: \"$.status\"\n          vlaue: \"ok\"\n"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error for an unknown key inside a body check")
	}
	if !strings.Contains(err.Error(), "vlaue") {
		t.Errorf("error = %q, want it to name the unknown key", err)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none", calls())
	}
}

func TestExecuteUnknownRetryKeyReturnsError(t *testing.T) {
	srv, calls := countingServer(t, 200)

	err := execute(t, strictYAML(srv.URL, "    retry:\n      tims: 3\n"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error for an unknown key inside retry")
	}
	if !strings.Contains(err.Error(), "tims") {
		t.Errorf("error = %q, want it to name the unknown key", err)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none", calls())
	}
}

// The first step is perfectly good; the second is not. Nothing may be sent,
// because a scenario artemis cannot finish should not half-run.
func TestExecuteUnknownStepTypeReturnsErrorBeforeAnyRequest(t *testing.T) {
	srv, calls := countingServer(t, 200)

	yaml := fmt.Sprintf(`name: "mixed"
type: functional
variables: []
steps:
  - name: "ping"
    type: api
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: 200
  - name: "query"
    type: db
    request:
      url: ""
      method: "GET"
    response:
      status_code: 200
`, srv.URL)

	err := execute(t, yaml)
	if err == nil {
		t.Fatal("Execute() = nil, want an error for a step type artemis cannot execute")
	}
	for _, want := range []string{"step 2", "query", `"db"`, "api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none -- step 1 must not run", calls())
	}
}

func TestExecuteStepWithNoTypeReturnsError(t *testing.T) {
	srv, calls := countingServer(t, 200)

	yaml := fmt.Sprintf(`name: "untyped"
type: functional
variables: []
steps:
  - name: "ping"
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: 200
`, srv.URL)

	err := execute(t, yaml)
	if err == nil {
		t.Fatal("Execute() = nil, want an error for a step with no type")
	}
	if !strings.Contains(err.Error(), "missing type") {
		t.Errorf("error = %q, want it to say the type is missing", err)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none", calls())
	}
}

// Every load failure has to say which file it came from: the error is all the
// user gets, and ART-2 wired it straight to the exit code.
func TestExecuteLoadErrorsNameTheFile(t *testing.T) {
	srv, _ := countingServer(t, 200)

	for name, yaml := range map[string]string{
		"unknown key":       strictYAML(srv.URL, "") + "varaibles: []\n",
		"unknown step type": strings.Replace(strictYAML(srv.URL, ""), "type: api", "type: db", 1),
	} {
		err := execute(t, yaml)
		if err == nil {
			t.Fatalf("%s: Execute() = nil, want an error", name)
		}
		if !strings.Contains(err.Error(), "scenario.yaml") {
			t.Errorf("%s: error = %q, want it to name the file", name, err)
		}
	}
}

// pathRecordingServer answers every request with body and remembers the paths
// it was asked for, in order.
func pathRecordingServer(t *testing.T, body string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// A numeric capture templated into a later url used to panic on an unchecked
// string assertion: every number out of a JSON body is a float64.
func TestExecuteStepsNumericCaptureTemplatesIntoTheNextURL(t *testing.T) {
	initLog(t)
	srv, paths := pathRecordingServer(t, `{"id":42}`)

	login := apiStep("login", srv.URL+"/login", 200)
	login.Capture = map[string]models.Capture{"id": {JSON: "$.id"}}
	fetch := apiStep("fetch", srv.URL+"/users/{{id}}", 200, models.BodyCheck{Path: "$.id", Value: 42})

	run := runScenario(scenario(login, fetch), "scenario.yaml")

	if !run.Passed() {
		t.Fatalf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	if got := paths(); len(got) != 2 || got[1] != "/users/42" {
		t.Errorf("server saw paths %v, want the second to be /users/42", got)
	}
}

func TestExecuteStepsUnknownTemplateVariableFailsTheStepBeforeAnyRequest(t *testing.T) {
	initLog(t)
	srv, calls := countingServer(t, 200)

	run := runScenario(scenario(apiStep("ping", srv.URL+"/{{tokn}}", 200)), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for an unknown template variable")
	}
	step := run.Scenarios[0].Steps[0]
	if !strings.Contains(step.Error, "unknown variable") || !strings.Contains(step.Error, "tokn") {
		t.Errorf("step.Error = %q, want it to name the unknown variable", step.Error)
	}
	if !strings.Contains(step.Error, "request url") {
		t.Errorf("step.Error = %q, want it to say which part of the request failed to render", step.Error)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none -- the url never rendered", calls())
	}
}

func TestExecuteStepsUnclosedTemplateInABodyFailsTheStep(t *testing.T) {
	initLog(t)
	srv, calls := countingServer(t, 200)

	step := apiStep("ping", srv.URL, 200)
	step.Request.Body = `{"token": "{{token`

	run := runScenario(scenario(step), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for an unclosed placeholder")
	}
	if got := run.Scenarios[0].Steps[0].Error; !strings.Contains(got, "request body") || !strings.Contains(got, "unclosed") {
		t.Errorf("step.Error = %q, want it to report the unclosed placeholder in the body", got)
	}
	if calls() != 0 {
		t.Errorf("server saw %d calls, want none", calls())
	}
}

// resetTestFlags puts the test command's flags back to their defaults. RootCmd is
// a package var shared by every test in this file, and cobra keeps whatever the
// last Execute set, so a test about default behaviour has to start from the
// defaults a fresh process would have.
// resetRunFlags puts the flags of every command that runs scenarios back to
// their defaults. The commands are package-level values, so a flag one test set
// is still set for the next one -- and `--env` in particular changes whether a
// warning is printed.
func resetRunFlags(t *testing.T) {
	t.Helper()
	for _, cmd := range []*cobra.Command{runCmd, testCmd} {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			// A repeatable flag appends, and its DefValue is the printed form
			// "[]", so setting that would leave one bogus value behind rather
			// than none. Emptying it is what "no --report was given" means.
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				if err := sv.Replace(nil); err != nil {
					t.Fatalf("emptying %s --%s: %v", cmd.Name(), f.Name, err)
				}
				f.Changed = false
				return
			}
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatalf("resetting %s --%s to %q: %v", cmd.Name(), f.Name, f.DefValue, err)
			}
			f.Changed = false
		})
	}
}

// executeCapturing runs the real command on a scenario file and returns
// everything it wrote to stdout, with no flags at all: this is what a user sees
// from `artemis run scenario.yaml`.
func executeCapturing(t *testing.T, yaml string) (string, error) {
	t.Helper()
	return captureArgs(t, func(path string) []string { return []string{"run", path} }, yaml)
}

// captureArgs writes yaml to a temp file, runs the root command with the args
// build gives for that path, and returns everything printed.
func captureArgs(t *testing.T, build func(path string) []string, yaml string) (string, error) {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	RootCmd.SetArgs(build(path))
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&out)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })

	err := RootCmd.Execute()
	return out.String(), err
}

// `test` is deprecated in favour of `run`, but it still has to run: every
// README, script and CI job written against artemis so far says `test -f`.
// Cobra prints where to go next; the run itself is the same one.
func TestDeprecatedTestCommandStillRunsAndPointsAtRun(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	out, err := captureArgs(t, func(path string) []string { return []string{"test", "-f", path} },
		scenarioYAML(srv.URL, 200, "ok"))
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	for _, want := range []string{"deprecated", "artemis run", "PASS in "} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// And it still fails the process when the run fails: a CI job that has not
// moved to `run` yet must not start going green on a broken API.
func TestDeprecatedTestCommandStillFailsTheProcess(t *testing.T) {
	srv := okServer(t, 200, `{"status":"pending"}`)

	_, err := captureArgs(t, func(path string) []string { return []string{"test", "-f", path} },
		scenarioYAML(srv.URL, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error so the process exits non-zero")
	}
}

func TestExecutePrintsStepsAndSummaryWithoutAnyFlags(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	out, err := executeCapturing(t, scenarioYAML(srv.URL, 200, "ok"))
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	for _, want := range []string{
		"scenario: exit code",
		"scenario.yaml",
		"ok",
		"ping",
		"Scenarios     1  (1 passed)",
		"Steps         1  (1 passed)",
		"Assertions    2  (2 passed)",
		"PASS in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestExecutePrintsTheFailedAssertionAndAFailingSummary(t *testing.T) {
	srv := okServer(t, 200, `{"status":"pending"}`)

	out, err := executeCapturing(t, scenarioYAML(srv.URL, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}

	for _, want := range []string{
		"FAIL ",
		"ping",
		"$.status equals ok, got pending",
		"Steps         1  (1 failed)",
		"Assertions    2  (1 passed, 1 failed)",
		"FAIL in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// A step whose request could not be made has no assertions to show, so its own
// error has to be what is printed.
func TestExecutePrintsAnErroredStepsError(t *testing.T) {
	srv := okServer(t, 200, `{}`)
	url := srv.URL
	srv.Close() // nothing is listening there any more

	out, err := executeCapturing(t, scenarioYAML(url, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}

	if !strings.Contains(out, "ERROR") {
		t.Errorf("output does not mark the step ERROR:\n%s", out)
	}
	if !strings.Contains(out, "connect") && !strings.Contains(out, "refused") {
		t.Errorf("output does not print the transport error:\n%s", out)
	}
	if !strings.Contains(out, "Steps         1  (1 errored)") {
		t.Errorf("output is missing the errored step tally:\n%s", out)
	}
}

// A captured value used to be printed to stdout by shared.ExtractValue, so a run
// that captured a token put it on the terminal between two step lines.
func TestExecuteDoesNotPrintCapturedValuesToStdout(t *testing.T) {
	srv := okServer(t, 200, `{"token":"s3cret"}`)

	yaml := fmt.Sprintf(`name: "capture"
type: functional
variables: []
steps:
  - name: "login"
    type: api
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: 200
    capture:
      token: "$.token"
`, srv.URL)

	out, err := executeCapturing(t, yaml)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil: %s", err, out)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("output contains the captured value:\n%s", out)
	}
}

// The response body of an unexpected status used to be printed to stdout by
// api.ParseResponse, unterminated, in the middle of the step list.
func TestExecuteDoesNotPrintTheResponseBodyToStdout(t *testing.T) {
	srv := okServer(t, 500, `{"secret":"do-not-print-me"}`)

	out, err := executeCapturing(t, scenarioYAML(srv.URL, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if strings.Contains(out, "do-not-print-me") {
		t.Errorf("output contains the raw response body:\n%s", out)
	}
}

// --log is opt-in: without it nothing is written anywhere, and in particular no
// app.log appears in the working directory.
func TestExecuteWritesNoLogFileWithoutTheLogFlag(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	before := dirEntries(t, ".")
	if _, err := executeCapturing(t, scenarioYAML(srv.URL, 200, "ok")); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	for name := range dirEntries(t, ".") {
		if !before[name] {
			t.Errorf("the run created %q; --log is meant to be opt-in", name)
		}
	}
}

func TestExecuteWithTheLogFlagWritesTheLogFile(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)
	initOnce.Do(Init)
	resetRunFlags(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.yaml")
	if err := os.WriteFile(path, []byte(scenarioYAML(srv.URL, 200, "ok")), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "run.log")

	var out bytes.Buffer
	RootCmd.SetArgs([]string{"test", "-f", path, "-l", logPath})
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&out)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the log file: %v", err)
	}
	if !strings.Contains(string(logged), `"msg"`) {
		t.Errorf("log file does not contain JSON records:\n%s", logged)
	}
	if !strings.Contains(out.String(), "PASS in ") {
		t.Errorf("--log must not take the summary away from the terminal:\n%s", out.String())
	}
}

// A log path that cannot be opened is the command's error to report -- the logger
// used to call os.Exit(1) from inside the library.
func TestExecuteUnopenableLogFileReturnsError(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)
	initOnce.Do(Init)
	resetRunFlags(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.yaml")
	if err := os.WriteFile(path, []byte(scenarioYAML(srv.URL, 200, "ok")), 0o600); err != nil {
		t.Fatal(err)
	}

	RootCmd.SetArgs([]string{"test", "-f", path, "-l", filepath.Join(dir, "no-such-dir", "run.log")})
	RootCmd.SetOut(os.Stderr)
	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() = nil, want an error for a log file that cannot be opened")
	}
	if !strings.Contains(err.Error(), "run.log") {
		t.Errorf("error = %q, want it to name the log file", err)
	}
}

// dirEntries is the set of names in dir, so a test can tell what a run created.
func dirEntries(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	return names
}

// The runner dispatches on the step's type through the registry it was given and
// hands the executor the scenario's scope. Nothing here mentions HTTP: that is
// the point of the seam.
func TestExecuteStepsDispatchesThroughTheRegistry(t *testing.T) {
	initLog(t)

	var saw struct {
		step  models.Step
		vars  map[string]any
		calls int
	}
	reg := executor.NewRegistry()
	reg.Register("probe", executor.Func(func(step models.Step, scope executor.Scope) (*result.StepResult, error) {
		saw.step, saw.vars, saw.calls = step, scope.Vars(), saw.calls+1
		scope.Set("probed", "yes")
		res := &result.StepResult{}
		res.Assert(result.Assertion{Kind: "probe", Operator: "equals"}.Pass())
		return res, nil
	}))

	config := models.Config{
		Name:      "probing",
		Variables: []models.Variable{{Name: "base", Value: "http://example.test"}},
		Steps:     []models.Step{{Name: "probe it", Type: "probe"}},
	}
	run := executeSteps(reg, config, "scenario.yaml", report.Discard())

	if saw.calls != 1 {
		t.Fatalf("the executor was called %d times, want 1", saw.calls)
	}
	if saw.step.Name != "probe it" {
		t.Errorf("executor saw step %q, want %q", saw.step.Name, "probe it")
	}
	if saw.vars["base"] != "http://example.test" {
		t.Errorf("scope[base] = %#v, want the declared variable", saw.vars["base"])
	}
	if !run.Passed() {
		t.Errorf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	got := run.Scenarios[0].Steps[0]
	if len(got.Assertions) != 1 || got.Assertions[0].Kind != "probe" {
		t.Errorf("Assertions = %+v, want the executor's one", got.Assertions)
	}
	// The runner stamps the name and duration the executor left off.
	if got.Name != "probe it" {
		t.Errorf("step.Name = %q, want %q", got.Name, "probe it")
	}
}

// An executor that returns neither a result nor an error is broken. The step it
// was asked to run must not pass because of it.
func TestExecuteStepsAnExecutorThatReturnsNothingErrorsTheStep(t *testing.T) {
	initLog(t)

	reg := executor.NewRegistry()
	reg.Register("broken", executor.Func(func(models.Step, executor.Scope) (*result.StepResult, error) {
		return nil, nil
	}))

	run := executeSteps(reg, models.Config{Steps: []models.Step{{Name: "x", Type: "broken"}}}, "scenario.yaml", report.Discard())

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false")
	}
	if got := run.Scenarios[0].Steps[0].Error; !strings.Contains(got, `"broken"`) {
		t.Errorf("step.Error = %q, want it to name the step type", got)
	}
}

// Retrying is the runner's, not the executor's: the executor is asked again, it
// does not loop. Captures written by an attempt that was then retried are
// overwritten by the attempt the loop stops on.
func TestExecuteStepsRetriesByCallingTheExecutorAgain(t *testing.T) {
	initLog(t)
	slept := recordSleeps(t)

	calls := 0
	reg := executor.NewRegistry()
	reg.Register("flaky", executor.Func(func(_ models.Step, scope executor.Scope) (*result.StepResult, error) {
		calls++
		scope.Set("attempt", calls)
		res := &result.StepResult{}
		a := result.Assertion{Kind: "flaky", Operator: "equals"}
		if calls < 3 {
			res.Assert(a.Fail())
		} else {
			res.Assert(a.Pass())
		}
		return res, nil
	}))

	config := models.Config{Steps: []models.Step{{
		Name:  "settle",
		Type:  "flaky",
		Retry: models.Retry{Times: 4, Delay: "10ms"},
	}}}
	run := executeSteps(reg, config, "scenario.yaml", report.Discard())

	if calls != 3 {
		t.Errorf("the executor was called %d times, want 3 -- it should stop as soon as an attempt passes", calls)
	}
	got := run.Scenarios[0].Steps[0]
	if got.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", got.Attempts)
	}
	if len(got.Assertions) != 1 || !got.Assertions[0].Passed() {
		t.Errorf("Assertions = %+v, want only the passing attempt's one", got.Assertions)
	}
	if want := []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}; len(*slept) != len(want) {
		t.Errorf("slept %v, want %v -- between attempts only", *slept, want)
	}
}

// A timeout that will not parse is the scenario's mistake, and the step fails
// before the executor is reached at all.
func TestExecuteStepsBadTimeoutFailsTheStepBeforeAnyAttempt(t *testing.T) {
	initLog(t)

	calls := 0
	reg := executor.NewRegistry()
	reg.Register("probe", executor.Func(func(models.Step, executor.Scope) (*result.StepResult, error) {
		calls++
		return &result.StepResult{}, nil
	}))

	config := models.Config{Steps: []models.Step{{
		Name:    "probe it",
		Type:    "probe",
		Timeout: "soon",
		Retry:   models.Retry{Times: 3},
	}}}
	run := executeSteps(reg, config, "scenario.yaml", report.Discard())

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for an unparseable timeout")
	}
	got := run.Scenarios[0].Steps[0]
	if got.Status != result.StatusError {
		t.Errorf("step.Status = %q, want %q", got.Status, result.StatusError)
	}
	if !strings.Contains(got.Error, `timeout "soon"`) {
		t.Errorf("step.Error = %q, want it to name the bad timeout", got.Error)
	}
	if calls != 0 {
		t.Errorf("the executor was called %d times, want 0", calls)
	}
}

// timeout: travels from the YAML all the way to the attempt. The server never
// answers, so the only thing that can end the step is the deadline the scenario
// asked for -- and a run that hangs instead is the bug this pins.
func TestExecuteTimeoutFromTheYAMLBoundsTheRequest(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	yaml := fmt.Sprintf(`name: "hang"
type: functional
variables: []
steps:
  - name: "ping"
    type: api
    timeout: "100ms"
    request:
      url: "%s"
      method: "GET"
    response:
      status_code: 200
`, srv.URL)

	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := executeCapturing(t, yaml)
		done <- outcome{out, err}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("execute() = nil, want the step to have failed on its timeout\n%s", got.out)
		}
		if !strings.Contains(got.out, "no response within 100ms") {
			t.Errorf("output does not name the timeout that was hit:\n%s", got.out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not finish: the step's timeout: was not applied")
	}
}

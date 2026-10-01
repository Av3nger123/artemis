package cli

import (
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

	"artemis/pkg/result"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

var initOnce sync.Once

// initLog points the package logger at a throwaway file; executeSteps logs
// unconditionally and would otherwise dereference a nil logger.
func initLog(t *testing.T) {
	t.Helper()
	file := logger.InitLog(filepath.Join(t.TempDir(), "test.log"))
	t.Cleanup(func() { _ = file.Close() })
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

	run := executeSteps(scenario(apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})), "scenario.yaml")

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

	run := executeSteps(scenario(apiStep("ping", srv.URL, 200, models.BodyCheck{Path: "$.status", Value: "ok"})), "scenario.yaml")

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

	run := executeSteps(scenario(apiStep("ping", srv.URL, 200)), "scenario.yaml")

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

	run := executeSteps(scenario(apiStep("ping", url, 200)), "scenario.yaml")

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

	run := executeSteps(scenario(step), "scenario.yaml")

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

	run := executeSteps(scenario(step), "scenario.yaml")

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

	run := executeSteps(scenario(step), "scenario.yaml")

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

	executeSteps(scenario(step), "scenario.yaml")

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

	run := executeSteps(scenario(step), "scenario.yaml")

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

	run := executeSteps(scenario(step), "scenario.yaml")

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
	step.Scripts = []models.Script{{Key: "token", Path: "$.token"}}

	run := executeSteps(scenario(step), "scenario.yaml")

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false when a capture has nothing to read")
	}
	failures := run.Failures()
	if len(failures) != 1 || failures[0].Kind != "capture" {
		t.Fatalf("Failures() = %+v, want one capture error", failures)
	}
	if !strings.Contains(failures[0].Error, "no parsed response body") {
		t.Errorf("error = %q, want it to say there was no body to capture from", failures[0].Error)
	}
}

func TestExecuteStepsUnsupportedStepTypeIsSkippedNotDropped(t *testing.T) {
	initLog(t)

	run := executeSteps(scenario(models.Step{Name: "query", Type: "db"}), "scenario.yaml")

	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusSkip {
		t.Errorf("step.Status = %q, want %q", step.Status, result.StatusSkip)
	}
	if !strings.Contains(step.Error, "unsupported step type") {
		t.Errorf("step.Error = %q, want it to name the unsupported type", step.Error)
	}
	if !run.Passed() {
		t.Error("run.Passed() = false, want true -- a skip must not fail the run yet (ART-7)")
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

	dir := t.TempDir()
	RootCmd.SetArgs([]string{"test", "-f", path, "-l", filepath.Join(dir, "app.log"), "-e", filepath.Join(dir, ".env")})
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

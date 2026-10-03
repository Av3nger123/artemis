package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// runArt runs one .art source through runFiles and returns the run and
// everything that went to the diagnostics writer.
func runArt(t *testing.T, src string) (*result.RunResult, string) {
	t.Helper()
	var diags bytes.Buffer
	path := writeArt(t, "scenario.art", src)
	run := runFiles(executor.Default(), []string{path}, report.Discard(), &diags)
	return run, diags.String()
}

// A scenario the DSL can run, end to end through the registry's real api
// executor: the capture out of the first step reaches the second step's URL,
// which is the one thing a unit test of the scope cannot fake.
func TestArtRunCapturesReachTheNextStep(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, `{"token": "t0ken"}`)
		case "/items/t0ken":
			fmt.Fprint(w, `{"ok": true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	run, _ := runArt(t, fmt.Sprintf(`scenario "two steps" {
  var base = %q

  step "sign in" {
    post "${base}/login"
    expect status == 200
    capture token = body.token
  }

  step "use it" {
    get "${base}/items/${token}"
    expect status == 200
    expect body.ok == true
  }
}
`, srv.URL))

	if !run.Passed() {
		t.Fatalf("run failed: %+v", run.Scenarios)
	}
	if len(asked) != 2 || asked[1] != "/items/t0ken" {
		t.Errorf("paths asked for = %v, want the capture in the second URL", asked)
	}
}

// A file that does not compile is one errored scenario with no steps -- the
// shape of a YAML file that would not load -- and the full diagnostics are
// written out, caret and hint included.
func TestArtRunCompileErrorIsAnErroredScenario(t *testing.T) {
	run, diags := runArt(t, `scenario "typo" {
  step "ping" {
    get "/ping"
    expect statu == 200
  }
}
`)

	if run.Passed() {
		t.Fatal("run passed, want a failure: the file does not compile")
	}
	if len(run.Scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(run.Scenarios))
	}
	sc := run.Scenarios[0]
	if len(sc.Steps) != 0 {
		t.Errorf("got %d steps, want none: nothing ran", len(sc.Steps))
	}
	if sc.Status != result.StatusError {
		t.Errorf("scenario status = %v, want %v", sc.Status, result.StatusError)
	}
	// The one-line reason, not the caret gutter: report.Console.field trims
	// each line of a value it prints and would destroy the alignment.
	if !strings.Contains(sc.Error, `unknown field "statu"`) {
		t.Errorf("scenario error = %q, want the first diagnostic's message", sc.Error)
	}
	if strings.Contains(sc.Error, "\n") {
		t.Errorf("scenario error = %q, want one line", sc.Error)
	}
	for _, want := range []string{`unknown field "statu"`, "^^^^^", `did you mean "status"?`} {
		if !strings.Contains(diags, want) {
			t.Errorf("diagnostics do not contain %q:\n%s", want, diags)
		}
	}
}

// Several errors in one file: the reason names the first and counts the rest,
// and every one of them is rendered.
func TestArtRunCompileErrorNamesTheFirstAndCountsTheRest(t *testing.T) {
	run, diags := runArt(t, `scenario "typos" {
  step "ping" {
    get "/ping"
    timeot = "5s"
    expect statu == 200
  }
}
`)

	sc := run.Scenarios[0]
	if !strings.Contains(sc.Error, `unknown field "timeot"`) {
		t.Errorf("scenario error = %q, want the *first* diagnostic", sc.Error)
	}
	if !strings.Contains(sc.Error, "(and 1 more error)") {
		t.Errorf("scenario error = %q, want it to count the rest", sc.Error)
	}
	if !strings.Contains(diags, `unknown field "statu"`) {
		t.Errorf("diagnostics do not hold the second error:\n%s", diags)
	}
}

// A broken file does not stop the files after it, whichever format either is
// in: a folder holding a .art typo and a .yaml scenario still runs the YAML.
func TestArtRunBrokenFileDoesNotStopTheRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()

	dir := writeTree(t, t.TempDir(), map[string]string{
		"01_broken.art": "scenario \"broken\" {\n  step \"ping\" {\n    get \"/x\"\n    expect statu == 200\n  }\n}\n",
		"02_fine.art": fmt.Sprintf(`scenario "fine" {
  step "ping" {
    get %q
    expect status == 200
  }
}
`, srv.URL),
	})
	files, err := discover(dir)
	if err != nil {
		t.Fatal(err)
	}

	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)
	if len(run.Scenarios) != 2 {
		t.Fatalf("got %d scenarios, want 2", len(run.Scenarios))
	}
	if run.Scenarios[0].Status != result.StatusError {
		t.Errorf("first scenario = %v, want errored", run.Scenarios[0].Status)
	}
	if !run.Scenarios[1].Passed() {
		t.Errorf("second scenario = %v, want it to have run anyway", run.Scenarios[1].Status)
	}
}

// A var that will not evaluate errors the scenario with no steps: a scenario
// whose variables do not resolve has nothing worth running.
func TestArtRunUnbindableVarErrorsTheScenario(t *testing.T) {
	run, _ := runArt(t, `scenario "bad var" {
  var n = match("no digits here", /([0-9]+)/)

  step "ping" {
    get "/ping"
    expect status == 200
  }
}
`)

	if len(run.Scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(run.Scenarios))
	}
	sc := run.Scenarios[0]
	if len(sc.Steps) != 0 {
		t.Errorf("got %d steps, want none: the scenario never started", len(sc.Steps))
	}
	if !strings.Contains(sc.Error, "var n") {
		t.Errorf("scenario error = %q, want it to name the var", sc.Error)
	}
}

// A capture that cannot be read is one errored assertion naming it, the
// captures after it are still read, and nothing is written for the one that
// failed -- so a later step fails on an unknown name rather than on a value
// that is quietly wrong.
func TestArtRunFailedCaptureIsOneErroredAssertion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token": "abc"}`)
	}))
	defer srv.Close()

	run, _ := runArt(t, fmt.Sprintf(`scenario "captures" {
  step "login" {
    get %q
    expect status == 200
    capture missing = body.nope.deeper
    capture token = body.token
  }
}
`, srv.URL))

	step := run.Scenarios[0].Steps[0]
	if got := len(step.Assertions); got != 2 {
		t.Fatalf("got %d assertions, want 2: the status and the one capture that failed\n%+v", got, step.Assertions)
	}
	if step.Assertions[1].Status != result.StatusError {
		t.Errorf("second assertion = %v, want errored", step.Assertions[1].Status)
	}
	if step.Assertions[1].Path != "missing" {
		t.Errorf("errored assertion names %q, want the capture's name", step.Assertions[1].Path)
	}
	// The capture after the failed one was still read: a run that stopped at
	// the first bad capture would take two runs to find two typos.
	if step.Status != result.StatusError {
		t.Errorf("step = %v, want errored", step.Status)
	}
}

// Retry: a failed capture counts against the attempt, exactly as it does in
// the YAML path, where the capture happens inside the executor and lands in
// the same assertion list.
func TestArtRunRetriesOnAFailedCapture(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls < 3 {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprint(w, `{"token": "abc"}`)
	}))
	defer srv.Close()

	orig := sleep
	var waited []time.Duration
	sleep = func(d time.Duration) { waited = append(waited, d) }
	defer func() { sleep = orig }()

	run, _ := runArt(t, fmt.Sprintf(`scenario "wait for the token" {
  step "login" {
    get %q
    retry { times = 4, delay = "10ms" }
    expect status == 200
    capture token = body.token
  }
}
`, srv.URL))

	step := run.Scenarios[0].Steps[0]
	if !step.Passed() {
		t.Fatalf("step = %v, want a pass on the third attempt\n%+v", step.Status, step.Assertions)
	}
	if step.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", step.Attempts)
	}
	// One assertion, not three: a later attempt replaces an earlier one whole,
	// so a successful capture leaves nothing behind from the two that failed.
	if got := len(step.Assertions); got != 1 {
		t.Errorf("got %d assertions, want 1 (the status)\n%+v", got, step.Assertions)
	}
	if len(waited) != 2 {
		t.Errorf("waited %v, want one delay between each of the three attempts", waited)
	}
}

// A retry delay that came out of an expression rather than a literal is caught
// before the first attempt, so a bad policy never costs a request.
func TestArtRunBadPolicyFromAnExpressionStopsTheStep(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	run, _ := runArt(t, fmt.Sprintf(`scenario "bad delay" {
  var d = "soon"

  step "ping" {
    get %q
    retry { times = 2, delay = d }
    expect status == 200
  }
}
`, srv.URL))

	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusError {
		t.Errorf("step = %v, want errored", step.Status)
	}
	if calls != 0 {
		t.Errorf("the server was called %d times, want 0: the policy is resolved first", calls)
	}
	if !strings.Contains(step.Error, "soon") {
		t.Errorf("step error = %q, want it to quote the delay", step.Error)
	}
}

// A browser scenario whose `config browser` will not resolve is a failed
// scenario with no steps, and -- the part that matters here -- no browser.
//
// Every test in this file runs through runFiles, which now wraps each scenario
// in session.WithScenario. That is lazy, so nothing is downloaded or launched
// unless a step actually asks for a page; a cli test that asked would download
// 683 MB in CI. So the browser-shaped tests here are the ones that fail
// *before* the first act, and the whole executor over a real page is
// pkg/steps/browserstep's browser-tagged suite.
func TestArtRunABrowserConfigThatWillNotResolveFailsTheScenario(t *testing.T) {
	run, _ := runArt(t, `scenario "the app" {
  var size = 1280
  config browser { viewport = size, headless = "yes" }

  step "open it" {
    browser {
      goto "http://127.0.0.1:1/settings"
    }
    expect page.url contains "/settings"
  }
}
`)

	if run.Passed() {
		t.Fatal("run passed, want a failure: the browser config does not resolve")
	}
	sc := run.Scenarios[0]
	if sc.Status != result.StatusError {
		t.Errorf("scenario = %v, want errored", sc.Status)
	}
	if len(sc.Steps) != 0 {
		t.Errorf("the scenario ran %d steps, want none: there was nowhere to run them", len(sc.Steps))
	}
	if !strings.Contains(sc.Error, "headless") {
		t.Errorf("scenario error = %q, want it to name the setting", sc.Error)
	}
}

// An act whose argument will not resolve fails the step before anything is
// driven, so a selector that came out of a bad expression costs no browser
// launch to discover -- the same rule as a bad `timeout` on an api step.
func TestArtRunAnActThatWillNotResolveFailsBeforeTheBrowserOpens(t *testing.T) {
	run, _ := runArt(t, `scenario "the app" {
  var sel = ".x"

  step "open it" {
    browser {
      goto "http://127.0.0.1:1/"
      click "text=${text(sel)}"
    }
    expect page.url contains "/"
  }
}
`)

	if run.Passed() {
		t.Fatal("run passed, want a failure")
	}
	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusError {
		t.Errorf("step = %v, want errored", step.Status)
	}
	if !strings.Contains(step.Error, "click") {
		t.Errorf("step error = %q, want it to name the act", step.Error)
	}
	if step.Screenshot != "" {
		t.Errorf("step screenshot = %q, want none: there was never a page", step.Screenshot)
	}
}

// Several scenarios in one file all run, in source order, as several scenarios
// of one run. A YAML file holds one; a .art file may hold any number.
func TestArtRunEveryScenarioInTheFile(t *testing.T) {
	run, _ := runArt(t, `scenario "first" {
  step "nothing" {
    run "true"
    expect exit_code == 0
  }
}

scenario "second" {
  step "nothing" {
    run "true"
    expect exit_code == 0
  }
}
`)

	if len(run.Scenarios) != 2 {
		t.Fatalf("got %d scenarios, want 2", len(run.Scenarios))
	}
	if run.Scenarios[0].Name != "first" || run.Scenarios[1].Name != "second" {
		t.Errorf("scenario names = %q, %q; want first, second in source order",
			run.Scenarios[0].Name, run.Scenarios[1].Name)
	}
	if !run.Passed() {
		t.Errorf("run failed: %+v", run.Scenarios)
	}
}

// compileArt reports a file that is not there as a plain error: a file with no
// contents has nothing to be diagnosed about.
func TestCompileArtMissingFile(t *testing.T) {
	_, err := compileArt(filepath.Join(t.TempDir(), "nope.art"), io.Discard)
	if err == nil {
		t.Fatal("compileArt() error = nil, want one for a file that is not there")
	}
}

// `artemis test -f x.art` is the invocation the design names, and it is
// reportRun -- so it runs a .art file with the same reporting and the same exit
// code as `artemis run`.
func TestTestCommandRunsAnArtFile(t *testing.T) {
	initOnce.Do(Init)
	resetRunFlags(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()

	path := writeArt(t, "ok.art", fmt.Sprintf(`scenario "ok" {
  step "ping" {
    get %q
    expect status == 200
    expect body.ok == true
  }
}
`, srv.URL))

	var out strings.Builder
	RootCmd.SetArgs([]string{"test", "-f", path})
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&out)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS in") {
		t.Errorf("output does not report a pass:\n%s", out.String())
	}
}

// runArtStep is handed a context, and it is the one the run was given: a
// cancelled run stops making requests rather than waiting them out.
func TestArtRunHonoursTheContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	path := writeArt(t, "slow.art", fmt.Sprintf(`scenario "slow" {
  step "ping" {
    get %q
    expect status == 200
  }
}
`, srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	run := result.NewRun()
	runArtFile(ctx, &runtimeEnv{reg: executor.Default()}, path, run, report.Discard(), io.Discard)
	run.Finish()

	if run.Passed() {
		t.Fatal("run passed, want a failure: the context was already cancelled")
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// writeTree writes a file per entry under dir, creating the parents, and
// returns dir. The keys are slash-separated paths relative to dir.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// rel turns discovered paths back into slash-separated paths relative to root,
// so an assertion reads as the tree the test wrote.
func rel(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("Rel(%q, %q) = %v", root, p, err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

// Discovery is recursive and in path order: a suite that runs in a different
// order on another machine is a suite whose transcript cannot be compared with
// yesterday's.
func TestDiscoverWalksAFolderInPathOrder(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{
		"02_second.art":        "",
		"01_first.art":         "",
		"nested/04_deep.art":   "",
		"nested/03_deep.art":   "",
		"alpha/05_alpha.art":   "",
		"notes.md":             "",
		"collection.json":      "",
		"legacy.yaml":          "",
		".hidden/06_skip.art":  "",
		"nested/.git/07_x.art": "",
	})

	got, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	want := []string{
		"01_first.art",
		"02_second.art",
		"alpha/05_alpha.art",
		"nested/03_deep.art",
		"nested/04_deep.art",
	}
	if diff := rel(t, root, got); !reflect.DeepEqual(diff, want) {
		t.Errorf("discover() = %v, want %v", diff, want)
	}
}

// A file named outright is run whatever it is called: the user said which file
// they meant, and a scenario kept as scenario.txt is still that one scenario.
func TestDiscoverASingleFileIgnoresItsExtension(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{"scenario.txt": ""})
	path := filepath.Join(root, "scenario.txt")

	got, err := discover(path)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, []string{path}) {
		t.Errorf("discover() = %v, want %v", got, []string{path})
	}
}

// A suite that quietly shrank to nothing must not report a passing run.
func TestDiscoverEmptyFolderIsAnError(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": ""})

	_, err := discover(root)
	if err == nil {
		t.Fatal("discover() = nil, want an error for a folder with no scenarios")
	}
	for _, want := range []string{root, ".art"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestDiscoverMissingPathIsAnError(t *testing.T) {
	if _, err := discover(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("discover() = nil, want an error for a path that does not exist")
	}
}

// runFiles aggregates: every file is a scenario of one run, with one verdict.
func TestRunFilesAggregatesEveryFileIntoOneRun(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)
	root := writeTree(t, t.TempDir(), map[string]string{
		"a.art": namedScenarioArt("first", srv.URL, "ok"),
		"b.art": namedScenarioArt("second", srv.URL, "ok"),
	})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)

	if !run.Passed() {
		t.Fatalf("run.Passed() = false, want true: %s", runFailedError(run))
	}
	if len(run.Scenarios) != 2 {
		t.Fatalf("run has %d scenarios, want 2", len(run.Scenarios))
	}
	if run.Scenarios[0].Name != "first" || run.Scenarios[1].Name != "second" {
		t.Errorf("scenario names = %q, %q, want first, second", run.Scenarios[0].Name, run.Scenarios[1].Name)
	}
	if got := run.Counts(); got.Scenarios.Total != 2 || got.Steps.Total != 2 {
		t.Errorf("counts = %+v, want 2 scenarios and 2 steps", got)
	}
}

// One unloadable file must not hide the rest of the suite: it is an errored
// scenario naming the file, and the files after it still run.
func TestRunFilesKeepsGoingPastAFileThatWillNotLoad(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)
	root := writeTree(t, t.TempDir(), map[string]string{
		"1_ok.art":     namedScenarioArt("first", srv.URL, "ok"),
		"2_broken.art": brokenArt(srv.URL),
		"3_ok.art":     namedScenarioArt("third", srv.URL, "ok"),
	})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for a file that would not load")
	}
	if len(run.Scenarios) != 3 {
		t.Fatalf("run has %d scenarios, want 3 -- the broken file gets one too", len(run.Scenarios))
	}
	broken := run.Scenarios[1]
	if broken.Status != result.StatusError {
		t.Errorf("broken scenario status = %q, want %q", broken.Status, result.StatusError)
	}
	if !strings.Contains(broken.File, "2_broken.art") {
		t.Errorf("broken scenario file = %q, want it to name 2_broken.art", broken.File)
	}
	if !strings.Contains(broken.Error, "timeot") {
		t.Errorf("broken scenario error = %q, want it to name the misspelled field", broken.Error)
	}
	if run.Scenarios[2].Name != "third" || !run.Scenarios[2].Passed() {
		t.Errorf("scenario after the broken one = %+v, want the third file, passed", run.Scenarios[2])
	}
}

// The exit error is all that survives when only stderr is kept, so a run whose
// only fault is a file that would not load has to say which file and why --
// counting its steps would say "0 of 0 steps failed".
func TestRunFailedErrorNamesAFileThatWouldNotLoad(t *testing.T) {
	initLog(t)
	root := writeTree(t, t.TempDir(), map[string]string{"broken.art": "scenario \"x\" {\n"})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)

	got := runFailedError(run).Error()
	if !strings.Contains(got, "1 scenario could not run") {
		t.Errorf("error = %q, want it to say one scenario could not run", got)
	}
	if !strings.Contains(got, "broken.art") {
		t.Errorf("error = %q, want it to name the file", got)
	}
	if strings.Contains(got, "0 of 0 steps") {
		t.Errorf("error = %q, want it not to count steps that never ran", got)
	}
}

// Each file gets its own scope. A capture made in one scenario must not be
// resolvable in the next: files found by a folder walk are not a sequence
// anyone wrote, and a suite whose files depend on each other's captures would
// pass or fail on the order of a directory listing.
func TestRunFilesDoesNotLeakCapturesBetweenFiles(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"token":"abc"}`)

	capturing := fmt.Sprintf(`scenario "capture" {
  step "login" {
    get %q
    expect status == 200
    capture token = body.token
  }
}
`, srv.URL)
	using := fmt.Sprintf(`scenario "use" {
  step "me" {
    get "%s/${token}"
    expect status == 200
  }
}
`, srv.URL)

	root := writeTree(t, t.TempDir(), map[string]string{
		"1_capture.art": capturing,
		"2_use.art":     using,
	})
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)

	if run.Scenarios[0].Status != result.StatusPass {
		t.Fatalf("the capturing scenario = %q, want it to pass: %s", run.Scenarios[0].Status, runFailedError(run))
	}
	// In the DSL an unknown name is a compile error, so the second file never
	// runs at all: it is one errored scenario with no steps, which is the same
	// shape as any other file that would not load.
	second := run.Scenarios[1]
	if second.Status != result.StatusError {
		t.Fatalf("the second file = %q (%s), want %q: a capture from another file must not resolve",
			second.Status, second.Error, result.StatusError)
	}
	if !strings.Contains(second.Error, "token") {
		t.Errorf("scenario error = %q, want it to name the unresolved variable", second.Error)
	}
}

// brokenArt is a scenario with a misspelled step field, which the checker
// rejects: a file artemis cannot load.
func brokenArt(url string) string {
	return fmt.Sprintf(`scenario "broken" {
  step "ping" {
    get %q
    timeot = "5s"
    expect status == 200
  }
}
`, url)
}

// A path the user named outright that artemis will not run is one errored
// scenario naming the way out, not a skip: someone who types
// `artemis run login.yaml` and gets a passing run that ran nothing has been
// told the opposite of the truth.
func TestRunFilesRefusesAYAMLFileAndNamesMigrate(t *testing.T) {
	initLog(t)
	root := writeTree(t, t.TempDir(), map[string]string{"login.yaml": "name: \"x\"\n"})
	path := filepath.Join(root, "login.yaml")

	run := runFiles(executor.Default(), []string{path}, report.Discard(), io.Discard)

	if run.Passed() {
		t.Fatal("run.Passed() = true, want false for a file artemis will not run")
	}
	if len(run.Scenarios) != 1 {
		t.Fatalf("run has %d scenarios, want 1", len(run.Scenarios))
	}
	got := run.Scenarios[0].Error
	for _, want := range []string{"login.yaml", "artemis migrate", "login.art"} {
		if !strings.Contains(got, want) {
			t.Errorf("error = %q, want it to contain %q", got, want)
		}
	}
}

// Anything else named outright says what artemis does run, without pretending
// it was a YAML scenario.
func TestRunFilesRefusesANonScenarioFile(t *testing.T) {
	initLog(t)
	root := writeTree(t, t.TempDir(), map[string]string{"notes.md": "hello"})

	run := runFiles(executor.Default(), []string{filepath.Join(root, "notes.md")}, report.Discard(), io.Discard)

	got := run.Scenarios[0].Error
	if !strings.Contains(got, "not a scenario file") || !strings.Contains(got, ".art") {
		t.Errorf("error = %q, want it to say what artemis runs", got)
	}
}

// namedScenarioArt is one passing GET step, with the scenario's name set so a
// test can tell the files of a folder run apart.
func namedScenarioArt(name, url, wantBody string) string {
	return fmt.Sprintf(`scenario %q {
  step "ping" {
    get %q
    expect status == 200
    expect body.status == %q
  }
}
`, name, url, wantBody)
}

// runStreams writes src to a temp .art file, runs `artemis run <file>` with
// extra args, and returns stdout and stderr separately. The separation is the
// point: `--report json` is only useful if stdout holds the document and
// nothing else.
func runStreams(t *testing.T, src string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	path := filepath.Join(t.TempDir(), "scenario.art")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	RootCmd.SetArgs(append([]string{"run", path}, args...))
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })

	runErr := RootCmd.Execute()
	return out.String(), errOut.String(), runErr
}

// The whole point of the format: stdout is one JSON document and nothing else,
// so `artemis run suite --report json | jq` works. The console report is still
// written -- a person watching a long run needs it -- but on stderr.
func TestReportJSONPutsOneDocumentOnStdoutAndTheConsoleOnStderr(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	stdout, stderr, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json")
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc["status"] != "pass" {
		t.Errorf("status = %v, want pass", doc["status"])
	}
	if strings.Contains(stdout, "scenario: health") {
		t.Errorf("stdout holds the console report as well as the document:\n%s", stdout)
	}
	for _, want := range []string{"scenario: health", "PASS in "} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// The document holds everything that ran, down to the assertion, not only what
// failed: a reader must be able to tell "nothing failed" from "nothing ran".
func TestReportJSONCarriesTheWholeTree(t *testing.T) {
	srv := okServer(t, 200, `{"status":"pending"}`)

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error: the body assertion does not match")
	}

	var doc struct {
		Status    string `json:"status"`
		Passed    bool   `json:"passed"`
		Scenarios []struct {
			Name   string `json:"name"`
			File   string `json:"file"`
			Status string `json:"status"`
			Steps  []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Attempts   int    `json:"attempts"`
				Assertions []struct {
					Kind     string `json:"kind"`
					Path     string `json:"path"`
					Operator string `json:"operator"`
					Expected any    `json:"expected"`
					Actual   any    `json:"actual"`
					Status   string `json:"status"`
				} `json:"assertions"`
			} `json:"steps"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}

	if doc.Status != "fail" || doc.Passed {
		t.Errorf("run = %q/%v, want fail/false", doc.Status, doc.Passed)
	}
	if len(doc.Scenarios) != 1 || len(doc.Scenarios[0].Steps) != 1 {
		t.Fatalf("document = %+v, want one scenario of one step", doc)
	}
	sc := doc.Scenarios[0]
	if sc.Name != "health" || !strings.HasSuffix(sc.File, "scenario.art") {
		t.Errorf("scenario = %q (%q), want it named and its file given", sc.Name, sc.File)
	}
	step := sc.Steps[0]
	if step.Name != "ping" || step.Attempts != 1 {
		t.Errorf("step = %q, %d attempts, want ping, 1", step.Name, step.Attempts)
	}
	if len(step.Assertions) != 2 {
		t.Fatalf("got %d assertions, want the passing status check and the failing body check: %+v", len(step.Assertions), step.Assertions)
	}
	// Every assertion a .art step makes is one `expect`, named by the
	// expression's subject: the YAML reader's "status_code" and "body" kinds
	// have no counterpart (ART-40).
	if step.Assertions[0].Kind != "expect" || step.Assertions[0].Path != "status" || step.Assertions[0].Status != "pass" {
		t.Errorf("first assertion = %+v, want a passing expect on status", step.Assertions[0])
	}
	body := step.Assertions[1]
	if body.Path != "body.status" || body.Status != "fail" || body.Expected != "ok" || body.Actual != "pending" {
		t.Errorf("second assertion = %+v, want body.status expected ok, actual pending, failed", body)
	}
}

// With a path, the document goes to the file and the console stays where it was:
// a CI job that keeps an artifact should not have its log moved out from under it.
func TestReportJSONToAFileLeavesTheConsoleOnStdout(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)
	path := filepath.Join(t.TempDir(), "results.json")

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json="+path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "PASS in ") {
		t.Errorf("stdout does not hold the console report:\n%s", stdout)
	}
	if strings.Contains(stdout, "schema_version") {
		t.Errorf("stdout holds the document, which was asked for as a file:\n%s", stdout)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // the path this test gave the command
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not a JSON document: %v\n%s", path, err, raw)
	}
	if doc["status"] != "pass" {
		t.Errorf("status = %v, want pass", doc["status"])
	}
}

// A document is written whether the run passed or not -- a report only produced
// for green runs is a report nobody can use.
func TestReportJSONIsWrittenForAFailingRun(t *testing.T) {
	srv := okServer(t, 500, `{"status":"boom"}`)
	path := filepath.Join(t.TempDir(), "results.json")

	_, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json="+path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	raw, readErr := os.ReadFile(path) //nolint:gosec // the path this test gave the command
	if readErr != nil {
		t.Fatalf("reading the report: %v", readErr)
	}
	if !strings.Contains(string(raw), `"status": "fail"`) {
		t.Errorf("the report does not say the run failed:\n%s", raw)
	}
}

// `--report junit=<path>` is the CI shape: the document in a file the job
// uploads, the console still on stdout for the log. Every step of the scenario is
// a <testcase> under the <testsuite> of its scenario, which is what makes a CI
// reporter show failures per scenario.
func TestReportJUnitToAFile(t *testing.T) {
	srv := okServer(t, 500, `{"status":"boom"}`)
	path := filepath.Join(t.TempDir(), "junit.xml")

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "junit="+path)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error for a failing run\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAIL in ") {
		t.Errorf("stdout does not hold the console report:\n%s", stdout)
	}
	if strings.Contains(stdout, "<testsuites") {
		t.Errorf("stdout holds the document, which was asked for as a file:\n%s", stdout)
	}

	raw, readErr := os.ReadFile(path) //nolint:gosec // the path this test gave the command
	if readErr != nil {
		t.Fatalf("reading the report: %v", readErr)
	}
	var doc struct {
		Failures int `xml:"failures,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name    string `xml:"name,attr"`
				Failure *struct {
					Message string `xml:"message,attr"`
				} `xml:"failure"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not an XML document: %v\n%s", path, err, raw)
	}
	if doc.Failures != 1 {
		t.Errorf("failures = %d, want 1\n%s", doc.Failures, raw)
	}
	if len(doc.Suites) != 1 || doc.Suites[0].Name != "health" {
		t.Fatalf("want one suite named after the scenario, got %+v\n%s", doc.Suites, raw)
	}
	one := doc.Suites[0].Cases
	if len(one) != 1 || one[0].Failure == nil {
		t.Fatalf("want one failing case, got %+v\n%s", one, raw)
	}
	if !strings.Contains(one[0].Failure.Message, "status == 200") {
		t.Errorf("message = %q, want the assertion that failed", one[0].Failure.Message)
	}
}

// Both formats out of one run, which is what the repeatable flag is for: the JSON
// document on stdout for whatever reads it, the JUnit file for the CI job.
func TestReportJSONAndJUnitTogether(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)
	path := filepath.Join(t.TempDir(), "junit.xml")

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"),
		"--report", "json", "--report", "junit="+path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\n%s", err, stdout)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not exactly one JSON document: %v\n%s", err, stdout)
	}
	raw, readErr := os.ReadFile(path) //nolint:gosec // the path this test gave the command
	if readErr != nil {
		t.Fatalf("reading the JUnit report: %v", readErr)
	}
	if err := xml.Unmarshal(raw, new(struct{})); err != nil {
		t.Fatalf("%s is not an XML document: %v\n%s", path, err, raw)
	}
	if !strings.Contains(string(raw), `tests="1"`) {
		t.Errorf("the JUnit report does not hold the run's one step:\n%s", raw)
	}
}

// A report that cannot be written fails the command although the run passed. A
// CI job whose artifact silently vanished is worse off than one that went red.
func TestReportThatCannotBeWrittenFailsAPassingRun(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)
	path := filepath.Join(t.TempDir(), "no-such-dir", "results.json")

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json="+path)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error\n%s", stdout)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Execute() = %q, want it to name the report path", err)
	}
	if !strings.Contains(stdout, "PASS in ") {
		t.Errorf("stdout does not hold the console report of the run that did pass:\n%s", stdout)
	}
}

// An unusable --report is refused before anything runs: a mistyped format must
// not cost a suite run, and the message has to say what the formats are.
func TestReportWithAnUnknownFormatRunsNothing(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "yaml")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	for _, want := range []string{"json", "junit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() = %q, want it to name the formats artemis knows", err)
		}
	}
	if stdout != "" {
		t.Errorf("something ran before the flag was refused:\n%s", stdout)
	}
}

// End to end through the real command: a failing run names the file and the line
// of the check that failed, and the summary's verdict is still the last line
// (ART-12).
func TestRunPrintsAFailureBlockNamingTheFileAndLine(t *testing.T) {
	srv := okServer(t, 200, `{"status":"pending"}`)

	stdout, _, err := runStreams(t, `scenario "status check" {
  step "ping" {
    get "`+srv.URL+`/ping"
    expect status == 200
    expect body.status == "ok"
  }
}
`)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error:\n%s", stdout)
	}

	// The scenario is written to a temp file, so only the line and the basename
	// are ours to assert on.
	if !strings.Contains(stdout, "scenario.art:5") {
		t.Errorf("output does not point at the failing check's line:\n%s", stdout)
	}
	for _, want := range []string{"1 failure:", "step      ping", `expected  "ok"`, `actual    "pending"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output is missing %q:\n%s", want, stdout)
		}
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "FAIL in ") {
		t.Errorf("last line = %q, want the summary verdict", last)
	}
}

// A passing run gains nothing: no heading, no blocks.
func TestRunPrintsNoFailureBlocksWhenEverythingPassed(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	stdout, _, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"))
	if err != nil {
		t.Fatalf("Execute() = %v, want nil:\n%s", err, stdout)
	}
	if strings.Contains(stdout, "failure") {
		t.Errorf("a passing run mentions a failure:\n%s", stdout)
	}
}

// With --report json the console moves to stderr, and the blocks move with it:
// stdout stays exactly one document.
func TestFailureBlocksFollowTheConsoleToStderr(t *testing.T) {
	srv := okServer(t, 500, `{}`)

	stdout, stderr, err := runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json")
	if err == nil {
		t.Fatalf("Execute() = nil, want an error:\n%s", stderr)
	}
	// Two, not one: the 500 fails `expect status == 200` and the body it
	// answered with has no `status` field, so the second expect errors. A
	// wrong status no longer suppresses the expects after it, which is the one
	// behaviour the DSL deliberately changed.
	if !strings.Contains(stderr, "2 failures:") {
		t.Errorf("stderr does not carry the failure blocks:\n%s", stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	failures, ok := doc["failures"].([]any)
	if !ok || len(failures) != 2 {
		t.Fatalf("failures = %v, want two entries", doc["failures"])
	}
	if first := failures[0].(map[string]any); first["step"] == "" || first["line"] == 0.0 {
		t.Errorf("failures[0] = %v, want a step and a line", first)
	}
}

// Discovery takes .art and nothing else. A folder that still holds unconverted
// YAML runs its converted half and walks past the rest: ART-40 took the format
// off the run path, and `artemis migrate` is how those files come back.
func TestDiscoverTakesArtFilesOnly(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{
		"01_first.art":       "",
		"02_second.yaml":     "",
		"03_third.yml":       "",
		"nested/04_deep.art": "",
		"notes.md":           "",
		"collection.postman": "",
		"05_upper.ART":       "",
	})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	want := []string{"01_first.art", "05_upper.ART", "nested/04_deep.art"}
	if got := rel(t, root, files); !reflect.DeepEqual(got, want) {
		t.Errorf("discover() = %v, want %v", got, want)
	}
}

// A folder with nothing artemis can run names every extension it looked for,
// so the message says what to rename rather than only that it found nothing.
func TestDiscoverEmptyFolderNamesTheArtExtension(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{"notes.md": ""})

	_, err := discover(root)
	if err == nil {
		t.Fatal("discover() error = nil, want one for a folder with no scenarios")
	}
	if !strings.Contains(err.Error(), ".art") {
		t.Errorf("error = %q, want it to name .art", err)
	}
}

// This is the test the design calls the important one: the check before the
// run covers the whole folder, not one file at a time. If it did not, the
// first file's step would already have reached the server by the time the
// third file's absent variable was discovered -- a suite that creates real
// orders before anyone learns its environment is incomplete. The hit count
// on a real httptest handler is the only thing that can actually prove that
// never happened; a stub would prove nothing.
func TestRunStopsBeforeTheFirstStepWhenAVariableIsAbsent(t *testing.T) {
	initOnce.Do(Init)
	resetRunFlags(t)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits++
	}))
	defer srv.Close()

	root := writeTree(t, t.TempDir(), map[string]string{
		"01-first.art": `scenario "first" {
  step "touch it" {
    get "` + srv.URL + `/orders"
    expect status == 200
  }
}`,
		"03-reports.art": `scenario "reports" {
  var url = env("ARTEMIS_TEST_REPORT_URL")
  step "report" {
    get "${url}"
    expect status == 200
  }
}`,
	})

	var out, errOut bytes.Buffer
	RootCmd.SetArgs([]string{"run", root})
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("the run passed with an absent variable")
	}
	if hits != 0 {
		t.Fatalf("%d request(s) reached the server; the gate must run before any step", hits)
	}
	if !strings.Contains(errOut.String(), "ARTEMIS_TEST_REPORT_URL") {
		t.Fatalf("the block does not name the variable:\n%s", errOut.String())
	}
}

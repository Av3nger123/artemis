package cli

import (
	"fmt"
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

// Discovery is recursive, takes both extensions, and is in path order: a suite
// that runs in a different order on another machine is a suite whose transcript
// cannot be compared with yesterday's.
func TestDiscoverWalksAFolderInPathOrder(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{
		"02_second.yaml":        "",
		"01_first.yml":          "",
		"nested/04_deep.yaml":   "",
		"nested/03_deep.yaml":   "",
		"alpha/05_alpha.yaml":   "",
		"notes.md":              "",
		"collection.json":       "",
		".hidden/06_skip.yaml":  "",
		"nested/.git/07_x.yaml": "",
	})

	got, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	want := []string{
		"01_first.yml",
		"02_second.yaml",
		"alpha/05_alpha.yaml",
		"nested/03_deep.yaml",
		"nested/04_deep.yaml",
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
	for _, want := range []string{root, ".yaml", ".yml"} {
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
		"a.yaml": namedScenarioYAML("first", srv.URL, "ok"),
		"b.yaml": namedScenarioYAML("second", srv.URL, "ok"),
	})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard())

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
		"1_ok.yaml":     namedScenarioYAML("first", srv.URL, "ok"),
		"2_broken.yaml": namedScenarioYAML("broken", srv.URL, "ok") + "varaibles: []\n",
		"3_ok.yaml":     namedScenarioYAML("third", srv.URL, "ok"),
	})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard())

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
	if !strings.Contains(broken.File, "2_broken.yaml") {
		t.Errorf("broken scenario file = %q, want it to name 2_broken.yaml", broken.File)
	}
	if !strings.Contains(broken.Error, "varaibles") {
		t.Errorf("broken scenario error = %q, want it to name the unknown key", broken.Error)
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
	root := writeTree(t, t.TempDir(), map[string]string{"broken.yaml": "name: [\n"})

	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard())

	got := runFailedError(run).Error()
	if !strings.Contains(got, "1 scenario could not run") {
		t.Errorf("error = %q, want it to say one scenario could not run", got)
	}
	if !strings.Contains(got, "broken.yaml") {
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

	capturing := fmt.Sprintf(`name: "capture"
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
	using := fmt.Sprintf(`name: "use"
type: functional
variables: []
steps:
  - name: "me"
    type: api
    request:
      url: "%s/{{token}}"
      method: "GET"
    response:
      status_code: 200
`, srv.URL)

	root := writeTree(t, t.TempDir(), map[string]string{
		"1_capture.yaml": capturing,
		"2_use.yaml":     using,
	})
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard())

	if run.Scenarios[0].Status != result.StatusPass {
		t.Fatalf("the capturing scenario = %q, want it to pass: %s", run.Scenarios[0].Status, runFailedError(run))
	}
	second := run.Scenarios[1].Steps[0]
	if second.Status != result.StatusError {
		t.Fatalf("the second file's step = %q (%s), want %q: a capture from another file must not resolve",
			second.Status, second.Error, result.StatusError)
	}
	if !strings.Contains(second.Error, "token") {
		t.Errorf("step error = %q, want it to name the unresolved variable", second.Error)
	}
}

// namedScenarioYAML is one passing GET step, with the scenario's name set so a
// test can tell the files of a folder run apart.
func namedScenarioYAML(name, url, wantBody string) string {
	return fmt.Sprintf(`name: "%s"
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
      body:
        - path: "$.status"
          value: "%s"
`, name, url, wantBody)
}

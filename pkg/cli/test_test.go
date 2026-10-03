package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"artemis/pkg/shared/logger"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file is what is left of the runner's own tests after ART-40 took YAML
// off the run path. What a step does, how it retries and what it captures is
// pinned against the DSL in artrun_test.go and against whole transcripts in
// golden_test.go; what stays here is the part that belongs to the commands
// rather than to either front end -- the deprecated `test` spelling, and --log.

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

// okServer answers every request with the given status and body.
func okServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// scenarioArt is one GET step that expects a status and a body field.
func scenarioArt(url string, wantStatus int, wantBody string) string {
	return fmt.Sprintf(`scenario "exit code" {
  step "ping" {
    get %q
    expect status == %d
    expect body.status == %q
  }
}
`, url, wantStatus, wantBody)
}

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
// from `artemis run scenario.art`.
func executeCapturing(t *testing.T, src string) (string, error) {
	t.Helper()
	return captureArgs(t, func(path string) []string { return []string{"run", path} }, src)
}

// captureArgs writes src to a temp .art file, runs the root command with the
// args build gives for that path, and returns everything printed.
func captureArgs(t *testing.T, build func(path string) []string, src string) (string, error) {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	path := filepath.Join(t.TempDir(), "scenario.art")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
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
//
// ART-40 deliberately kept it. Retiring a command is a user-visible break, and
// that issue's whole point was that nothing a scenario can say changed.
func TestDeprecatedTestCommandStillRunsAndPointsAtRun(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	out, err := captureArgs(t, func(path string) []string { return []string{"test", "-f", path} },
		scenarioArt(srv.URL, 200, "ok"))
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
		scenarioArt(srv.URL, 200, "ok"))
	if err == nil {
		t.Fatal("Execute() = nil, want an error so the process exits non-zero")
	}
}

// --log is opt-in: without it nothing is written anywhere, and in particular no
// app.log appears in the working directory.
func TestExecuteWritesNoLogFileWithoutTheLogFlag(t *testing.T) {
	srv := okServer(t, 200, `{"status":"ok"}`)

	before := dirEntries(t, ".")
	if _, err := executeCapturing(t, scenarioArt(srv.URL, 200, "ok")); err != nil {
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
	path := filepath.Join(dir, "scenario.art")
	if err := os.WriteFile(path, []byte(scenarioArt(srv.URL, 200, "ok")), 0o600); err != nil {
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
	path := filepath.Join(dir, "scenario.art")
	if err := os.WriteFile(path, []byte(scenarioArt(srv.URL, 200, "ok")), 0o600); err != nil {
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

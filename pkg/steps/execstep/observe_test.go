package execstep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The roots this file binds must be exactly the ones pkg/dsl/check lets a .art
// terminal step name, for the reason httpstep's test gives.
func TestObservedRootsMatchTheChecker(t *testing.T) {
	roots, err := observe(t, models.Step{Name: "echo", Exec: models.Exec{
		Command: "sh", Args: []string{"-c", "echo hi"},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}

	got := keys(roots)
	want := append([]string{}, check.Roots(check.Terminal)...)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("observed roots = %v, want check.Roots(check.Terminal) = %v", got, want)
	}
}

func TestObserveStreamsAndExitCode(t *testing.T) {
	roots, err := observe(t, models.Step{Exec: models.Exec{
		Command: "sh", Args: []string{"-c", "printf out; printf err >&2; exit 3"},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v, want nil: a non-zero exit is an observation", err)
	}
	// float64, so `expect exit_code == 0` compares two numbers of one type.
	if got, ok := roots[RootExitCode].(float64); !ok || got != 3 {
		t.Errorf("roots[exit_code] = %#v, want float64(3)", roots[RootExitCode])
	}
	if roots[RootStdout] != "out" {
		t.Errorf("roots[stdout] = %q, want %q", roots[RootStdout], "out")
	}
	if roots[RootStderr] != "err" {
		t.Errorf("roots[stderr] = %q, want %q", roots[RootStderr], "err")
	}
}

// ART-17's semantics, unchanged behind the `run` action block: argv is passed
// as argv, so a glob and a semicolon are arguments and not shell syntax.
func TestObserveDoesNotUseAShell(t *testing.T) {
	roots, err := observe(t, models.Step{Exec: models.Exec{
		Command: "echo", Args: []string{"a; rm -rf *", "b c"},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if got, want := roots[RootStdout], "a; rm -rf * b c\n"; got != want {
		t.Errorf("roots[stdout] = %q, want %q", got, want)
	}
}

// cwd, stdin and env all reach the child, which is what the `run` block's four
// fields are for.
func TestObserveCwdStdinAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots, err := observe(t, models.Step{Exec: models.Exec{
		Command: "sh",
		Args:    []string{"-c", `cat; ls marker; printf "%s" "$ARTEMIS_TEST"`},
		Cwd:     dir,
		Stdin:   "from-stdin\n",
		Env:     map[string]string{"ARTEMIS_TEST": "set"},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	out, _ := roots[RootStdout].(string)
	for _, want := range []string{"from-stdin", "marker", "set"} {
		if !strings.Contains(out, want) {
			t.Errorf("roots[stdout] = %q, want it to contain %q", out, want)
		}
	}
}

// Each stream is capped at MaxOutput, per attempt, so a command that prints
// without stopping cannot exhaust the run that started it.
func TestObserveCapsTheOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the generator is a sh one-liner")
	}
	roots, err := observe(t, models.Step{Exec: models.Exec{
		Command: "sh",
		Args:    []string{"-c", fmt.Sprintf("i=0; while [ $i -lt %d ]; do printf '0123456789'; i=$((i+1)); done", (MaxOutput/10)+64)},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	out, _ := roots[RootStdout].(string)
	if len(out) != MaxOutput {
		t.Errorf("len(roots[stdout]) = %d, want %d", len(out), MaxOutput)
	}
}

// The step arrives rendered, so a brace in an argument is a brace. This is the
// jq-program and shell-one-liner case, and it is why Observe does not share
// render with Execute.
func TestObserveDoesNotTemplateTheStep(t *testing.T) {
	roots, err := observe(t, models.Step{Exec: models.Exec{
		Command: "echo", Args: []string{"{{nope}}"},
	}})
	if err != nil {
		t.Fatalf("Observe() error = %v -- a brace is not a placeholder here", err)
	}
	if got, want := roots[RootStdout], "{{nope}}\n"; got != want {
		t.Errorf("roots[stdout] = %q, want %q", got, want)
	}
}

// A step with nothing to run says so rather than asking the OS about "".
func TestObserveNoCommand(t *testing.T) {
	_, err := observe(t, models.Step{Exec: models.Exec{Args: []string{"-l"}}})
	if err == nil {
		t.Fatal("Observe() error = nil, want a complaint about the missing command")
	}
	if want := "no command to run"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// A binary that is not there is a step that could not run, not an assertion.
func TestObserveCommandNotFound(t *testing.T) {
	_, err := observe(t, models.Step{Exec: models.Exec{Command: "artemis-no-such-binary"}})
	if err == nil {
		t.Fatal("Observe() error = nil, want a complaint about the missing binary")
	}
}

// A step's own timeout is honoured, and the message names the scenario's number.
func TestObserveRespectsTheStepTimeout(t *testing.T) {
	_, err := observe(t, models.Step{Timeout: "20ms", Exec: models.Exec{
		Command: "sh", Args: []string{"-c", "sleep 5"},
	}})
	if err == nil {
		t.Fatal("Observe() error = nil, want a timeout")
	}
	if want := "did not finish within 20ms"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func observe(t *testing.T, step models.Step) (map[string]any, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixtures are POSIX commands")
	}
	return Executor{}.Observe(context.Background(), step, executor.NewScope())
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

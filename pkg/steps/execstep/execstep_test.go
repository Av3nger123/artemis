package execstep

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The commands these tests run are this test binary, re-executed. A test that
// shells out to `echo`, `sh` or `sleep` is a test about the machine it ran on:
// it passes on the laptop, passes on CI and fails on Windows or on an image
// without coreutils. The helper below is the one binary that is guaranteed to
// be there, so every behaviour of the step -- streams, exit codes, stdin, cwd,
// env, deadlines -- is covered without asking anything of the host.
const helperEnv = "ARTEMIS_EXEC_HELPER"

// TestHelperProcess is not a test. It is the command the other tests run: when
// the environment marks it as the helper, it does what its arguments say and
// exits, so the testing framework never prints anything of its own into the
// stream under assertion.
//
// Verbs, read in order and all optional: `stdout <text>`, `stderr <text>`,
// `pwd`, `env <NAME>`, `echo-stdin`, `spam <n>`, `sleep <duration>`,
// `exit <code>`.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("not the helper process")
	}

	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}

	code := 0
	for len(args) > 0 {
		verb, rest := args[0], args[1:]
		next := func() string {
			if len(rest) == 0 {
				fmt.Fprintf(os.Stderr, "helper: %s wants an argument\n", verb)
				os.Exit(99)
			}
			v := rest[0]
			rest = rest[1:]
			return v
		}
		switch verb {
		case "stdout":
			fmt.Fprint(os.Stdout, next())
		case "stderr":
			fmt.Fprint(os.Stderr, next())
		case "pwd":
			wd, err := os.Getwd()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(99)
			}
			fmt.Fprint(os.Stdout, wd)
		case "env":
			fmt.Fprint(os.Stdout, os.Getenv(next()))
		case "echo-stdin":
			if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(99)
			}
		case "spam":
			n, _ := strconv.Atoi(next())
			fmt.Fprint(os.Stdout, strings.Repeat("x", n))
			fmt.Fprint(os.Stdout, "TAIL")
		case "sleep":
			d, _ := time.ParseDuration(next())
			time.Sleep(d)
		case "exit":
			n, _ := strconv.Atoi(next())
			code = n
		default:
			fmt.Fprintf(os.Stderr, "helper: unknown verb %q\n", verb)
			os.Exit(99)
		}
		args = rest
	}
	os.Exit(code)
}

// helper is an Exec that runs this test binary as the helper process with the
// given verbs.
func helper(verbs ...string) models.Exec {
	return models.Exec{
		Command: os.Args[0],
		Args:    append([]string{"-test.run=TestHelperProcess", "--"}, verbs...),
		Env:     map[string]string{helperEnv: "1"},
	}
}

// step is a rendered terminal step. There is no expect block: what a scenario
// expects of a command is an `expect` expression now, evaluated by the runner
// against what Observe reports (ART-40).
func step(name string, e models.Exec) models.Step {
	return models.Step{Name: name, Type: StepType, Exec: e}
}

// runStep observes the step with a short default timeout, so a test that
// accidentally waits on a command does not wait DefaultTimeout.
func runStep(t *testing.T, s models.Step, sc executor.Scope) (map[string]any, error) {
	t.Helper()
	return Executor{Timeout: 20 * time.Second}.Observe(context.Background(), s, sc)
}

func scope() executor.Scope {
	return executor.ScopeOf(map[string]any{"word": "hello", "n": float64(3)})
}

// The registration is the whole point of the package: a scenario writing
// `type: exec` has to reach this executor through the default registry, and
// Validate has to accept the type because it is in Types().
func TestItIsRegisteredAsExec(t *testing.T) {
	e, ok := executor.Default().Lookup(StepType)
	if !ok {
		t.Fatalf("nothing is registered for %q", StepType)
	}
	if _, isExec := e.(Executor); !isExec {
		t.Errorf("%q is registered to %T, want execstep.Executor", StepType, e)
	}
	var found bool
	for _, typ := range executor.Default().Types() {
		if typ == StepType {
			found = true
		}
	}
	if !found {
		t.Errorf("Types() = %v, want it to contain %q -- Validate reads this list", executor.Default().Types(), StepType)
	}
}

// Dispatch, not just the executor: the step has to run when it is reached the
// way the runner reaches it.
func TestItRunsThroughTheRegistry(t *testing.T) {
	s := step("version", helper("stdout", "artemis 1.2.3"))
	roots, err := executor.Observe(context.Background(), executor.Default(), s, scope())
	if err != nil {
		t.Fatalf("Observe() = %v, want nil", err)
	}
	if got := roots[RootStdout]; got != "artemis 1.2.3" {
		t.Errorf("roots[stdout] = %q, want %q", got, "artemis 1.2.3")
	}
}

// A step's own value wins over an inherited one with the same name.
func TestTheStepsEnvOverridesTheInheritedOne(t *testing.T) {
	e := helper("env", "ARTEMIS_OVERRIDDEN")
	e.Env["ARTEMIS_OVERRIDDEN"] = "the step's"
	s := step("env", e)
	ex := Executor{Timeout: 20 * time.Second, Env: append(os.Environ(), "ARTEMIS_OVERRIDDEN=the parent's")}
	roots, err := ex.Observe(context.Background(), s, executor.ScopeOf(nil))
	if err != nil {
		t.Fatalf("Observe() = %v, want nil", err)
	}
	if got := roots[RootStdout]; got != "the step's" {
		t.Errorf("roots[stdout] = %q, want the step's value", got)
	}
}

func TestABadTimeoutIsAnErrorBeforeTheCommandRuns(t *testing.T) {
	s := step("bad timeout", helper("stdout", "never runs"))
	s.Timeout = "soon"
	_, err := runStep(t, s, scope())
	if err == nil {
		t.Fatal("Observe() = nil, want an error about the timeout")
	}
	if !strings.Contains(err.Error(), "soon") {
		t.Errorf("error = %v, want it to quote the timeout it could not read", err)
	}
}

func TestTheZeroExecutorHasADeadline(t *testing.T) {
	if got := (Executor{}).timeout(); got != DefaultTimeout {
		t.Errorf("timeout() = %s, want DefaultTimeout (%s)", got, DefaultTimeout)
	}
}

func TestCappedBufferCountsWhatItDropped(t *testing.T) {
	b := cappedBuffer{limit: 10}
	for _, write := range []string{"12345", "67890abc", "def"} {
		n, err := b.Write([]byte(write))
		if err != nil || n != len(write) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil -- a short write would fail the command", write, n, err, len(write))
		}
	}
	if got := b.buf.String(); got != "1234567890" {
		t.Errorf("buffer = %q, want the first 10 bytes", got)
	}
	if got, want := b.dropped, 6; got != want {
		t.Errorf("dropped = %d, want %d", got, want)
	}
}

package execstep

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/result"
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

func step(name string, e models.Exec, expect models.Expect) models.Step {
	return models.Step{Name: name, Type: StepType, Exec: e, Expect: expect}
}

// run executes the step with a short default timeout, so a test that
// accidentally waits on a command does not wait DefaultTimeout.
func run(t *testing.T, s models.Step, sc executor.Scope) (*result.StepResult, error) {
	t.Helper()
	return Executor{Timeout: 20 * time.Second}.Execute(context.Background(), s, sc)
}

func scope() executor.Scope {
	return executor.ScopeOf(map[string]any{"word": "hello", "n": float64(3)})
}

// kinds is the assertions of a result, by kind and in order, which is what the
// order of the printed lines is.
func kinds(res *result.StepResult) []string {
	out := make([]string, 0, len(res.Assertions))
	for _, a := range res.Assertions {
		out = append(out, a.Kind)
	}
	return out
}

func only(t *testing.T, res *result.StepResult, kind string) result.AssertionResult {
	t.Helper()
	for _, a := range res.Assertions {
		if a.Kind == kind {
			return a
		}
	}
	t.Fatalf("no %s assertion in %v", kind, kinds(res))
	return result.AssertionResult{}
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
	s := step("version", helper("stdout", "artemis 1.2.3"), models.Expect{
		Stdout: []models.TextCheck{{Value: "artemis 1.2.3"}},
	})
	res, err := executor.Run(context.Background(), executor.Default(), s, scope())
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions did not pass: %v", res.Assertions)
	}
}

func TestASuccessfulCommandAssertsOnItsExitCode(t *testing.T) {
	res, err := run(t, step("ok", helper(), models.Expect{}), scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if got := kinds(res); len(got) != 1 || got[0] != KindExitCode {
		t.Fatalf("assertions = %v, want exactly one exit_code", got)
	}
	if a := res.Assertions[0]; a.Status != result.StatusPass {
		t.Errorf("exit_code = %v, want a pass (%s)", a.Status, a.Describe())
	}
}

// A command that ran and failed is a failed assertion, never an error: the
// difference is what makes "the service said no" readable apart from "artemis
// could not ask".
func TestANonZeroExitIsAFailedAssertionNotAnError(t *testing.T) {
	res, err := run(t, step("fails", helper("exit", "3"), models.Expect{}), scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil -- a command that ran is not an error", err)
	}
	a := only(t, res, KindExitCode)
	if a.Status != result.StatusFail {
		t.Errorf("exit_code = %v, want a fail (%s)", a.Status, a.Describe())
	}
	if a.Actual != 3 || a.Expected != 0 {
		t.Errorf("exit_code assertion = %s, want expected 0 and actual 3", a.Describe())
	}
}

// An exit code a scenario asked for is a pass: a step whose point is that a
// command rejects its input has to be writable.
func TestAnExpectedNonZeroExitPasses(t *testing.T) {
	res, err := run(t, step("rejects", helper("exit", "2"), models.Expect{ExitCode: 2}), scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindExitCode); a.Status != result.StatusPass {
		t.Errorf("exit_code = %v, want a pass (%s)", a.Status, a.Describe())
	}
}

func TestStreamChecksBecomeOneAssertionEach(t *testing.T) {
	s := step("streams", helper("stdout", "out one\nout two\n", "stderr", "err here\n"), models.Expect{
		// (?m) because Go's $ is end-of-text, not end-of-line: a scenario
		// anchoring a line of output needs the inline flag, and this is where
		// that is written down.
		Stdout: []models.TextCheck{{Value: "out one"}, {Operator: "matches", Value: "(?m)out two$"}},
		Stderr: []models.TextCheck{{Value: "err here"}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	want := []string{KindExitCode, KindStdout, KindStdout, KindStderr}
	if got := kinds(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("assertions = %v, want %v -- the exit code first, then stdout, then stderr", got, want)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions did not all pass: %v", res.Assertions)
	}
}

// The streams are kept separate: a check on stdout must not be satisfied by
// text the command wrote to stderr.
func TestTheStreamsDoNotLeakIntoEachOther(t *testing.T) {
	s := step("separate", helper("stderr", "only on stderr"), models.Expect{
		Stdout: []models.TextCheck{{Value: "only on stderr"}},
		Stderr: []models.TextCheck{{Operator: "empty"}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusFail {
		t.Errorf("stdout check = %v, want a fail (%s)", a.Status, a.Describe())
	}
	if a := only(t, res, KindStderr); a.Status != result.StatusFail {
		t.Errorf("stderr empty = %v, want a fail -- the command wrote to it (%s)", a.Status, a.Describe())
	}
}

// Unlike the HTTP step, where a wrong status suppresses the body checks, a
// wrong exit code leaves the stream checks in play: stderr is what a failed
// command is diagnosed from.
func TestAWrongExitCodeStillChecksTheStreams(t *testing.T) {
	s := step("broken", helper("stderr", "fatal: no such table\n", "exit", "1"), models.Expect{
		Stderr: []models.TextCheck{{Value: "no such table"}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindExitCode); a.Status != result.StatusFail {
		t.Errorf("exit_code = %v, want a fail", a.Status)
	}
	if a := only(t, res, KindStderr); a.Status != result.StatusPass {
		t.Errorf("stderr check = %v, want it still made and passing (%s)", a.Status, a.Describe())
	}
}

func TestStdinReachesTheCommand(t *testing.T) {
	e := helper("echo-stdin")
	e.Stdin = "fed in on stdin"
	s := step("stdin", e, models.Expect{
		Stdout: []models.TextCheck{{Operator: "equals", Value: "fed in on stdin"}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusPass {
		t.Errorf("stdout = %v, want the stdin echoed back (%s)", a.Status, a.Describe())
	}
}

// A step with no `stdin:` still gets a stdin that is at EOF, not artemis's
// own: a command that reads stdin must not be able to hang the run.
func TestACommandWithNoStdinSeesEOF(t *testing.T) {
	s := step("no stdin", helper("echo-stdin"), models.Expect{
		Stdout: []models.TextCheck{{Operator: "empty"}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusPass {
		t.Errorf("stdout = %v, want nothing read (%s)", a.Status, a.Describe())
	}
}

func TestCwdIsWhereTheCommandRuns(t *testing.T) {
	dir := t.TempDir()
	e := helper("pwd")
	e.Cwd = dir
	s := step("cwd", e, models.Expect{
		Stdout: []models.TextCheck{{Value: filepath.Base(dir)}},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusPass {
		t.Errorf("stdout = %v, want the command to have run in %s (%s)", a.Status, dir, a.Describe())
	}
}

// `env:` is layered onto the environment artemis was given, not a replacement
// for it: a replaced environment has no PATH. Executor.Env stands in for
// os.Environ() so the test can name an inherited variable without touching the
// test process's own environment.
func TestEnvIsLayeredOntoTheInheritedEnvironment(t *testing.T) {
	e := helper("env", "ARTEMIS_ONLY_HERE", "stdout", "|", "env", "ARTEMIS_AMBIENT")
	e.Env["ARTEMIS_ONLY_HERE"] = "set by the step"
	s := step("env", e, models.Expect{
		Stdout: []models.TextCheck{{Operator: "equals", Value: "set by the step|from the parent"}},
	})

	ex := Executor{Timeout: 20 * time.Second, Env: append(os.Environ(), "ARTEMIS_AMBIENT=from the parent")}
	res, err := ex.Execute(context.Background(), s, executor.ScopeOf(nil))
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusPass {
		t.Errorf("stdout = %v, want the step's env and the inherited env both there (%s)", a.Status, a.Describe())
	}
}

// A step's own value wins over an inherited one with the same name.
func TestTheStepsEnvOverridesTheInheritedOne(t *testing.T) {
	e := helper("env", "ARTEMIS_OVERRIDDEN")
	e.Env["ARTEMIS_OVERRIDDEN"] = "the step's"
	s := step("env", e, models.Expect{
		Stdout: []models.TextCheck{{Operator: "equals", Value: "the step's"}},
	})
	ex := Executor{Timeout: 20 * time.Second, Env: append(os.Environ(), "ARTEMIS_OVERRIDDEN=the parent's")}
	res, err := ex.Execute(context.Background(), s, executor.ScopeOf(nil))
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if a := only(t, res, KindStdout); a.Status != result.StatusPass {
		t.Errorf("stdout = %v, want the step's value (%s)", a.Status, a.Describe())
	}
}

// Every part of the command is rendered against the scenario's variables, not
// just some of them.
func TestEveryFieldIsRenderedAgainstTheScope(t *testing.T) {
	dir := t.TempDir()
	e := helper("stdout", "{{word}} ", "env", "ARTEMIS_RENDERED", "stdout", " ", "echo-stdin", "stdout", " ", "pwd")
	e.Cwd = dir
	e.Stdin = "{{word}}-in"
	e.Env["ARTEMIS_RENDERED"] = "{{word}}-env"
	s := step("rendered", e, models.Expect{
		Stdout: []models.TextCheck{
			{Value: "hello hello-env hello-in "},
			{Value: filepath.Base(dir)},
		},
	})
	res, err := run(t, s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions did not all pass: %v", res.Assertions)
	}
}

func TestRenderErrorsNameWhatFailedToRender(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.Exec)
		wantIn string
	}{
		{"command", func(e *models.Exec) { e.Command = "{{" }, "rendering command"},
		{"args", func(e *models.Exec) { e.Args = append(e.Args, "{{") }, "rendering args"},
		{"cwd", func(e *models.Exec) { e.Cwd = "{{" }, "rendering cwd"},
		{"stdin", func(e *models.Exec) { e.Stdin = "{{" }, "rendering stdin"},
		{"env", func(e *models.Exec) { e.Env["BAD"] = "{{" }, "rendering env BAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := helper("stdout", "never runs")
			tc.mutate(&e)
			_, err := run(t, step("bad template", e, models.Expect{}), scope())
			if err == nil {
				t.Fatalf("Execute() = nil, want an error about %s", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to name %q", err, tc.wantIn)
			}
		})
	}
}

// A command that cannot be started at all is an error, not a failed exit-code
// assertion: there was no exit code.
func TestACommandThatCannotBeStartedIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    models.Exec
	}{
		{"not on the path", models.Exec{Command: "artemis-no-such-binary-12345"}},
		{"cwd does not exist", func() models.Exec {
			e := helper()
			e.Cwd = filepath.Join(os.TempDir(), "artemis-no-such-dir-12345")
			return e
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := run(t, step("cannot start", tc.e, models.Expect{}), scope())
			if err == nil {
				t.Fatalf("Execute() = nil with %v, want an error", res)
			}
			if !strings.Contains(err.Error(), "running ") {
				t.Errorf("error = %v, want it to say what it tried to run", err)
			}
		})
	}
}

func TestAStepWithNoCommandIsAnError(t *testing.T) {
	_, err := run(t, step("empty", models.Exec{Command: "  "}, models.Expect{}), scope())
	if err == nil {
		t.Fatal("Execute() = nil, want an error about the missing command")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("error = %v, want it to name the missing command", err)
	}
}

func TestTheStepsTimeoutBoundsTheAttempt(t *testing.T) {
	s := step("slow", helper("sleep", "30s"), models.Expect{})
	s.Timeout = "150ms"

	start := time.Now()
	_, err := run(t, s, scope())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Execute() = nil, want a deadline error")
	}
	if !strings.Contains(err.Error(), "150ms") {
		t.Errorf("error = %v, want it to name the deadline the scenario set", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("Execute() took %s, want it to give up at the deadline", elapsed)
	}
}

func TestABadTimeoutIsAnErrorBeforeTheCommandRuns(t *testing.T) {
	s := step("bad timeout", helper("stdout", "never runs"), models.Expect{})
	s.Timeout = "soon"
	_, err := run(t, s, scope())
	if err == nil {
		t.Fatal("Execute() = nil, want an error about the timeout")
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

// Captures come from stdout, through pkg/shared/capture -- no code in this
// package reads a path or a pattern.
func TestCapturesReadStdout(t *testing.T) {
	s := step("captures", helper("stdout", `{"data":{"id":42},"name":"widget"}`), models.Expect{})
	s.Capture = map[string]models.Capture{
		"id":   {JSON: "$.data.id"},
		"name": {JSON: "$.name"},
		"raw":  {Regex: `"id":([0-9]+)`},
	}
	sc := scope()
	res, err := run(t, s, sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Fatalf("assertions did not all pass: %v", res.Assertions)
	}
	for key, want := range map[string]any{"id": float64(42), "name": "widget", "raw": "42"} {
		got, ok := sc.Get(key)
		if !ok {
			t.Errorf("%q is not in the scope", key)
			continue
		}
		if got != want {
			t.Errorf("scope[%q] = %#v, want %#v", key, got, want)
		}
	}
}

// Output that is not JSON is not a failure in itself -- a regex capture reads
// it -- but a JSON path against it is an errored assertion saying so.
func TestAJSONCaptureFromNonJSONOutputIsAnErroredAssertion(t *testing.T) {
	s := step("not json", helper("stdout", "id=42 plain text"), models.Expect{})
	s.Capture = map[string]models.Capture{
		"id":   {Regex: `id=([0-9]+)`},
		"nope": {JSON: "$.data.id"},
	}
	sc := scope()
	res, err := run(t, s, sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if got, ok := sc.Get("id"); !ok || got != "42" {
		t.Errorf("scope[id] = %#v, %v; want \"42\" -- the regex capture should still work", got, ok)
	}
	var errored int
	for _, a := range res.Assertions {
		if a.Kind == "capture" && a.Status == result.StatusError {
			errored++
		}
	}
	if errored != 1 {
		t.Errorf("errored captures = %d, want 1: %v", errored, res.Assertions)
	}
}

// A command that prints without stopping must not be able to exhaust the memory
// of the run that started it.
func TestOutputIsCappedAndTheTailIsDropped(t *testing.T) {
	s := step("spam", helper("spam", strconv.Itoa(MaxOutput)), models.Expect{})
	s.Capture = map[string]models.Capture{"tail": {Regex: "(TAIL)"}}
	sc := scope()
	res, err := run(t, s, sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if _, ok := sc.Get("tail"); ok {
		t.Error("the tail past the cap was captured, want it dropped")
	}
	a := only(t, res, "capture")
	if a.Status != result.StatusError {
		t.Errorf("capture = %v, want it errored because the output was truncated (%s)", a.Status, a.Describe())
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

// A wrong exit code points at the `exit_code:` the step wrote (ART-12).
func TestExitCodeAssertionCarriesTheExitCodeLine(t *testing.T) {
	step := models.Step{Name: "build", Expect: models.Expect{ExitCode: 0, ExitCodeLine: 27}}
	if got := exitCodeAssertion(step, 1); got.Line != 27 {
		t.Errorf("exitCodeAssertion().Line = %d, want 27", got.Line)
	}
	// A step that did not write one -- which is the common case, since exit_code
	// defaults to the 0 almost every scenario wants -- leaves it at zero for
	// result.Diagnostics to resolve against the step.
	bare := models.Step{Name: "build"}
	if got := exitCodeAssertion(bare, 1); got.Line != 0 {
		t.Errorf("exitCodeAssertion().Line = %d, want 0", got.Line)
	}
}

// Package execstep is the exec step: it runs a command, checks its exit code
// and its two streams, and hands stdout to pkg/shared/capture so the step's
// `capture:` values come out of it. It is registered in the default registry
// under the step type "exec".
//
// It is the escape hatch. Anything artemis has no step type for -- a CLI, a
// migration script, a health check that is a shell one-liner -- is an exec step,
// and it is the second implementation of the ART-15 Executor interface, which is
// what makes that interface something other than HTTP wearing a hat.
//
// There is no shell. Command is executed directly with Args: no word splitting,
// no globbing, no `sh -c` unless the scenario names a shell itself. A scenario
// that wants a pipeline writes `command: "sh"`, `args: ["-c", "..."]`, which is
// longer to write and impossible to misread.
//
// Per ART-15's contract: one attempt per Execute, no retrying, no timing, and a
// returned result that carries assertions and nothing else. A command that ran
// and exited non-zero is a failed assertion, not an error -- the error return is
// for a command that could not be run at all.
package execstep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/assert"
	"artemis/pkg/shared/capture"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// StepType is the type a scenario writes to get this executor.
const StepType = "exec"

// DefaultTimeout is how long one attempt may take when the step does not say.
// There is no "no timeout": a command with no deadline is how a CI job hangs
// until someone notices.
const DefaultTimeout = 30 * time.Second

// MaxOutput is how much of each stream is kept, per attempt.
//
// A command that prints without stopping must not be able to exhaust the
// memory of the run that started it, and no assertion or capture worth making
// needs a megabyte of context. Everything past the cap is counted and dropped,
// and the drop is logged rather than left to be inferred from a regex that
// stopped matching.
const MaxOutput = 1 << 20

// Kinds the assertions this step makes are recorded under.
const (
	KindExitCode = "exit_code"
	KindStdout   = "stdout"
	KindStderr   = "stderr"
)

// Executor is the exec step.
type Executor struct {
	// Timeout is the deadline for a step that does not set one. Zero means
	// DefaultTimeout.
	Timeout time.Duration
	// Env is the environment a step's `env:` is layered onto. Nil means the
	// environment artemis itself was given.
	Env []string
}

var _ executor.Executor = Executor{}

func init() {
	executor.Register(StepType, Executor{})
}

// Execute runs the command once and reports what it did.
//
// The order the assertions come out in is the order they are printed in: the
// exit code first, then the stdout checks, then the stderr checks, then the
// captures. Unlike the HTTP step, a wrong exit code does not suppress the rest:
// the reason it does there -- a 500's body is not the body the scenario
// described -- runs the other way for a command, where stderr is exactly what a
// failure is diagnosed from and a check on it is most worth making when the
// command did not succeed.
func (e Executor) Execute(ctx context.Context, step models.Step, scope executor.Scope) (*result.StepResult, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}

	spec, err := e.render(step, scope)
	if err != nil {
		return nil, err
	}

	// The step's deadline hangs off the one the run was given, so cancelling a
	// run kills the command it is waiting on rather than waiting it out.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run, err := e.run(ctx, step, spec, timeout)
	if err != nil {
		return nil, err
	}

	res := &result.StepResult{}
	res.Assert(exitCodeAssertion(step, run.exitCode))
	for _, check := range step.Expect.Stdout {
		res.Assert(assert.Text(step.Name, KindStdout, check, run.stdout))
	}
	for _, check := range step.Expect.Stderr {
		res.Assert(assert.Text(step.Name, KindStderr, check, run.stderr))
	}
	out := []byte(run.stdout)
	for _, a := range capture.Apply(step, capture.Source{Text: out, JSON: parseJSON(step, out)}, scope.Vars()) {
		res.Assert(a)
	}
	return res, nil
}

// render resolves every placeholder in the step before anything is started. A
// failure names the part of the command it was in, because `{{host}}` missing
// from the cwd and from an argument are different mistakes to go and fix.
//
// Env values are rendered in sorted key order so a scenario with two bad ones
// reports the same one first on every run.
func (e Executor) render(step models.Step, scope executor.Scope) (models.Exec, error) {
	vars := scope.Vars()
	src := step.Exec
	out := models.Exec{}

	if strings.TrimSpace(src.Command) == "" {
		// Caught here rather than in models.Validate: Validate is handed the
		// list of step types precisely so it holds none of its own, and
		// teaching it that `exec` implies `command` would put a type's name
		// back inside it. The same rule applies to an api step with no url.
		return out, errors.New("exec step has no command to run")
	}

	var err error
	if out.Command, err = shared.TransformText(src.Command, vars); err != nil {
		return out, fmt.Errorf("rendering command: %w", err)
	}
	if out.Cwd, err = shared.TransformText(src.Cwd, vars); err != nil {
		return out, fmt.Errorf("rendering cwd: %w", err)
	}
	if out.Stdin, err = shared.TransformText(src.Stdin, vars); err != nil {
		return out, fmt.Errorf("rendering stdin: %w", err)
	}
	if len(src.Args) > 0 {
		out.Args = make([]string, len(src.Args))
		for i := range src.Args {
			if out.Args[i], err = shared.TransformText(src.Args[i], vars); err != nil {
				return out, fmt.Errorf("rendering args[%d]: %w", i, err)
			}
		}
	}
	if len(src.Env) > 0 {
		out.Env = make(map[string]string, len(src.Env))
		for _, key := range src.EnvKeys() {
			if out.Env[key], err = shared.TransformText(src.Env[key], vars); err != nil {
				return out, fmt.Errorf("rendering env %s: %w", key, err)
			}
		}
	}
	return out, nil
}

// outcome is what one run of a command produced.
type outcome struct {
	exitCode int
	stdout   string
	stderr   string
}

// run starts the command, waits for it, and returns its exit code and its two
// streams.
//
// The error return is for a command that never ran -- a binary that is not on
// the PATH, a cwd that does not exist -- and for one that outlived its deadline.
// A command that ran and exited non-zero returns no error: that is the exit code
// assertion's business.
func (e Executor) run(ctx context.Context, step models.Step, spec models.Exec, timeout time.Duration) (outcome, error) {
	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = MaxOutput, MaxOutput

	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Cwd
	cmd.Env = e.environ(spec)
	// Always a reader, even an empty one: a child that inherited artemis's own
	// stdin and read from it would hang a run with no sign of why.
	cmd.Stdin = strings.NewReader(spec.Stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger.Logger.Info("Running command", "name", step.Name, "command", spec.Command, "args", spec.Args, "cwd", spec.Cwd)

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	out := outcome{stdout: stdout.buf.String(), stderr: stderr.buf.String()}
	for _, s := range []struct {
		kind string
		b    *cappedBuffer
	}{{KindStdout, &stdout}, {KindStderr, &stderr}} {
		if s.b.dropped > 0 {
			logger.Logger.Warn("Command output was truncated", "name", step.Name, "stream", s.kind, "kept", s.b.buf.Len(), "dropped", s.b.dropped)
		}
	}

	// The deadline is checked before the error is read: a killed command's
	// error says it was signalled, which is true and useless. The scenario's
	// own number is what the reader needs.
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s did not finish within %s", spec.Command, timeout)
	}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		out.exitCode = 0
	case errors.As(runErr, &exitErr):
		out.exitCode = exitErr.ExitCode()
	default:
		return out, fmt.Errorf("running %s: %w", spec.Command, runErr)
	}

	logger.Logger.Info("Command finished", "name", step.Name, "exit_code", out.exitCode, "time", elapsed,
		"stdout", out.stdout, "stderr", out.stderr)
	return out, nil
}

// environ is the environment the command is given: the one artemis was given,
// with the step's `env:` layered on top. cmd.Env keeps the last value for a
// repeated name, so the step's value wins over an inherited one.
//
// It is never nil, because a nil cmd.Env means "inherit", and a step that sets
// nothing and a step whose values were all dropped should not differ.
func (e Executor) environ(spec models.Exec) []string {
	base := e.Env
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+len(spec.Env))
	env = append(env, base...)
	for _, key := range spec.EnvKeys() {
		env = append(env, key+"="+spec.Env[key])
	}
	return env
}

// exitCodeAssertion is the one check every exec step makes, whether or not it
// asked for any others.
func exitCodeAssertion(step models.Step, got int) result.AssertionResult {
	a := result.Assertion{
		Step:     step.Name,
		Kind:     KindExitCode,
		Operator: assert.OpEquals,
		Expected: step.Expect.ExitCode,
		Actual:   got,
		Line:     step.Expect.ExitCodeLine,
	}
	if got == step.Expect.ExitCode {
		return a.Pass()
	}
	return a.Fail()
}

// parseJSON decodes stdout as a JSON object, or returns nil. Output that is not
// JSON is not an error here: whether the step needed JSON is decided by the
// captures it declared, and each of those says so itself.
func parseJSON(step models.Step, out []byte) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		logger.Logger.Debug("Command output is not a JSON object", "name", step.Name, "error", err.Error())
		return nil
	}
	return parsed
}

func (e Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// cappedBuffer collects up to limit bytes and counts the rest.
//
// It always reports every byte as written: a short write would be an I/O error
// to the child, which would make a chatty command fail for a reason that has
// nothing to do with the scenario.
type cappedBuffer struct {
	buf     bytes.Buffer
	limit   int
	dropped int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	switch room := c.limit - c.buf.Len(); {
	case room <= 0:
		c.dropped += len(p)
	case room < len(p):
		c.buf.Write(p[:room])
		c.dropped += len(p) - room
	default:
		c.buf.Write(p)
	}
	return len(p), nil
}

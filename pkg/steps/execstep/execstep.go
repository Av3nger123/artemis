// Package execstep is the terminal step: it runs a command and reports its
// exit code and its two streams. It is registered in the default registry
// under the step type "terminal", which is what pkg/dsl/lower stamps on a step
// whose action block is `run`.
//
// The key was "exec" until ART-40, because that is what a YAML `type:` spelled
// and the YAML loader validated against the registry. With YAML off the run
// path the registry answers to the front end alone, and the front end -- the
// checker, the diagnostics, `artemis grammar` -- has always called this a
// terminal step.
//
// It is the escape hatch. Anything artemis has no step type for -- a CLI, a
// migration script, a health check that is a shell one-liner -- is an exec step,
// and it is the second implementation of the ART-15 Executor interface, which is
// what makes that interface something other than HTTP wearing a hat.
//
// There is no shell. Command is executed directly with Args: no word splitting,
// no globbing, no `sh -c` unless the scenario names a shell itself. A scenario
// that wants a pipeline writes `run "sh" { args = ["-c", "..."] }`, which is
// longer to write and impossible to misread.
//
// Per ART-15's contract: one attempt per Observe, no retrying, no timing, and
// no assertions. A command that ran and exited non-zero is an observation, not
// an error -- the error return is for a command that could not be run at all,
// and `expect exit_code == 0` is what decides whether the scenario minds.
package execstep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// StepType is the type a scenario writes to get this executor.
const StepType = "terminal"

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
	}{{RootStdout, &stdout}, {RootStderr, &stderr}} {
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

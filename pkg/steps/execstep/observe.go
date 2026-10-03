package execstep

import (
	"context"
	"errors"
	"strings"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The roots a terminal step binds, which is what `expect` and `capture` in a
// .art `run` step may name.
//
// Declared here and compared against check.Roots(Terminal) in observe_test.go,
// for the reason httpstep's are: a step package must not depend on the front
// end, and a test is what keeps the checker's list and this one equal.
//
// They are the same three things ART-17's exec step already checks -- the exit
// code and the two streams -- which is the whole point. `terminal` is the YAML
// exec step with the `run` action block in front of it, not a new step type.
const (
	// RootExitCode is the command's exit status, as a number.
	RootExitCode = "exit_code"
	// RootStdout is everything the command wrote to stdout, capped at MaxOutput.
	RootStdout = "stdout"
	// RootStderr is everything it wrote to stderr, capped at MaxOutput.
	RootStderr = "stderr"
)

var _ executor.Observer = Executor{}

// Observe runs the command once and reports what it did, leaving every
// assertion to the caller.
//
// ART-17's semantics are unchanged, and that is what this issue owes the
// `terminal` step: there is no shell, Args is passed as argv with no word
// splitting, each stream is capped at MaxOutput, the step's `env` is layered
// over artemis's own, and a command that ran and exited non-zero is an
// observation rather than an error -- `expect exit_code == 0` is what decides
// whether the scenario minds.
//
// The step is already rendered, so the spec is step.Exec verbatim: pkg/dsl/lower
// evaluated the command, the args, the cwd, the stdin and the env against the
// scenario's scope, and running pkg/shared's {{}} substituter over the result
// would reinterpret an argument that legitimately contains a brace -- which for
// a shell one-liner or a jq program is not a rare thing to write.
func (e Executor) Observe(ctx context.Context, step models.Step, _ executor.Scope) (map[string]any, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}

	spec := step.Exec
	if strings.TrimSpace(spec.Command) == "" {
		// The same check render makes, for the same reason: a step with nothing
		// to run must say so rather than ask the OS about "".
		return nil, errors.New("terminal step has no command to run")
	}

	// The step's deadline hangs off the one the run was given, so cancelling a
	// run kills the command it is waiting on rather than waiting it out.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := e.run(ctx, step, spec, timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		// float64, not int: every number in the evaluator's domain is one, so
		// `expect exit_code == 0` compares two numbers of the same type.
		RootExitCode: float64(out.exitCode),
		RootStdout:   out.stdout,
		RootStderr:   out.stderr,
	}, nil
}

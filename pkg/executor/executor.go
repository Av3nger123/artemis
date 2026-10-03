// Package executor is the seam between the runner and the kinds of thing a step
// can be. One interface -- Executor -- one attempt of one step; a Registry
// mapping a step type to the executor that runs it; and Observe, which
// dispatches on Step.Type and refuses a type nothing is registered for (ART-7).
//
// The four implementations this exists for are http, terminal, db and browser.
// It is sized for those and not meant to grow: a second method here is four
// implementations of it.
package executor

import (
	"context"

	"artemis/pkg/shared/models"
)

// Executor runs one kind of step and reports what it saw.
//
// The division of labour between an executor and the runner is the whole of
// this contract, so it is written down here rather than left to be rediscovered
// in the fourth implementation:
//
// The executor makes one attempt. It does the thing -- sends the request, runs
// the command, drives the page -- and hands back the step's roots: what it
// observed, by the name the step's *type* binds them under. It does not retry,
// does not time itself, does not know how many attempts came before it, and
// makes no assertions at all.
//
// The runner owns the result tree and every judgement in it. It decides how
// many attempts to make (models.Retry is declared per step, not per type),
// stamps Name, Attempts and Duration on the step in the tree, evaluates the
// step's `expect` expressions against the observation through pkg/eval, and
// calls Finish. A later attempt replaces an earlier one whole, so a recorded
// step is never a mix of two tries.
//
// A returned error means the step could not run at all: a connection that was
// refused, a binary that is not on the PATH. A wrong answer is not an error --
// a command that exited non-zero and a request that answered 500 are both
// observations, and what the scenario thinks of them is the runner's business.
//
// The context is the fourth thing in this contract. It carries the run's
// cancellation and, for a browser step, the session registry the scenario's
// page lives in -- see sessions.go. An executor *borrows* a session and never
// closes one: lifetime belongs to the registry, so a step that panicked cannot
// leak a browser process. The http and terminal executors read nothing off it
// but the deadline, which they make their own per-attempt timeout a child of,
// so a cancelled run stops making requests.
//
// There was a second method here until ART-40: Execute, which asserted the
// step's checks itself. It existed because a YAML step carried its checks as
// data -- a status code, a list of body checks -- so the executor was the only
// thing that could evaluate them. A `.art` step carries them as expressions, so
// there is one question left to ask.
//
// The roots map is in the evaluator's domain, which is JSON's: nil, bool,
// float64, string, []any and map[string]any, plus eval.Headers for a header
// set. A number is a float64 whatever it arrived as, so nothing downstream
// needs a type table. The names -- status, body, raw, headers for an api step;
// exit_code, stdout, stderr for a terminal one -- are pkg/dsl/check's:
// check.Roots is exported and each implementation's tests compare against it,
// because a root the checker admits and the runtime never binds is an expect
// that silently errors at run time.
//
// The step it is handed is already rendered. Every value position in a .art
// file is an expression evaluated by pkg/dsl/lower before this is called, so an
// implementation must never re-interpret what it gets: a URL that legitimately
// contains a brace is not a template.
type Executor interface {
	Observe(ctx context.Context, step models.Step, scope Scope) (map[string]any, error)
}

// Func adapts a plain function to Executor, for a trivial executor and for a
// test that needs one.
type Func func(ctx context.Context, step models.Step, scope Scope) (map[string]any, error)

// Observe calls f.
func (f Func) Observe(ctx context.Context, step models.Step, scope Scope) (map[string]any, error) {
	return f(ctx, step, scope)
}

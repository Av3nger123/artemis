// Package executor is the seam between the runner and the kinds of thing a step
// can be. One interface -- Executor -- one attempt of one step; a Registry
// mapping a step type to the executor that runs it; and Run, which dispatches on
// Step.Type and refuses a type nothing is registered for (ART-7).
//
// The four implementations this exists for are http, exec, db and browser. It is
// sized for those and not meant to grow: a fifth method here is five
// implementations of it.
package executor

import (
	"context"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// Executor runs one kind of step.
//
// The division of labour between an executor and the runner is the whole of this
// contract, so it is written down here rather than left to be rediscovered in
// the fourth implementation:
//
// The executor makes one attempt. It renders what it needs out of scope, does
// the thing -- sends the request, runs the command, runs the query -- records an
// assertion for every check the step asked for, and writes whatever the step
// captured back into scope. It does not retry, does not time itself, and does
// not know how many attempts came before it.
//
// The runner owns the result tree. It decides how many attempts to make
// (models.Retry is declared per step, not per type), stamps Name, Attempts and
// Duration on the step in the tree, copies the assertions of the attempt it
// stopped on, and calls Finish. A later attempt replaces an earlier one whole,
// so a recorded step is never a mix of two tries.
//
// A returned error means the step could not run at all: a URL that will not
// render, a connection that was refused, a binary that is not on the PATH. A
// wrong answer is not an error -- it is a failed assertion on the returned
// result. An executor that returns an error may return a nil result with it; the
// runner fails the step with the error either way.
//
// The context is the fourth thing in this contract and the newest. It carries
// the run's cancellation and, for a browser step, the session registry the
// scenario's page lives in -- see sessions.go. An executor *borrows* a session
// and never closes one: lifetime belongs to the registry, so a step that
// panicked cannot leak a browser process. The http and terminal executors read
// nothing off it but the deadline, which they make their own per-attempt
// timeout a child of, so a cancelled run stops making requests.
//
// The result an executor returns is deliberately partial. It has assertions and
// nothing else: no name, no duration, and Finish has not been called on it.
// Returning result.StepResult rather than a narrower attempt type keeps one
// shape for "what a step produced" instead of two.
type Executor interface {
	Execute(ctx context.Context, step models.Step, scope Scope) (*result.StepResult, error)
}

// Func adapts a plain function to Executor, for a trivial executor and for a
// test that needs one.
type Func func(ctx context.Context, step models.Step, scope Scope) (*result.StepResult, error)

// Execute calls f.
func (f Func) Execute(ctx context.Context, step models.Step, scope Scope) (*result.StepResult, error) {
	return f(ctx, step, scope)
}

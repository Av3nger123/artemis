package executor

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"context"

	"artemis/pkg/shared/models"
)

// RootDurationMS is how long the attempt took, in milliseconds, as a number.
// Every step binds it whatever its type, because it is measured here, around
// the executor, rather than by one.
const RootDurationMS = "duration_ms"

// ErrUnknownStepType is what a step whose type nothing is registered for
// produces. It is wrapped rather than returned bare so the message can name both
// the offending type and the types that do exist; errors.Is still matches it.
var ErrUnknownStepType = errors.New("unknown step type")

// Observe makes one attempt at step with the executor registered for its type,
// and returns what the step observed.
//
// A type nothing is registered for is an error (ART-7), not a skip and not a
// pass: a step artemis cannot execute is the quiet failure this dispatch exists
// to prevent. The caller -- the runner -- fails the step with it.
//
// Everything else Observe does is hand the context, the step and the scope
// straight through, so the contract in Executor's doc comment is the contract
// here too: one attempt, roots on the way back, an error only when the step
// could not run. The one thing it adds is duration_ms: the attempt's wall time,
// from handing the step over to having its roots back -- for an api step,
// request built, sent, and every byte of the body read.
func Observe(ctx context.Context, reg *Registry, step models.Step, scope Scope) (map[string]any, error) {
	e, ok := reg.Lookup(step.Type)
	if !ok {
		return nil, unknownStepTypeError(step.Type, reg.Types())
	}
	start := time.Now()
	roots, err := e.Observe(ctx, step, scope)
	if err != nil {
		return nil, err
	}
	if roots == nil {
		roots = map[string]any{}
	}
	roots[RootDurationMS] = float64(time.Since(start).Microseconds()) / 1000
	return roots, nil
}

// unknownStepTypeError names the type that was asked for and the way out of it.
func unknownStepTypeError(stepType string, known []string) error {
	if len(known) == 0 {
		return fmt.Errorf("%w %q (no step types are registered)", ErrUnknownStepType, stepType)
	}
	return fmt.Errorf("%w %q (known types: %s)", ErrUnknownStepType, stepType, strings.Join(known, ", "))
}

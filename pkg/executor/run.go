package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// ErrUnknownStepType is what a step whose type nothing is registered for
// produces. It is wrapped rather than returned bare so the message can name both
// the offending type and the types that do exist; errors.Is still matches it.
var ErrUnknownStepType = errors.New("unknown step type")

// Run makes one attempt at step, using the executor registered for its type.
//
// A type nothing is registered for is an error (ART-7), not a skip and not a
// pass: a step artemis cannot execute is the quiet failure this dispatch exists
// to prevent. The caller -- the runner -- fails the step with it.
//
// Everything else Run does is hand the context, the step and the scope straight
// through, so the contract in Executor's doc comment is the contract here too:
// one attempt, assertions on the returned result, an error only when the step
// could not run.
func Run(ctx context.Context, reg *Registry, step models.Step, scope Scope) (*result.StepResult, error) {
	e, ok := reg.Lookup(step.Type)
	if !ok {
		return nil, unknownStepTypeError(step.Type, reg.Types())
	}
	return e.Execute(ctx, step, scope)
}

// unknownStepTypeError names the type that was asked for and the way out of it.
// The "known types: ..." wording matches models.validateStep, so the same
// mistake reads the same whether it is caught at load time or at dispatch.
func unknownStepTypeError(stepType string, known []string) error {
	if len(known) == 0 {
		return fmt.Errorf("%w %q (no step types are registered)", ErrUnknownStepType, stepType)
	}
	return fmt.Errorf("%w %q (known types: %s)", ErrUnknownStepType, stepType, strings.Join(known, ", "))
}

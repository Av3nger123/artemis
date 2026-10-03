package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"artemis/pkg/shared/models"
)

// This file is an executor's second way to be asked, and it exists because the
// two front ends ask different questions.
//
// A YAML step carries its checks as data -- models.Response.StatusCode, a list
// of models.BodyCheck, a map of captures -- so the executor that runs it is the
// only thing that can evaluate them, and Execute is "run this step and say
// whether it passed".
//
// A .art step carries its checks as expressions. `expect status == 200` is one
// assertion the caller evaluates against the step's observation, so what the
// executor owes it is the observation and nothing else. That is Observe.
//
// Both exist until ART-40 deletes models.BodyCheck and Execute's assertion half
// with it. They share the transport -- one request, one process launch -- and
// nothing else, so an api step's deadline, retry policy and error wording are
// the same whichever way it was asked.

// Observer is an executor that can report what a step observed, for a caller
// that makes the assertions itself.
//
// The returned map is the step's roots, by the name its *type* binds them
// under: status, body, raw and headers for an api step, exit_code, stdout and
// stderr for a terminal one. Those names are pkg/dsl/check's -- check.Roots is
// exported and each implementation's tests compare against it -- because a root
// the checker admits and the runtime never binds is an expect that silently
// errors at run time.
//
// The values are in the evaluator's domain, which is JSON's: nil, bool,
// float64, string, []any and map[string]any, plus eval.Headers for a header
// set. A number is a float64 whatever it arrived as, so nothing downstream
// needs a type table.
//
// The contract is otherwise Execute's, and deliberately so: one attempt, no
// retrying, no timing, and an error only when the step could not run at all. A
// command that exited non-zero and a request that answered 500 are both
// observations, not errors -- what the scenario thinks of them is the caller's
// business.
//
// The step it is handed is already rendered. Every value position in a .art
// file is an expression evaluated by pkg/dsl/lower before this is called, so an
// implementation must not run pkg/shared's {{}} substituter over what it gets:
// a URL that legitimately contains a brace is not a template.
type Observer interface {
	Observe(ctx context.Context, step models.Step, scope Scope) (map[string]any, error)
}

// ErrNoObservation is what a step type whose executor cannot report an
// observation produces. It is wrapped rather than returned bare so the message
// can name the type; errors.Is still matches it.
//
// It is reachable for a type that is registered but has no Observe -- "browser"
// until ART-47 -- which is an error rather than a skip and rather than a pass,
// for the same reason ErrUnknownStepType is.
var ErrNoObservation = errors.New("cannot report what it observed")

// Observe asks the executor registered for step.Type what the step observed.
//
// A type nothing is registered for is ErrUnknownStepType, exactly as Run
// reports it, so the same mistake reads the same whichever way the step was
// dispatched. The caller -- the runner -- fails the step with either error.
func Observe(ctx context.Context, reg *Registry, step models.Step, scope Scope) (map[string]any, error) {
	e, ok := reg.Lookup(step.Type)
	if !ok {
		return nil, unknownStepTypeError(step.Type, reg.Types())
	}
	o, ok := e.(Observer)
	if !ok {
		return nil, noObservationError(step.Type, reg.Observable())
	}
	return o.Observe(ctx, step, scope)
}

// Observable returns every registered step type whose executor can report an
// observation, sorted, so an error message reads the same on every run.
func (r *Registry) Observable() []string {
	var out []string
	for _, t := range r.Types() {
		e, _ := r.Lookup(t)
		if _, ok := e.(Observer); ok {
			out = append(out, t)
		}
	}
	return out
}

// noObservationError names the type that cannot be observed and the ones that
// can, matching unknownStepTypeError's shape: the reader's next move is the
// same in both cases, which is to look at the step's action block.
func noObservationError(stepType string, known []string) error {
	if len(known) == 0 {
		return fmt.Errorf("step type %q %w (no step type can)", stepType, ErrNoObservation)
	}
	return fmt.Errorf("step type %q %w (these can: %s)", stepType, ErrNoObservation, strings.Join(known, ", "))
}

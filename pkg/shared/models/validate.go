package models

import (
	"errors"
	"fmt"
	"strings"
)

// StepTypes are the step types artemis can execute, in the spelling a scenario
// must use. A type named here that nothing executes would be accepted at load
// time and then error at run time, which is the quiet failure this list exists
// to prevent -- so the list and the executors have to agree.
//
// This is the declared half of that agreement; executor.Registry.Types() is the
// registered half, and it is the one to derive this from rather than keeping two
// lists in step. models cannot read it directly -- pkg/executor imports models,
// so the import can only go one way -- which means the known types have to be
// handed to Validate. Doing that is ART-16's job, when there is a first executor
// registered to hand over.
var StepTypes = []string{"api"}

// IsKnownStepType reports whether t is a type artemis can execute. The match is
// exact: `API` is not `api`, so the set of valid spellings stays equal to the
// set of documented ones.
func IsKnownStepType(t string) bool {
	for _, known := range StepTypes {
		if t == known {
			return true
		}
	}
	return false
}

// Validate reports everything about the scenario that would stop artemis from
// executing it, before a single request is sent. A scenario artemis cannot
// fully run should not half-run: a bad step type three steps in must not be
// found only after two requests have already gone out.
//
// It collects every problem rather than stopping at the first, so a file with
// three typo'd step types takes one run to fix.
//
// What it does not check: whether an `api` step actually has a usable url and
// method. That is the step executor's business, and it is caught when the
// request is made.
func (c Config) Validate() error {
	var problems []error
	for i := range c.Steps {
		if err := validateStep(c.Steps[i], i); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// validateStep names the step by its 1-based position as well as its name,
// because a scenario may leave the name off or repeat it.
func validateStep(step Step, i int) error {
	where := fmt.Sprintf("step %d %q", i+1, step.Name)
	known := strings.Join(StepTypes, ", ")
	switch {
	case step.Type == "":
		return fmt.Errorf("%s: missing type (known types: %s)", where, known)
	case !IsKnownStepType(step.Type):
		return fmt.Errorf("%s: unknown type %q (known types: %s)", where, step.Type, known)
	}
	return nil
}

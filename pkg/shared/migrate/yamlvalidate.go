package migrate

import (
	"errors"
	"fmt"
	"strings"
)

// Validate reports everything about the scenario that would stop artemis from
// executing it, before a single request is sent. A scenario artemis cannot
// fully run should not half-run: a bad step type three steps in must not be
// found only after two requests have already gone out.
//
// known is the step types artemis can execute, in the spelling a scenario must
// use. There is no list of them in this package: a type named here that nothing
// executes would be accepted at load time and then error at run time, which is
// the quiet failure this check exists to prevent, so the only list worth
// validating against is the one the executors registered themselves in
// (executor.Registry.Types). pkg/executor imports this package, so the import
// can only go one way and the list has to be handed in.
//
// It collects every problem rather than stopping at the first, so a file with
// three typo'd step types takes one run to fix.
//
// What it does not check: whether an `api` step actually has a usable url and
// method. That is the step executor's business, and it is caught when the
// request is made.
func (c Config) Validate(known []string) error {
	var problems []error
	for i := range c.Steps {
		if err := validateStep(c.Steps[i], i, known); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// validateStep names the step by its 1-based position as well as its name,
// because a scenario may leave the name off or repeat it.
func validateStep(step Step, i int, known []string) error {
	where := fmt.Sprintf("step %d %q", i+1, step.Name)
	list := strings.Join(known, ", ")
	if list == "" {
		list = "none are registered"
	}
	switch {
	case step.Type == "":
		return fmt.Errorf("%s: missing type (known types: %s)", where, list)
	case !isKnown(step.Type, known):
		return fmt.Errorf("%s: unknown type %q (known types: %s)", where, step.Type, list)
	}
	return validateCaptures(step, where)
}

// validateCaptures checks the two things about a `capture:` map that Capture's
// own decoder cannot see: the name a value is captured under, which is the map
// key rather than part of the value, and a capture with no source at all, which
// yaml.v3 produces for `token:` with nothing after it -- a null node never
// reaches an UnmarshalYAML method.
//
// Keys are reported in sorted order so a scenario with two bad ones reads the
// same on every run, and every problem is collected, like Validate's own.
func validateCaptures(step Step, where string) error {
	var problems []error
	for _, key := range step.CaptureKeys() {
		switch c := step.Capture[key]; {
		case strings.TrimSpace(key) == "":
			problems = append(problems, fmt.Errorf("%s: capture %q has no name", where, key))
		case strings.TrimSpace(c.JSON) == "" && strings.TrimSpace(c.Regex) == "":
			problems = append(problems, fmt.Errorf("%s: capture %q gives no json or regex to read the value with", where, key))
		}
	}
	return errors.Join(problems...)
}

// isKnown reports whether t is one of known. The match is exact: `API` is not
// `api`, so the set of valid spellings stays equal to the set of documented
// ones.
func isKnown(t string, known []string) bool {
	for _, k := range known {
		if t == k {
			return true
		}
	}
	return false
}

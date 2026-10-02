// Package assert compares a response body against the checks a scenario asked
// for and reports each one as an ART-1 assertion result.
//
// Every check names an operator -- equals by default -- and every check returns
// exactly one result.AssertionResult. A check that cannot be made at all (a
// malformed path, a path that does not resolve, an operator this build does not
// know, a regex that will not compile) is an errored assertion carrying the
// reason, never a silent false.
//
// This package imports only models, result and the shared jsonpath wrapper, so
// it can be tested without an HTTP server.
package assert

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"artemis/pkg/result"
	"artemis/pkg/shared/jsonpath"
	"artemis/pkg/shared/models"
)

// The operators a check may name.
const (
	OpEquals   = "equals"
	OpContains = "contains"
	OpMatches  = "matches"
	OpExists   = "exists"
	OpType     = "type"
	OpGt       = "gt"
	OpGte      = "gte"
	OpLt       = "lt"
	OpLte      = "lte"
)

// Operators are every operator this build understands, for an error message or
// for ART-7 to validate a scenario against.
var Operators = []string{OpEquals, OpContains, OpMatches, OpExists, OpType, OpGt, OpGte, OpLt, OpLte}

// Body checks every body check of step against response and returns one result
// per check, in order.
func Body(step models.Step, response map[string]any) []result.AssertionResult {
	out := make([]result.AssertionResult, 0, len(step.Response.Body))
	for _, check := range step.Response.Body {
		out = append(out, Check(step.Name, check, response))
	}
	return out
}

// Check makes one assertion against response and says what happened. It never
// panics: a path library that trips over its input becomes an errored
// assertion like any other failure to make the check.
func Check(stepName string, check models.BodyCheck, response map[string]any) (res result.AssertionResult) {
	op := check.Operator
	if op == "" {
		op = OpEquals
	}
	a := result.Assertion{
		Step:     stepName,
		Kind:     "body",
		Path:     check.Path,
		Operator: op,
		Expected: normalizeYAML(check.Value),
	}
	defer func() {
		if r := recover(); r != nil {
			res = a.Errored(fmt.Errorf("could not evaluate %s: %v", check.Path, r))
		}
	}()

	if !knownOperator(op) {
		return a.Errored(fmt.Errorf("unknown operator %q: expected one of %s", op, strings.Join(Operators, ", ")))
	}
	if strings.TrimSpace(check.Path) == "" {
		return a.Errored(fmt.Errorf("check has no path"))
	}
	compiled, err := jsonpath.Compile(check.Path)
	if err != nil {
		return a.Errored(fmt.Errorf("malformed path %s: %v", check.Path, err))
	}
	actual, lookupErr := compiled.Lookup(response)

	if op == OpExists {
		return checkExists(a, lookupErr == nil, actual)
	}
	if errors.Is(lookupErr, jsonpath.ErrNotFound) {
		return a.Errored(fmt.Errorf("path %s did not resolve", check.Path))
	}
	if lookupErr != nil {
		return a.Errored(fmt.Errorf("path %s did not resolve: %v", check.Path, lookupErr))
	}

	a.Actual = actual

	if op == OpType {
		return checkType(a)
	}
	if check.Type != "" {
		if !knownType(check.Type) {
			return a.Errored(fmt.Errorf("unknown type %q: expected one of %s", check.Type, strings.Join(TypeNames, ", ")))
		}
		if got := jsonTypeOf(actual); got != check.Type {
			return a.Errored(fmt.Errorf("path %s is a %s, not the %s the check asks for", check.Path, got, check.Type))
		}
		want, err := coerceToType(a.Expected, check.Type)
		if err != nil {
			return a.Errored(fmt.Errorf("value for %s: %v", check.Path, err))
		}
		a.Expected = want
	}

	switch op {
	case OpEquals:
		return pass(a, equalValues(a.Expected, actual))
	case OpContains:
		return checkContains(a)
	case OpMatches:
		return checkMatches(a)
	default:
		return checkOrder(a, op)
	}
}

func knownOperator(op string) bool {
	for _, o := range Operators {
		if o == op {
			return true
		}
	}
	return false
}

// pass records a as passed or failed.
func pass(a result.Assertion, ok bool) result.AssertionResult {
	if ok {
		return a.Pass()
	}
	return a.Fail()
}

// checkExists answers whether the path is there at all. A path that does not
// resolve is the answer here, not an error, and `value: false` asks for a key
// to be absent. A path that resolves to JSON null counts as absent.
func checkExists(a result.Assertion, resolved bool, actual any) result.AssertionResult {
	want := truthy(a.Expected)
	a.Expected = want
	if resolved {
		a.Actual = actual
	}
	return pass(a, (resolved && actual != nil) == want)
}

// checkType compares the value's JSON type against the type the check names.
func checkType(a result.Assertion) result.AssertionResult {
	want, ok := a.Expected.(string)
	if !ok {
		return a.Errored(fmt.Errorf("the type operator needs a type name, got %v", a.Expected))
	}
	if !knownType(want) {
		return a.Errored(fmt.Errorf("unknown type %q: expected one of %s", want, strings.Join(TypeNames, ", ")))
	}
	got := jsonTypeOf(a.Actual)
	a.Actual = got
	return pass(a, got == want)
}

// checkContains tests membership: a substring of a string, an element of an
// array, or a key of an object.
func checkContains(a result.Assertion) result.AssertionResult {
	switch actual := a.Actual.(type) {
	case string:
		want, ok := toString(a.Expected)
		if !ok {
			return a.Errored(fmt.Errorf("contains against a string needs a scalar value, got %v", a.Expected))
		}
		return pass(a, strings.Contains(actual, want))
	case []any:
		for _, el := range actual {
			if equalValues(a.Expected, el) {
				return a.Pass()
			}
		}
		return a.Fail()
	case map[string]any:
		key, ok := a.Expected.(string)
		if !ok {
			return a.Errored(fmt.Errorf("contains against an object needs a key name, got %v", a.Expected))
		}
		_, found := actual[key]
		return pass(a, found)
	default:
		return a.Errored(fmt.Errorf("contains needs a string, array or object at %s, got %s", a.Path, jsonTypeOf(a.Actual)))
	}
}

// checkMatches matches the value, rendered as a string, against the regex the
// check gives. The pattern is unanchored, as a regex usually is.
func checkMatches(a result.Assertion) result.AssertionResult {
	pattern, ok := a.Expected.(string)
	if !ok {
		return a.Errored(fmt.Errorf("matches needs a regular expression, got %v", a.Expected))
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return a.Errored(fmt.Errorf("bad regular expression %q: %v", pattern, err))
	}
	got, ok := toString(a.Actual)
	if !ok {
		return a.Errored(fmt.Errorf("matches needs a string at %s, got %s", a.Path, jsonTypeOf(a.Actual)))
	}
	return pass(a, re.MatchString(got))
}

// checkOrder compares two numbers. A side that is not a number is an errored
// assertion: "is an object greater than 3" has no false answer worth printing.
func checkOrder(a result.Assertion, op string) result.AssertionResult {
	got, ok := toFloat(a.Actual)
	if !ok {
		return a.Errored(fmt.Errorf("%s needs a number at %s, got %s", op, a.Path, jsonTypeOf(a.Actual)))
	}
	want, ok := toFloat(a.Expected)
	if !ok {
		return a.Errored(fmt.Errorf("%s needs a numeric value, got %v", op, a.Expected))
	}
	switch op {
	case OpGt:
		return pass(a, got > want)
	case OpGte:
		return pass(a, got >= want)
	case OpLt:
		return pass(a, got < want)
	default:
		return pass(a, got <= want)
	}
}

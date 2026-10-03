package migrate

import (
	"fmt"
	"strconv"
	"strings"

	"artemis/pkg/dsl/token"
)

// The operator mapping, which is this issue's substance.
//
// It is derived from pkg/shared/assert's control flow rather than from the
// design document's table read on its own, because the table is a summary and
// the runtime is the specification. Two places it matters:
//
//   - `operator: exists` returns at Check's checkExists call *before*
//     the `type:` key is ever looked at, so a check carrying both asserts
//     existence only. Emitting an `is` beside it would add an assertion the
//     YAML run never made.
//   - `operator: type` takes the type name from `value:`, not from `type:`.
//     `type:` is a separate guard, which with any other operator asserts the
//     value's JSON type *as well as* the comparison -- two YAML assertions in
//     one check, and so two expect lines.
//
// An operator this build does not know is an error naming the list, the same
// way a run of it would be an errored assertion naming the list.

// bodyExpects is the expect lines one `body:` check becomes, in the order they
// should be written.
func bodyExpects(check BodyCheck) ([]string, error) {
	subject, err := pathExpr("body", check.Path)
	if err != nil {
		return nil, err
	}

	op := check.Operator
	if op == "" {
		op = OpEquals
	}

	switch op {
	case OpExists:
		return []string{existsExpect(subject, check.Value)}, nil
	case OpType:
		name, ok := check.Value.(string)
		if !ok {
			return nil, fmt.Errorf("operator %s needs a type name in value:, found %v", op, check.Value)
		}
		line, err := isExpect(subject, name)
		if err != nil {
			return nil, err
		}
		return []string{line}, nil
	}

	var lines []string
	if check.Type != "" {
		line, err := isExpect(subject, check.Type)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}

	line, err := comparisonExpect(subject, op, check.Value)
	if err != nil {
		return nil, err
	}
	return append(lines, line), nil
}

// existsExpect is `expect body.x exists`, negated when the check asked for the
// path to be absent.
//
// What counts as asking for absence is assert's own truthy: no value at all
// means present, `false` and the string "false" mean absent, and anything else
// means present. A check written `value: 0` therefore migrates to the positive
// form, because that is what it asserted.
func existsExpect(subject string, value any) string {
	if wantsPresent(value) {
		return "expect " + subject + " exists"
	}
	return "expect not " + subject + " exists"
}

// wantsPresent reads an `operator: exists` check's value as the question it
// was asking: is the path there, or is it absent?
//
// The rule is the one pkg/shared/assert applied before ART-40 deleted it, kept
// verbatim so a migrated file asserts what the YAML one did. It is a strange
// rule and it is deliberately not improved here: a missing value means
// presence, a bool means itself, a string is read as a bool and anything that
// will not parse as one -- `value: yes`, `value: ""` -- means presence, and a
// number or a collection means presence. The table test below states it.
func wantsPresent(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(t)
		return err != nil || b
	default:
		return true
	}
}

// isExpect is `expect body.x is number`, with the type name checked against the
// DSL's own list so a `type: integer` is refused here rather than becoming a
// file the checker rejects.
func isExpect(subject, name string) (string, error) {
	if !token.IsTypeName(name) {
		return "", fmt.Errorf("type %q is not one the DSL knows (%s)", name, strings.Join(token.TypeNames, ", "))
	}
	return "expect " + subject + " is " + name, nil
}

// comparisonExpect is the one expect line for an operator that compares.
func comparisonExpect(subject, op string, value any) (string, error) {
	if op == OpMatches {
		pattern, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("operator %s needs a regular expression in value:, found %v", op, value)
		}
		lit, err := regexLiteral(pattern)
		if err != nil {
			return "", err
		}
		return "expect " + subject + " matches " + lit, nil
	}

	symbol, ok := comparisons[op]
	if !ok {
		return "", fmt.Errorf("unknown operator %q: expected one of %s", op, strings.Join(Operators, ", "))
	}
	lit, err := plain(value)
	if err != nil {
		return "", err
	}
	return "expect " + subject + " " + symbol + " " + lit, nil
}

// comparisons is each YAML operator's DSL spelling, for the operators whose
// right-hand side is an ordinary value. `exists`, `type` and `matches` are not
// here because each needs its own shape.
var comparisons = map[string]string{
	OpEquals:   "==",
	OpContains: "contains",
	OpGt:       ">",
	OpGte:      ">=",
	OpLt:       "<",
	OpLte:      "<=",
}

// textExpect is the expect line one `expect.stdout` or `expect.stderr` check
// becomes. stream is the root the DSL binds -- "stdout" or "stderr" -- which is
// also the name of the YAML key the check was written under.
//
// A text check's operator defaults to `contains`, not `equals`, which is
// Text's default and the one a command's trailing newline makes right.
// `empty` has no DSL spelling: `== ""` is a different assertion, because empty
// is true of a stream holding only whitespace, so it is refused by name rather
// than approximated.
func textExpect(stream string, check TextCheck) (string, error) {
	op := check.Operator
	if op == "" {
		op = OpContains
	}

	switch op {
	case OpEquals:
		return "expect " + stream + " == " + quote(check.Value), nil
	case OpContains:
		return "expect " + stream + " contains " + quote(check.Value), nil
	case OpMatches:
		lit, err := regexLiteral(check.Value)
		if err != nil {
			return "", err
		}
		return "expect " + stream + " matches " + lit, nil
	case OpEmpty:
		return "", fmt.Errorf("operator %s on %s has no DSL spelling: it is true of a stream of whitespace, "+
			"which `%s == \"\"` is not; rewrite the check", op, stream, stream)
	default:
		return "", fmt.Errorf("unknown operator %q on %s: expected one of %s",
			op, stream, strings.Join(TextOperators, ", "))
	}
}

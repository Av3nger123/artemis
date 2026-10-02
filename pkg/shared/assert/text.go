package assert

import (
	"fmt"
	"regexp"
	"strings"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// OpEmpty is an operator only a text check has: the stream held nothing but
// whitespace. It exists because `equals ""` is the check that is right in
// theory and wrong in practice -- almost every command ends its output with a
// newline -- and "stderr was quiet" is what a scenario actually means.
const OpEmpty = "empty"

// TextOperators are the operators a text check may name. It is a shorter list
// than Operators on purpose: ordering and type operators have nothing to say
// about a stream of bytes, and offering them would only make a scenario that
// asks "is stdout greater than 3" look supported.
var TextOperators = []string{OpContains, OpEquals, OpMatches, OpEmpty}

// MaxActual is how much of a stream is kept on an assertion.
//
// AssertionResult.Describe prints Actual inline with %v, so a megabyte of stdout
// on one terminal line helps nobody; the streams in full go to the log. How a
// failed step renders is ART-12's subject.
const MaxActual = 200

// Text makes one assertion against a stream of text and says what happened.
//
// kind is the stream's name -- "stdout", "stderr" -- and becomes the assertion's
// Kind, which is what Describe prints in front of the comparison. There is no
// Path: the whole stream is the subject.
//
// An operator this build does not know is an errored assertion rather than a
// failed one, as it is for a body check: "that check could not be made" and
// "that check gave the wrong answer" are different things to read in a report.
func Text(stepName, kind string, check models.TextCheck, text string) result.AssertionResult {
	op := check.Operator
	if op == "" {
		op = OpContains
	}
	a := result.Assertion{
		Step:     stepName,
		Kind:     kind,
		Operator: op,
		Expected: check.Value,
		Actual:   Excerpt(text),
		Line:     check.Line,
	}

	switch op {
	case OpContains:
		return pass(a, strings.Contains(text, check.Value))
	case OpEquals:
		return pass(a, text == check.Value)
	case OpMatches:
		re, err := regexp.Compile(check.Value)
		if err != nil {
			return a.Errored(fmt.Errorf("bad regular expression %q: %v", check.Value, err))
		}
		return pass(a, re.MatchString(text))
	case OpEmpty:
		// The check takes no value, so there is nothing worth printing as
		// Expected beyond the word the scenario wrote.
		a.Expected = OpEmpty
		return pass(a, strings.TrimSpace(text) == "")
	default:
		return a.Errored(fmt.Errorf("unknown operator %q: expected one of %s", op, strings.Join(TextOperators, ", ")))
	}
}

// Excerpt is as much of a stream as is worth putting on an assertion: the first
// MaxActual bytes, with a marker when there was more. Truncation is on a byte
// boundary, which can split a multi-byte rune; the excerpt is for a person to
// read, and the stream it came from is in the log intact.
func Excerpt(text string) string {
	if len(text) <= MaxActual {
		return text
	}
	return text[:MaxActual] + fmt.Sprintf("... (%d bytes in all)", len(text))
}

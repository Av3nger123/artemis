package report

// This file is ART-12's half of the console report: the blocks a failed run ends
// with, which are the text an agent reads to fix the scenario it wrote.
//
// The per-step one-liners in console.go stay what they are -- the live transcript,
// printed as each step finishes. These are the dossier, printed once the run is
// over, and each block is self-contained: it names the file and the line, the
// scenario and the step, what was checked and what came back, so a reader holding
// one block needs nothing above it.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"artemis/pkg/result"
)

// failIndent is the left margin of a block's fields. It clears the "1) " a block
// opens with, so the fields hang under the location rather than beside it.
const failIndent = "     "

// labelWidth is how wide a field's label column is. Every label fits in it, so
// the values line up down the block and the eye finds "actual" without reading.
const labelWidth = 9

// Failures prints one block per thing the run says to go and fix.
//
// Nothing is printed for a run that passed, or for a nil one: a clean run should
// end with its summary and not with an empty heading.
//
// It is called between the step lines and the summary, so the summary's verdict
// stays the last line a run writes.
func (c *Console) Failures(run *result.RunResult) {
	diags := run.Diagnostics()
	if len(diags) == 0 {
		return
	}

	fmt.Fprintf(c.out, "\n%s:\n", plural(len(diags), "failure", "failures"))
	for i, d := range diags {
		fmt.Fprintf(c.out, "\n%d) %s\n", i+1, location(d))
		c.field("scenario", d.Scenario)
		c.field("step", d.Step)
		if d.Assertion != nil {
			c.field("assert", subject(*d.Assertion))
			if d.Assertion.Error != "" {
				c.field("error", d.Assertion.Error)
			} else {
				expected, actual := describeValues(*d.Assertion)
				c.field("expected", expected)
				c.field("actual", actual)
			}
			continue
		}
		c.field("error", d.Error)
	}
}

// field prints one labelled line of a block, and nothing at all when there is
// nothing to say: a block does not carry an empty "step" for a file that would
// not load.
//
// A value with newlines in it -- a YAML parse error is two lines -- is indented
// to the value column, so a block stays one visual unit instead of spilling into
// the margin and reading as a new block.
func (c *Console) field(label, value string) {
	if value == "" {
		return
	}
	pad := strings.Repeat(" ", labelWidth-len(label))
	indent := failIndent + strings.Repeat(" ", labelWidth+1)
	lines := strings.Split(value, "\n")
	fmt.Fprintf(c.out, "%s%s%s %s\n", failIndent, label, pad, strings.TrimSpace(lines[0]))
	for _, line := range lines[1:] {
		fmt.Fprintf(c.out, "%s%s\n", indent, strings.TrimSpace(line))
	}
}

// location is the heading of a block: the scenario file and the line to edit.
//
// A line artemis does not know is left off rather than printed as ":0", which
// would send a reader -- or an editor following the usual file:line convention --
// to a line that does not exist.
func location(d result.Diagnostic) string {
	where := d.File
	if where == "" {
		// Reachable only for a tree built in Go; a run always knows its files.
		where = "(no file)"
	}
	if d.Line > 0 {
		return where + ":" + strconv.Itoa(d.Line)
	}
	return where
}

// subject says what was checked: the kind of check, what it addressed, and the
// comparison applied -- "body $.status equals", "status_code equals",
// "capture token json", "stdout contains".
//
// Kind and Path are both printed, unlike in AssertionResult.Describe, which
// prints whichever it has. A block has room, and "body $.status" says more about
// which line of the scenario to look at than "$.status" does.
func subject(a result.AssertionResult) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{a.Kind, a.Path, a.Operator} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// Redacted is what every writer prints in place of a value that came from a
// binding the scenario declared `secret`. It is result's, so the console's
// inline line and these writers cannot disagree about the spelling. ART-54.
const Redacted = result.Redacted

// operands returns the two sides of an assertion as a reader may see them.
//
// It is AssertionResult.Shown under a name this package already reads like, and
// every writer here goes through it rather than reading Expected and Actual.
func operands(a result.AssertionResult) (expected, actual any) {
	return a.Shown()
}

// describeValues renders the two sides of a comparison, with each side's JSON
// type named when the two differ.
//
// The type is what makes the `200` against `"200"` mistake -- a YAML scalar that
// should have been quoted, or should not have been -- readable at all. When both
// sides are the same type it is left off: naming it on every line would be noise
// on the nine failures out of ten that are a plain wrong value.
//
// The type is computed from the *real* values and the text from the withheld
// ones. The type of a credential is not the credential, and a redaction that
// also removed this hint would make a withheld failure far harder to read.
func describeValues(a result.AssertionResult) (expected, actual string) {
	shownExpected, shownActual := operands(a)
	expected, actual = renderValue(shownExpected), renderValue(shownActual)
	if wantType, gotType := typeName(a.Expected), typeName(a.Actual); wantType != gotType {
		expected += "  (" + wantType + ")"
		actual += "  (" + gotType + ")"
	}
	return expected, actual
}

// renderValue renders one side of a comparison so that what it is, as well as
// what it says, survives.
//
// A string is quoted, so "" and a missing value do not read the same and so
// trailing whitespace is visible. A number, a bool and nil are their JSON
// spellings. Anything composite is compact JSON, which is both the shortest
// faithful rendering and one a reader can paste into jq.
func renderValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(val)
	case bool:
		return strconv.FormatBool(val)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		// A value no encoder can render is still worth printing something for.
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// typeName is the JSON type of a value, for the hint describeValues adds. It is
// this package's own rather than pkg/shared/assert's, which is about the types a
// `type:` check may name; this is about telling a reader why two values that look
// alike did not match.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// plural renders a count with the right noun. pkg/cli has its own copy for the
// exit error; this package imports nothing of artemis but pkg/result, and one
// four-line helper is cheaper than a package to share it.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

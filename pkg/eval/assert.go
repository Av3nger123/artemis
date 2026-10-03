package eval

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
	"artemis/pkg/result"
)

// Outcome is one `expect` evaluated: the verdict and the operands it was
// reached from.
//
// This is why Assert does not return a boolean. A reader of a failed run wants
//
//	body.data.count > 0, got 0
//
// which is the subject's source text, the operator, and *both* evaluated
// operands -- the expected one and the actual one. A boolean answer throws all
// four away, and a message rebuilt later from the tree alone would have to
// evaluate the expression a second time, against an observation that has since
// moved on.
//
// The fields are exactly result.Assertion's, so Record fills one in without a
// translation: the message every reporter prints comes from the one
// AssertionResult.Describe that exists today.
type Outcome struct {
	// Path is the source text of what was inspected: `body.data.count`. For an
	// expression with no single subject -- an `or` chain, a bare call -- it is
	// the whole expression.
	Path string

	// Operator is the comparison as the author wrote it: `>`, `contains`,
	// `exists`, `is`, or `not` prefixed to one of them. It is `is` for an
	// expression whose verdict is its own value.
	Operator string

	// Expected is the right-hand operand's value, the type name `is` asked
	// for, or true.
	Expected any

	// Actual is the subject's value, or the type name it turned out to have.
	Actual any

	// Passed is the verdict, meaningless when Err is set.
	Passed bool

	// Err is set when the assertion could not be made at all: an absent path,
	// `>` against an object. It is an *Error carrying a reason and a span.
	Err error
}

// Assert evaluates x as an assertion and says what it compared.
//
// It never panics and it never returns a bare false for a question it could
// not ask; the three outcomes are Passed, not Passed, and Err.
func Assert(x ast.Expr, env *Env) Outcome {
	x = unparen(x)

	switch x := x.(type) {
	case *ast.Binary:
		op := operatorOf(x.Op)
		if op == "and" || op == "or" {
			return assertLogical(x, op, env)
		}
		return assertCompare(x, op, env)

	case *ast.Exists:
		return assertExists(x, env)

	case *ast.IsType:
		return assertIsType(x, env)

	case *ast.Unary:
		if x.Op.Kind == token.Ident && x.Op.Value == "not" {
			return assertNot(x, env)
		}
	}
	return assertValue(x, env)
}

// Record turns an Outcome into the result-tree assertion ART-1 reports on.
// step, kind and line come from the step the expression was written in, which
// is what the lowerer knows and this package does not.
func (o Outcome) Record(step, kind string, line int) result.AssertionResult {
	a := result.Assertion{
		Step:     step,
		Kind:     kind,
		Path:     o.Path,
		Operator: o.Operator,
		Expected: o.Expected,
		Actual:   o.Actual,
		Line:     line,
	}
	switch {
	case o.Err != nil:
		return a.Errored(o.Err)
	case o.Passed:
		return a.Pass()
	default:
		return a.Fail()
	}
}

// assertCompare is `<subject> <op> <value>`, which is nearly every assertion
// anyone writes. Both operands are evaluated and kept, whichever way the
// comparison goes.
func assertCompare(b *ast.Binary, op string, env *Env) Outcome {
	o := Outcome{Path: exprText(b.X), Operator: op}

	left, err := Eval(b.X, env)
	if err != nil {
		o.Err = err
		return o
	}
	o.Actual = left

	right, err := Eval(b.Y, env)
	if err != nil {
		o.Err = err
		return o
	}
	o.Expected = right
	if rx, ok := right.(Regexp); ok {
		// A regex is reported as the author wrote it, slashes and all, so the
		// message reads `str matches /owner/` -- and so that nothing outside
		// this package has to know what a Regexp is, the JSON report
		// included.
		o.Expected = Render(rx)
	}

	v, err := compare(b, op, left, right)
	if err != nil {
		o.Err = err
		return o
	}
	o.Passed, _ = v.(bool)
	return o
}

// assertExists keeps assert.checkExists's shape: the expectation is the boolean
// the author asked for, and the value is reported when there was one.
func assertExists(e *ast.Exists, env *Env) Outcome {
	o := Outcome{Path: exprText(e.X), Operator: "exists", Expected: true}

	v, err := Eval(e.X, env)
	switch {
	case IsAbsent(err):
		return o
	case err != nil:
		o.Err = err
		return o
	}
	o.Actual = v
	o.Passed = v != nil
	return o
}

// assertIsType reports the type that was found against the one asked for, which
// is what makes the message read `body.count is number, got string`.
func assertIsType(i *ast.IsType, env *Env) Outcome {
	o := Outcome{Path: exprText(i.X), Operator: "is", Expected: i.Type.Value}

	if !isTypeName(i.Type.Value) {
		o.Err = errorf(i.Type.Span, "unknown type name %s", i.Type.Value)
		return o
	}
	v, err := Eval(i.X, env)
	if err != nil {
		o.Err = err
		return o
	}
	o.Actual = typeOf(v)
	o.Passed = o.Actual == i.Type.Value
	return o
}

// assertNot negates the assertion inside it and keeps its operands.
//
// `not <path> exists` keeps the operator and flips the expectation, which is
// the YAML `operator: exists, value: false` this replaces. Every other form
// prefixes the operator, so `not body.x == 1` reads `body.x not == 1, got 1`:
// the author wrote `not`, and a message that hid it would describe a different
// assertion.
func assertNot(u *ast.Unary, env *Env) Outcome {
	o := Assert(u.X, env)
	if o.Err != nil {
		return o
	}
	o.Passed = !o.Passed
	if o.Operator == "exists" {
		o.Expected = false
		return o
	}
	o.Operator = "not " + o.Operator
	return o
}

// assertLogical attributes an `and` or `or` chain to the operand that decided
// it, which is the whole reason sub-expression values are retained. One expect
// is still one assertion -- ART-1's granularity is unchanged -- but the line a
// reader gets names the half that actually broke:
//
//	expect status == 200 and body.ok     ->  body.ok is true, got false
//
// An `or` whose operands all failed has no one operand to blame, so it reports
// itself.
func assertLogical(b *ast.Binary, op string, env *Env) Outcome {
	left := Assert(b.X, env)
	if left.Err != nil {
		return left
	}
	if op == "and" && !left.Passed {
		return left
	}
	if op == "or" && left.Passed {
		return left
	}

	right := Assert(b.Y, env)
	if right.Err != nil || op == "and" {
		return right
	}
	if right.Passed {
		return right
	}
	// `a or b` with both halves false.
	return Outcome{Path: exprText(b), Operator: "is", Expected: true, Actual: false}
}

// assertValue is an expression that is its own verdict: `expect
// visible(".modal")`, or a captured boolean.
//
// It must be a boolean. There is no truthiness in this language, so
// `expect body.items` is an errored assertion rather than a test for a
// non-empty list -- the author either meant `exists` or meant a comparison,
// and guessing which would be the kind of silent reinterpretation this front
// end exists to delete.
func assertValue(x ast.Expr, env *Env) Outcome {
	o := Outcome{Path: exprText(x), Operator: "is", Expected: true}

	v, err := Eval(x, env)
	if err != nil {
		o.Err = err
		return o
	}
	o.Actual = v
	b, ok := v.(bool)
	if !ok {
		o.Err = errorf(x.Span(), "an expect needs a boolean at %s, got %s", exprText(x), typeOf(v))
		return o
	}
	o.Passed = b
	return o
}

// unparen looks through parentheses for the purpose of describing an assertion.
// `(status == 200)` is still a comparison; the parentheses are source the
// printer owes the author and say nothing about the verdict.
func unparen(x ast.Expr) ast.Expr {
	for {
		p, ok := x.(*ast.Paren)
		if !ok || p == nil || p.X == nil {
			return x
		}
		x = p.X
	}
}

// exprText is an expression's source text, normalised: every token's Text in
// order, with its trivia replaced by the spacing an operator needs.
//
// It is not pkg/dsl/print, which is ART-34's and reproduces a file. This is one
// line of a failure message, so a comment or a line break inside an expression
// must not reach it: `body.data.count` has to read as a path wherever the
// author happened to break the line.
func exprText(x ast.Expr) string {
	if x == nil {
		return ""
	}
	var b strings.Builder
	var prev token.Token
	for _, t := range x.Tokens(nil) {
		if t.Text == "" {
			continue // a zero-width token: an absent optional, an error marker
		}
		if b.Len() > 0 && !tightBefore[t.Kind] && !tightAfter[prev.Kind] {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
		prev = t
	}
	return b.String()
}

// tightBefore and tightAfter are the tokens that take no space on one side, so
// `body.data[0]` and `env("URL")` read as themselves while `a or b` and
// `status == 200` keep their spaces.
var (
	tightBefore = map[token.Kind]bool{
		token.Dot: true, token.LBracket: true, token.RBracket: true,
		token.LParen: true, token.RParen: true, token.Comma: true,
		token.Colon: true, token.StringMid: true, token.StringEnd: true,
	}
	tightAfter = map[token.Kind]bool{
		token.Dot: true, token.LBracket: true, token.LParen: true,
		token.Minus: true, token.StringStart: true, token.StringMid: true,
	}
)

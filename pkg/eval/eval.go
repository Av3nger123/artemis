// Package eval evaluates a checked .art expression against a step's live
// observation plus the scenario's scope.
//
// It is the run-time half of the DSL's assertion story, and it subsumes two
// things the YAML front end did by hand: pkg/shared/assert's operator table --
// `operator:`, `value:` and `type:` spelled out as a record -- and
// pkg/shared/template.go's renderValue, which decided what a captured number
// looks like inside a URL. Neither is deleted yet; ART-40 does that once ART-38
// has proven parity on the goldens.
//
// # Three kinds of answer
//
// An assertion has three outcomes, not two, and keeping them apart is the
// point of this package:
//
//	expect status == 200           // pass, or fail: the question was asked
//	expect body.data.count > 0     // errored: body has no data, so there is
//	                               // no number to compare and no honest false
//
// An absent path, `>` against an object, `contains` against a number: each is
// an *Error carrying a reason, which pkg/report prints in place of a
// comparison. This is pkg/shared/assert's taxonomy unchanged -- a check that
// could not be made is never a silent false -- and it is not a compile error,
// because a response's shape is a run-time fact.
//
// # Operands are retained
//
// A reader of a failed run wants `body.data.count > 0, got 0`, which needs the
// subject's source text, the operator and *both* evaluated operands. So Assert
// does not return a boolean: it returns an Outcome holding what it compared.
// See assert.go.
//
// # What it does not do
//
// No scope policing: pkg/dsl/check resolved every name against the step type's
// roots before this ran, so what resolves here is what the Env holds. No
// waiting: Assert is pure, and a `within` budget is a loop around it. No
// panics on any tree the parser can produce -- a nil Expr and an *ast.Bad are
// both reasons, not crashes, because the parser recovers and hands on a
// half-built tree.
package eval

import (
	"regexp"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// arity is how many arguments each builtin takes, which pkg/dsl/check enforces
// at compile time. It is repeated here because Eval must be total: a tree built
// in Go, or decoded from JSON by a UI, reaches this package without having been
// checked.
var arity = map[string]int{
	"env":     1,
	"match":   2,
	"text":    1,
	"value":   1,
	"attr":    2,
	"count":   1,
	"visible": 1,
}

// maxGroups is how many capturing groups a match() pattern may have, which is
// pkg/dsl/check's maxGroups. The checker counts a literal pattern and reports
// it as a compile error; this counts whatever the pattern turned out to be,
// which is the only count available when it came out of a var.
const maxGroups = 1

// Eval evaluates x and returns its value, or an *Error saying why it could not.
//
// The value is in the JSON domain: nil, bool, float64, string, []any,
// map[string]any, plus Headers for an api step's headers and Regexp for a regex
// literal.
func Eval(x ast.Expr, env *Env) (any, error) {
	switch x := x.(type) {
	case nil:
		return nil, errorf(token.Span{}, "there is no expression to evaluate")

	case *ast.Bad:
		return nil, errorf(x.Span(), "this expression did not parse")

	case *ast.Literal:
		return evalLiteral(x)

	case *ast.Interp:
		return evalInterp(x, env)

	case *ast.Ident:
		return evalIdent(x, env)

	case *ast.Paren:
		return Eval(x.X, env)

	case *ast.Unary:
		return evalUnary(x, env)

	case *ast.Binary:
		return evalBinary(x, env)

	case *ast.Exists:
		return evalExists(x, env)

	case *ast.IsType:
		return evalIsType(x, env)

	case *ast.Member:
		return evalMember(x, env)

	case *ast.Index:
		return evalIndex(x, env)

	case *ast.Call:
		return evalCall(x, env)

	case *ast.Object:
		return evalObject(x, env)

	case *ast.Array:
		return evalArray(x, env)
	}
	return nil, errorf(x.Span(), "cannot evaluate %s", exprText(x))
}

// evalLiteral decodes a literal. A number is parsed here rather than by the
// parser, which is shape-driven, and a regex is compiled here -- the checker
// already reported one that does not compile, so this branch is only reached by
// a tree that skipped it.
func evalLiteral(l *ast.Literal) (any, error) {
	switch l.Kind() {
	case token.Number:
		f, err := strconv.ParseFloat(l.Tok.Value, 64)
		if err != nil {
			return nil, errorf(l.Span(), "%s is not a number this build can read", l.Tok.Text)
		}
		return f, nil
	case token.String:
		return l.Tok.Value, nil
	case token.Bool:
		return l.Tok.Value == "true", nil
	case token.Null:
		return nil, nil
	case token.Regex:
		re, err := regexp.Compile(l.Tok.Value)
		if err != nil {
			return nil, errorf(l.Span(), "regex does not compile: %v", err)
		}
		return Regexp{re}, nil
	}
	return nil, errorf(l.Span(), "cannot evaluate %s", l.Tok.Text)
}

// evalInterp renders an interpolated string.
//
// Each delimiter token carries the decoded literal text of its own piece --
// `"a${` holds "a", `}b${` holds "b" -- so the string is the pieces and the
// rendered expressions between them, in order. Rendering is Render, which is
// ART-6's renderValue rules: a number as written, an object as compact JSON.
func evalInterp(i *ast.Interp, env *Env) (any, error) {
	var b strings.Builder
	for _, s := range i.Segments {
		b.WriteString(s.Delim.Value)
		v, err := Eval(s.Expr, env)
		if err != nil {
			return nil, err
		}
		b.WriteString(Render(v))
	}
	b.WriteString(i.End.Value)
	return b.String(), nil
}

// evalIdent resolves a name against the Env.
func evalIdent(x *ast.Ident, env *Env) (any, error) {
	name := x.Name()
	if v, ok := env.lookup(name); ok {
		return v, nil
	}
	if _, isFn := arity[name]; isFn {
		return nil, errorf(x.Span(), "%s is a function, not a value", name)
	}
	return nil, errorf(x.Span(), "unknown name %s", name)
}

// evalUnary is `not x` and `-x`.
//
// `not` takes a boolean and nothing else. There is no truthiness in this
// language: `not body.items` is a mistake worth a reason, not a test for a
// non-empty list.
func evalUnary(u *ast.Unary, env *Env) (any, error) {
	v, err := Eval(u.X, env)
	if err != nil {
		return nil, err
	}
	if u.Op.Kind == token.Minus {
		f, ok := number(v)
		if !ok {
			return nil, errorf(u.Span(), "- needs a number at %s, got %s", exprText(u.X), typeOf(v))
		}
		return -f, nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil, errorf(u.Span(), "not needs a boolean at %s, got %s", exprText(u.X), typeOf(v))
	}
	return !b, nil
}

// evalBinary is a comparison or a logical operator.
//
// `and` and `or` short-circuit, so `false and body.missing.x == 1` is false
// rather than an error -- which is also what the Python and JavaScript targets
// will do, so ART-48 lowers mechanically.
func evalBinary(b *ast.Binary, env *Env) (any, error) {
	op := operatorOf(b.Op)
	if op == "and" || op == "or" {
		return evalLogical(b, op, env)
	}

	left, err := Eval(b.X, env)
	if err != nil {
		return nil, err
	}
	right, err := Eval(b.Y, env)
	if err != nil {
		return nil, err
	}
	return compare(b, op, left, right)
}

func evalLogical(b *ast.Binary, op string, env *Env) (any, error) {
	left, err := logicalOperand(b.X, op, env)
	if err != nil {
		return nil, err
	}
	if (op == "and" && !left) || (op == "or" && left) {
		return left, nil
	}
	return logicalOperand(b.Y, op, env)
}

func logicalOperand(x ast.Expr, op string, env *Env) (bool, error) {
	v, err := Eval(x, env)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, errorf(x.Span(), "%s needs a boolean at %s, got %s", op, exprText(x), typeOf(v))
	}
	return b, nil
}

// compare applies one comparison operator to two evaluated values.
//
// The two shapes of failure are kept apart here: a comparison that can be made
// and does not hold is false, and one that cannot be made at all is an *Error
// naming which side was wrong and what it was instead. "Is an object greater
// than 3" has no false answer worth printing.
func compare(b *ast.Binary, op string, left, right any) (any, error) {
	if op != "matches" {
		if err := rejectRegex(b, left, right); err != nil {
			return nil, err
		}
	}
	switch op {
	case "==":
		return equal(left, right), nil
	case "!=":
		return !equal(left, right), nil
	case "<", "<=", ">", ">=":
		return compareOrder(b, op, left, right)
	case "contains":
		return contains(b, left, right)
	case "matches":
		return matches(b, left, right)
	}
	return nil, errorf(b.Op.Span, "unknown operator %s", op)
}

func compareOrder(b *ast.Binary, op string, left, right any) (any, error) {
	l, ok := number(left)
	if !ok {
		return nil, errorf(b.Span(), "%s needs a number at %s, got %s", op, exprText(b.X), typeOf(left))
	}
	r, ok := number(right)
	if !ok {
		return nil, errorf(b.Span(), "%s needs a number at %s, got %s", op, exprText(b.Y), typeOf(right))
	}
	switch op {
	case "<":
		return l < r, nil
	case "<=":
		return l <= r, nil
	case ">":
		return l > r, nil
	default:
		return l >= r, nil
	}
}

// contains tests membership: a substring of a string, an element of an array,
// or a key of an object. The three readings come from the left operand's type,
// which is pkg/shared/assert's rule unchanged.
func contains(b *ast.Binary, left, right any) (any, error) {
	switch l := left.(type) {
	case string:
		want, ok := text(right)
		if !ok {
			return nil, errorf(b.Span(), "contains against a string needs a string at %s, got %s", exprText(b.Y), typeOf(right))
		}
		return strings.Contains(l, want), nil

	case []any:
		for _, el := range l {
			if equal(right, el) {
				return true, nil
			}
		}
		return false, nil

	case map[string]any:
		key, ok := right.(string)
		if !ok {
			return nil, errorf(b.Span(), "contains against an object needs a key name at %s, got %s", exprText(b.Y), typeOf(right))
		}
		_, found := l[key]
		return found, nil

	case Headers:
		key, ok := right.(string)
		if !ok {
			return nil, errorf(b.Span(), "contains against an object needs a key name at %s, got %s", exprText(b.Y), typeOf(right))
		}
		_, found := l.lookup(key)
		return found, nil

	default:
		return nil, errorf(b.Span(), "contains needs a string, array or object at %s, got %s", exprText(b.X), typeOf(left))
	}
}

// matches matches the left side, rendered as a string, against a regex. The
// pattern is unanchored, as a regex usually is, and may be written as a regex
// literal or as a string: `matches /.+@.+/` and `matches pattern` are both
// legal, the second being how a var holds one.
func matches(b *ast.Binary, left, right any) (any, error) {
	re, err := asRegexp(b, right)
	if err != nil {
		return nil, err
	}
	got, ok := text(left)
	if !ok {
		return nil, errorf(b.Span(), "matches needs a string at %s, got %s", exprText(b.X), typeOf(left))
	}
	return re.MatchString(got), nil
}

func asRegexp(b *ast.Binary, right any) (*regexp.Regexp, error) {
	switch r := right.(type) {
	case Regexp:
		return r.Regexp, nil
	case string:
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, errorf(b.Y.Span(), "bad regular expression %q: %v", r, err)
		}
		return re, nil
	default:
		return nil, errorf(b.Span(), "matches needs a regular expression at %s, got %s", exprText(b.Y), typeOf(right))
	}
}

// rejectRegex stops a regex value being used as an ordinary one. It is a value
// only so that `matches` can take it; comparing it, or ordering it, is a
// mistake with no honest answer.
func rejectRegex(b *ast.Binary, left, right any) error {
	if _, ok := left.(Regexp); ok {
		return errorf(b.Span(), "a regex belongs on the right of matches, not at %s", exprText(b.X))
	}
	if _, ok := right.(Regexp); ok {
		return errorf(b.Span(), "a regex belongs on the right of matches, not at %s", exprText(b.Y))
	}
	return nil
}

// evalExists is the one operator that answers about an absent path rather than
// reporting it.
//
// A path that did not resolve is false, and so is one that resolved to JSON
// null: pkg/shared/assert's checkExists treats a null as absent and a scenario
// written against that behaviour must keep working. Any *other* failure -- a
// field of a number, a selector with no page -- is still reported, because
// "that could not be evaluated" is not the same as "that is not there".
func evalExists(e *ast.Exists, env *Env) (any, error) {
	v, err := Eval(e.X, env)
	if IsAbsent(err) {
		return false, nil
	}
	if err != nil {
		return nil, err
	}
	return v != nil, nil
}

// evalIsType compares a value's JSON type against the name `is` asks for.
func evalIsType(i *ast.IsType, env *Env) (any, error) {
	want := i.Type.Value
	if !isTypeName(want) {
		return nil, errorf(i.Type.Span, "unknown type name %s", want)
	}
	v, err := Eval(i.X, env)
	if err != nil {
		return nil, err
	}
	return typeOf(v) == want, nil
}

// evalMember is `a.b`.
//
// A member of an object is its value, or absent. A member of null is absent
// too, so `expect body.data.x exists` against `"data": null` is a clean false
// rather than a reason. A member of a number, a string, a boolean or an array
// is an error: reading one is a mistake, not a missing value.
func evalMember(m *ast.Member, env *Env) (any, error) {
	recv, err := Eval(m.X, env)
	if err != nil {
		return nil, err
	}
	name := m.Name.Value
	if name == "" {
		name = m.Name.Text
	}
	return field(m.Span(), recv, name, exprText(m.X), exprText(m))
}

// field reads name out of recv. where is the receiver's source text, for a
// reason that says which half was wrong, and whole is the whole path's, for the
// absent reason a reader goes and fixes.
func field(span token.Span, recv any, name, where, whole string) (any, error) {
	switch r := recv.(type) {
	case nil:
		return nil, absentf(span, "%s did not resolve", whole)
	case map[string]any:
		if v, ok := r[name]; ok {
			return v, nil
		}
		return nil, absentf(span, "%s did not resolve", whole)
	case Headers:
		if v, ok := r.lookup(name); ok {
			return v, nil
		}
		return nil, absentf(span, "%s did not resolve", whole)
	default:
		return nil, errorf(span, "cannot read field %s of %s at %s", name, article(typeOf(recv)), where)
	}
}

// evalIndex is `a[i]`.
//
// Indexing does not coerce. An array wants an integral number and an object
// wants a string, because an index of the wrong shape is a mistake in the
// expression rather than a value to be read loosely. An index outside an
// array, or a key an object does not hold, is absent -- so `exists` answers
// about it.
func evalIndex(i *ast.Index, env *Env) (any, error) {
	recv, err := Eval(i.X, env)
	if err != nil {
		return nil, err
	}
	idx, err := Eval(i.Index, env)
	if err != nil {
		return nil, err
	}

	switch r := recv.(type) {
	case nil:
		return nil, absentf(i.Span(), "%s did not resolve", exprText(i))

	case []any:
		// A number, and nothing a number can be read out of: "1" is a string
		// an author wrote where they meant 1.
		n, ok := idx.(float64)
		if !ok && typeOf(idx) == "number" {
			n, ok = number(idx)
		}
		if !ok {
			return nil, errorf(i.Span(), "an array index must be a number, got %s at %s", typeOf(idx), exprText(i.Index))
		}
		if n != float64(int(n)) {
			return nil, errorf(i.Span(), "an array index must be a whole number, got %s at %s", Render(n), exprText(i.Index))
		}
		at := int(n)
		if at < 0 || at >= len(r) {
			return nil, absentf(i.Span(), "%s did not resolve", exprText(i))
		}
		return r[at], nil

	case map[string]any, Headers:
		key, ok := idx.(string)
		if !ok {
			return nil, errorf(i.Span(), "an object key must be a string, got %s at %s", typeOf(idx), exprText(i.Index))
		}
		return field(i.Span(), recv, key, exprText(i.X), exprText(i))

	default:
		return nil, errorf(i.Span(), "cannot index %s at %s", article(typeOf(recv)), exprText(i.X))
	}
}

// evalCall calls a builtin: env() and match() everywhere, and the element
// functions in a browser step.
//
// match() is handled before the argument loop because it is the one builtin
// whose arguments are not all strings: its second is a regex. Everything else
// takes string arguments -- a variable name, a selector, an attribute name --
// and a value of another type there is a mistake in the expression.
func evalCall(c *ast.Call, env *Env) (any, error) {
	if c.Callee == nil {
		return nil, errorf(c.Span(), "this call has no function name")
	}
	name := c.Callee.Name()
	want, known := arity[name]
	if !known {
		return nil, errorf(c.Span(), "unknown function %s", name)
	}
	if len(c.Args) != want {
		return nil, errorf(c.Span(), "%s() takes %d argument(s); this call has %d", name, want, len(c.Args))
	}

	if name == "match" {
		return evalMatch(c, env)
	}

	args := make([]string, 0, want)
	for _, a := range c.Args {
		v, err := Eval(a.Value, env)
		if err != nil {
			return nil, err
		}
		s, ok := v.(string)
		if !ok {
			return nil, errorf(a.Value.Span(), "%s()'s argument must be a string, got %s at %s", name, typeOf(v), exprText(a.Value))
		}
		args = append(args, s)
	}

	if name == "env" {
		return env.getenv(args[0]), nil
	}

	page := env.elements()
	if page == nil {
		return nil, errorf(c.Span(), "%s() needs a browser page, and this step has none", name)
	}
	switch name {
	case "text":
		return page.Text(args[0])
	case "value":
		return page.Value(args[0])
	case "attr":
		return page.Attr(args[0], args[1])
	case "count":
		return page.Count(args[0])
	default:
		return page.Visible(args[0])
	}
}

// evalMatch is `match(<text>, <pattern>)`: the regex extraction a capture off
// a body that is not JSON needs.
//
// It is the DSL's spelling of what pkg/shared/capture.readRegex does for a
// YAML `{regex: ...}` capture, with one rule tightened. The whole of the
// contract, which SPEC.md states and ART-48 and ART-50 port:
//
//   - the value is capturing group 1 when the pattern has one and the whole
//     match when it has none, so `id=([0-9]+)` needs no second argument
//     saying which part was wanted;
//   - two or more capturing groups is a mistake, not a choice. readRegex
//     takes the first silently; here it is an error, because a pattern with
//     two groups has an author who meant one of them. The checker catches a
//     literal pattern; this catches one that came out of a var;
//   - the value is always a **string**: the text that matched, not a guess at
//     what it meant, because reading "007" as seven loses data;
//   - a pattern that matches nothing is an *Error, not an empty string. It is
//     not absent -- the text was there and the question was asked -- so a
//     capture records one errored assertion and `exists` reports the reason
//     rather than answering false.
//
// The pattern may be a regex literal or a string, through the same asRegexp
// that `matches` uses, so a pattern can live in a var.
func evalMatch(c *ast.Call, env *Env) (any, error) {
	subject, err := Eval(c.Args[0].Value, env)
	if err != nil {
		return nil, err
	}
	in, ok := text(subject)
	if !ok {
		return nil, errorf(c.Args[0].Value.Span(),
			"match()'s first argument must be a string, got %s at %s",
			typeOf(subject), exprText(c.Args[0].Value))
	}

	pattern, err := Eval(c.Args[1].Value, env)
	if err != nil {
		return nil, err
	}
	re, err := matchPattern(c.Args[1].Value, pattern)
	if err != nil {
		return nil, err
	}
	if n := re.NumSubexp(); n > maxGroups {
		return nil, errorf(c.Args[1].Value.Span(),
			"match() reads one capturing group, and regex %q has %d", re.String(), n)
	}

	m := re.FindStringSubmatch(in)
	if m == nil {
		return nil, errorf(c.Span(), "regex %q matched nothing in %s", re.String(), exprText(c.Args[0].Value))
	}
	if re.NumSubexp() > 0 {
		return m[1], nil
	}
	return m[0], nil
}

// matchPattern is asRegexp for a call rather than for a binary operator: the
// same two accepted forms, with the spans and the wording a call's argument
// wants.
func matchPattern(x ast.Expr, v any) (*regexp.Regexp, error) {
	switch p := v.(type) {
	case Regexp:
		return p.Regexp, nil
	case string:
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, errorf(x.Span(), "bad regular expression %q: %v", p, err)
		}
		return re, nil
	default:
		return nil, errorf(x.Span(),
			"match()'s second argument must be a regular expression, got %s at %s",
			typeOf(v), exprText(x))
	}
}

// evalObject builds an object literal, which is what a request body is before
// the lowerer serialises it.
func evalObject(o *ast.Object, env *Env) (any, error) {
	out := make(map[string]any, len(o.Entries))
	for _, e := range o.Entries {
		k, err := Eval(e.Key, env)
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, errorf(e.Key.Span(), "an object key must be a string, got %s at %s", typeOf(k), exprText(e.Key))
		}
		v, err := Eval(e.Value, env)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

func evalArray(a *ast.Array, env *Env) (any, error) {
	out := make([]any, 0, len(a.Elems))
	for _, e := range a.Elems {
		v, err := Eval(e.Value, env)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// operatorOf is the operator an infix token spells. The word operators arrive
// as identifiers, because this grammar has no lexical keywords, so the operator
// is the token's text either way.
func operatorOf(t token.Token) string {
	if t.Kind == token.Ident {
		return t.Value
	}
	return t.Text
}

// article prefixes a type name for a message: "a number", "an object".
func article(typeName string) string {
	switch typeName {
	case "null":
		return "null"
	case "object", "array":
		return "an " + typeName
	default:
		return "a " + typeName
	}
}

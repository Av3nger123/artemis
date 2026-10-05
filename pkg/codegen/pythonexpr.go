package codegen

import (
	"fmt"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// ast.Expr to Python, and the only place the interpreter and the transpiler
// diverge.
//
// The switch below is pkg/eval's switch in the same order, so the two can be
// read side by side and a construct one handles and the other does not is
// visible rather than inferred. Where Python has the operator, Python's operator
// is emitted; where it does not -- `contains`, `matches`, `exists`, `is`,
// `match()` -- one of the helpers in helpers.go is, and the helper is written to
// pkg/eval's rules.
//
// The accepted divergence, stated in every generated file's header: artemis
// coerces across types on `==` and the ordering operators and Python does not
// (`status == "200"` holds against a JSON 200 under the interpreter), and
// Python's `True == 1` holds where the interpreter's does not. Emitting a helper
// for `==` would close the gap and cost pytest's assertion rewriting, which
// turns `assert status == 200` into "assert 404 == 200" and a helper call into
// "assert False". The export is for people to read; the interpreter stays the
// authority.

// Binding powers, loosest first, mirroring parser/expr.go's level order. Python
// has the same order for the same operators, which is what makes a
// precedence-aware printer safe rather than a parenthesis around everything.
//
// precAtom is where a call, a literal, a subscript and an author's own
// parentheses sit: never wrapped, because nothing can split them.
const (
	precOr = iota + 1
	precAnd
	precNot
	precCmp
	precUnary
	precAtom
)

// expr emits x at a position that binds at least min, parenthesising when x
// binds looser than that.
func (f *pyFile) expr(x ast.Expr, min int) (string, error) {
	src, prec, err := f.exprPrec(x)
	if err != nil {
		return "", err
	}
	if prec < min {
		return "(" + src + ")", nil
	}
	return src, nil
}

// exprPrec emits x and reports how tightly the result binds.
func (f *pyFile) exprPrec(x ast.Expr) (string, int, error) {
	switch x := x.(type) {
	case nil:
		return "", 0, fmt.Errorf("there is no expression here")

	case *ast.Bad:
		return "", 0, fmt.Errorf("line %d: this expression did not parse", x.Span().Line)

	case *ast.Literal:
		s, err := f.literal(x)
		return s, precAtom, err

	case *ast.Interp:
		s, err := f.interp(x)
		return s, precAtom, err

	case *ast.Ident:
		return reference(x.Name(), f.step), precAtom, nil

	case *ast.Paren:
		// Kept rather than folded: `(a or b) and c` is a different expression
		// from `a or b and c`, and the parentheses are source the author wrote.
		inner, err := f.expr(x.X, precOr)
		return "(" + inner + ")", precAtom, err

	case *ast.Unary:
		return f.unary(x)

	case *ast.Binary:
		return f.binary(x)

	case *ast.Exists:
		s, err := f.exists(x)
		return s, precAtom, err

	case *ast.IsType:
		s, err := f.isType(x)
		return s, precAtom, err

	case *ast.Member:
		s, err := f.member(x)
		return s, precAtom, err

	case *ast.Index:
		s, err := f.index(x)
		return s, precAtom, err

	case *ast.Call:
		s, err := f.call(x)
		return s, precAtom, err

	case *ast.Object:
		s, err := f.object(x)
		return s, precAtom, err

	case *ast.Array:
		s, err := f.array(x)
		return s, precAtom, err
	}
	return "", 0, fmt.Errorf("line %d: cannot export %s", x.Span().Line, ast.Source(x))
}

// literal emits a number, string, regex, boolean or null.
//
// A number is emitted as the author wrote it. The evaluator reads every number
// as a float64 and Python would read `200` as an int, which is a difference with
// no observable consequence: `resp.status_code` is an int and `200 == 200.0` is
// true in Python, so the readable spelling is also the correct one.
func (f *pyFile) literal(l *ast.Literal) (string, error) {
	switch l.Kind() {
	case token.Number:
		return l.Tok.Value, nil
	case token.String:
		return quote(l.Tok.Value), nil
	case token.Bool:
		if l.Tok.Value == "true" {
			return "True", nil
		}
		return "False", nil
	case token.Null:
		return "None", nil
	case token.Regex:
		return pattern(l.Tok.Value, l.Span().Line)
	}
	return "", fmt.Errorf("line %d: cannot export the literal %s", l.Span().Line, l.Tok.Text)
}

// interp emits an interpolated string.
//
// An f-string when every interpolated piece is safe to put between braces, and
// concatenation when one is not. Python before 3.12 allows neither a quote of
// the enclosing kind nor a backslash inside an f-string's braces, so
// `f"{art_render(body["a"])}"` is a syntax error on the Pythons people run. The
// f-string is worth the second spelling because `f"{art_render(base)}/orders"`
// is what a URL looks like and `art_render(base) + "/orders"` is not.
//
// Every piece goes through art_render whichever spelling is used, because that
// is eval.Render: a number renders as written rather than as 42.0, and an object
// as compact JSON -- so a URL built here and a URL built by `artemis run` are
// the same string.
func (f *pyFile) interp(i *ast.Interp) (string, error) {
	parts := make([]string, 0, len(i.Segments))
	safe := true
	for _, s := range i.Segments {
		piece, err := f.expr(s.Expr, precOr)
		if err != nil {
			return "", err
		}
		piece = f.use(helperRender) + "(" + piece + ")"
		if strings.ContainsAny(piece, "\"\\{}#") {
			safe = false
		}
		parts = append(parts, piece)
	}

	if safe {
		var b strings.Builder
		b.WriteString("f\"")
		for n, s := range i.Segments {
			b.WriteString(escapeF(s.Delim.Value))
			b.WriteString("{")
			b.WriteString(parts[n])
			b.WriteString("}")
		}
		b.WriteString(escapeF(i.End.Value))
		b.WriteString("\"")
		return b.String(), nil
	}

	// Concatenation, with the literal pieces that are empty left out: a string
	// that begins with its interpolation must not begin with `"" + `.
	var terms []string
	for n, s := range i.Segments {
		if s.Delim.Value != "" {
			terms = append(terms, quote(s.Delim.Value))
		}
		terms = append(terms, parts[n])
	}
	if i.End.Value != "" {
		terms = append(terms, quote(i.End.Value))
	}
	return strings.Join(terms, " + "), nil
}

// unary is `not x` and `-x`.
func (f *pyFile) unary(u *ast.Unary) (string, int, error) {
	if u.Op.Kind == token.Minus {
		x, err := f.expr(u.X, precUnary)
		return "-" + x, precUnary, err
	}
	// `not` sits above a comparison and below `and`, in this grammar and in
	// Python, so `not body.x exists` and `not a and b` both survive unwrapped.
	x, err := f.expr(u.X, precNot)
	return "not " + x, precNot, err
}

// binary is a comparison or a logical operator.
func (f *pyFile) binary(b *ast.Binary) (string, int, error) {
	op := operator(b.Op)
	switch op {
	case "and", "or":
		// Left-associative, and short-circuiting in both languages, so
		// `false and body.missing.x == 1` is False here too rather than a
		// KeyError.
		prec := precAnd
		if op == "or" {
			prec = precOr
		}
		left, err := f.expr(b.X, prec)
		if err != nil {
			return "", 0, err
		}
		right, err := f.expr(b.Y, prec+1)
		if err != nil {
			return "", 0, err
		}
		return left + " " + op + " " + right, prec, nil

	case "contains":
		// Substring, element or key, off the left operand's type: one helper
		// rather than three spellings a reader would have to tell apart.
		return f.helper2(helperContains, b.X, b.Y)

	case "matches":
		left, err := f.expr(b.X, precOr)
		if err != nil {
			return "", 0, err
		}
		right, err := f.patternArg(b.Y)
		if err != nil {
			return "", 0, err
		}
		return f.use(helperMatches) + "(" + left + ", " + right + ")", precAtom, nil

	case "==", "!=", "<", "<=", ">", ">=":
		// Non-associative in the grammar, so neither side is ever a comparison
		// and Python's chaining cannot be reached.
		left, err := f.expr(b.X, precUnary)
		if err != nil {
			return "", 0, err
		}
		right, err := f.expr(b.Y, precUnary)
		if err != nil {
			return "", 0, err
		}
		return left + " " + op + " " + right, precCmp, nil
	}
	return "", 0, fmt.Errorf("line %d: cannot export the operator %s", b.Op.Span.Line, op)
}

// helper2 emits a two-argument helper call over both operands.
func (f *pyFile) helper2(name string, x, y ast.Expr) (string, int, error) {
	left, err := f.expr(x, precOr)
	if err != nil {
		return "", 0, err
	}
	right, err := f.expr(y, precOr)
	if err != nil {
		return "", 0, err
	}
	return f.use(name) + "(" + left + ", " + right + ")", precAtom, nil
}

// exists is `body.x exists`, the one operator that answers about an absent path
// rather than reporting it.
//
// The operand goes in a lambda because that is the whole mechanism: a member
// read emits a subscript, so an absent path raises KeyError, IndexError or
// TypeError, and art_exists is what turns those three into False. Evaluating the
// operand eagerly would raise before the helper could see it.
func (f *pyFile) exists(e *ast.Exists) (string, error) {
	x, err := f.expr(e.X, precOr)
	if err != nil {
		return "", err
	}
	return f.use(helperExists) + "(lambda: " + x + ")", nil
}

// isType is `body.count is number`, over the JSON type names.
func (f *pyFile) isType(i *ast.IsType) (string, error) {
	x, err := f.expr(i.X, precOr)
	if err != nil {
		return "", err
	}
	if !token.IsTypeName(i.Type.Value) {
		return "", fmt.Errorf("line %d: %q is not a type name", i.Type.Span.Line, i.Type.Value)
	}
	return f.use(helperIs) + "(" + x + ", " + quote(i.Type.Value) + ")", nil
}

// member is `body.data`, and `page.url` and `page.title`.
//
// A member of a response body is a subscript, so Python's own KeyError is the
// DSL's "body.data did not resolve" -- which pytest reports as an error on the
// assertion, which is what an errored assertion is. The `page` root is the
// exception: its member set is closed and each member is a Playwright call, so
// `page.url` is the page's url property and `page.title` is a method.
func (f *pyFile) member(m *ast.Member) (string, error) {
	name := m.Name.Value
	if name == "" {
		name = m.Name.Text
	}
	if id, ok := m.X.(*ast.Ident); ok && id.Name() == "page" && reference("page", f.step) == "page" {
		switch name {
		case "url":
			return "page.url", nil
		case "title":
			return "page.title()", nil
		default:
			return "", fmt.Errorf("line %d: page has no member %q", m.Span().Line, name)
		}
	}
	x, err := f.expr(m.X, precAtom)
	if err != nil {
		return "", err
	}
	return x + "[" + quote(name) + "]", nil
}

// index is `body.items[0]` and `headers["content-type"]`.
//
// `headers` is requests' CaseInsensitiveDict, so the case-insensitive lookup
// eval.Headers does is the library's behaviour here and needs no helper.
func (f *pyFile) index(i *ast.Index) (string, error) {
	x, err := f.expr(i.X, precAtom)
	if err != nil {
		return "", err
	}
	idx, err := f.expr(i.Index, precOr)
	if err != nil {
		return "", err
	}
	return x + "[" + idx + "]", nil
}

// call emits a builtin.
//
// env() and match() are available everywhere; the five element functions need a
// page and the checker has already refused them in a step that has none.
// count() and visible() are Playwright calls directly -- a locator count does
// not wait and is_visible is a non-waiting predicate, which is exactly what
// SPEC.md says those two do -- while text(), value() and attr() need the
// query-then-handle read that makes "nothing matched" null instead of a
// timeout.
func (f *pyFile) call(c *ast.Call) (string, error) {
	if c.Callee == nil {
		return "", fmt.Errorf("line %d: this call has no function name", c.Span().Line)
	}
	name := c.Callee.Name()
	args, err := f.args(c)
	if err != nil {
		return "", err
	}
	if ok, min, max := arity(name, len(args)); !ok {
		if _, known := builtinArity[name]; !known {
			return "", fmt.Errorf("line %d: %s() is not a function artemis exports", c.Span().Line, name)
		}
		if min == max {
			return "", fmt.Errorf("line %d: %s() takes %d argument(s); this call has %d",
				c.Span().Line, name, max, len(args))
		}
		return "", fmt.Errorf("line %d: %s() takes %d to %d argument(s); this call has %d",
			c.Span().Line, name, min, max, len(args))
	}

	switch name {
	case "env":
		// art_env rather than os.environ.get(name, ""): ART-25 made an absent,
		// empty or blank variable an error in the interpreter, and the three
		// targets have to agree about what env() means.
		if len(args) == 2 {
			return f.use(helperEnv) + "(" + args[0] + ", " + args[1] + ")", nil
		}
		return f.use(helperEnv) + "(" + args[0] + ")", nil
	case "match":
		p, err := f.patternArg(c.Args[1].Value)
		if err != nil {
			return "", err
		}
		return f.use(helperMatch) + "(" + args[0] + ", " + p + ")", nil
	case "text":
		return f.use(helperText) + "(page, " + args[0] + ")", nil
	case "value":
		return f.use(helperValue) + "(page, " + args[0] + ")", nil
	case "attr":
		return f.use(helperAttr) + "(page, " + args[0] + ", " + args[1] + ")", nil
	case "count":
		return "page.locator(" + args[0] + ").count()", nil
	case "visible":
		return "page.is_visible(" + args[0] + ")", nil
	}
	return "", fmt.Errorf("line %d: %s() is not a function artemis exports", c.Span().Line, name)
}

// builtinArity is token.Builtins with the number of arguments each takes. It is
// here and checked against pkg/eval's own table in the tests, so a builtin added
// to the language without a Python spelling fails this package's tests rather
// than emitting a call to something that does not exist.
//
// This is the maximum a builtin takes; minBuiltinArity below is the minimum,
// for env() alone. Both targets share one table because both switches are
// guarded by arity() before they are reached.
var builtinArity = map[string]int{
	"env": 2, "match": 2, "text": 1, "value": 1, "attr": 2, "count": 1, "visible": 1,
}

// minBuiltinArity is how few arguments a builtin accepts, for the builtins that
// take a range -- today only env(). A name absent here takes exactly
// builtinArity[name]; this mirrors pkg/dsl/check/scope.go's minArgs, which the
// checker already enforces before a target ever sees the call, so this is a
// second line of the same rule rather than a new one.
var minBuiltinArity = map[string]int{
	"env": 1,
}

// arity reports whether got is a count name's builtin accepts, and the bounds
// that decide the error message when it is not.
func arity(name string, got int) (ok bool, min, max int) {
	max, known := builtinArity[name]
	if !known {
		return false, 0, 0
	}
	min = max
	if m, ok := minBuiltinArity[name]; ok {
		min = m
	}
	return got >= min && got <= max, min, max
}

// args emits a call's arguments in order.
func (f *pyFile) args(c *ast.Call) ([]string, error) {
	out := make([]string, 0, len(c.Args))
	for _, a := range c.Args {
		s, err := f.expr(a.Value, precOr)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// patternArg emits a regex position, which takes a regex literal or a string --
// the rule `matches` and `match()` already follow, so a pattern can live in a
// var.
func (f *pyFile) patternArg(x ast.Expr) (string, error) {
	return f.expr(x, precOr)
}

// object emits an object literal. A key is an expression because `{"${k}": v}`
// parses, and a Python dict display accepts one in the same position.
func (f *pyFile) object(o *ast.Object) (string, error) {
	parts := make([]string, 0, len(o.Entries))
	for _, e := range o.Entries {
		k, err := f.expr(e.Key, precOr)
		if err != nil {
			return "", err
		}
		v, err := f.expr(e.Value, precOr)
		if err != nil {
			return "", err
		}
		parts = append(parts, k+": "+v)
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

// array emits an array literal.
func (f *pyFile) array(a *ast.Array) (string, error) {
	parts := make([]string, 0, len(a.Elems))
	for _, e := range a.Elems {
		v, err := f.expr(e.Value, precOr)
		if err != nil {
			return "", err
		}
		parts = append(parts, v)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

// operator is the operator an infix token spells. The word operators arrive as
// Ident tokens, because this grammar has no lexical keywords.
func operator(t token.Token) string {
	if t.Kind == token.Ident {
		return t.Value
	}
	return t.Text
}

// quote renders s as a Python string literal, double-quoted.
func quote(s string) string { return "\"" + escape(s) + "\"" }

// escapeF is escape for the literal text of an f-string, where a brace stands
// for an interpolation and a literal one has to be doubled.
func escapeF(s string) string {
	return strings.NewReplacer("{", "{{", "}", "}}").Replace(escape(s))
}

// escape renders s's contents for a double-quoted Python string literal.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pattern renders a regex literal's pattern as a Python regex string.
//
// A raw string where one is possible, because that is how a Python author writes
// a regex and `r"\d+"` is readable where `"\\d+"` is not. A pattern holding a
// double quote, or ending in a backslash, cannot be a double-quoted raw string
// -- so it falls back to an escaped one.
//
// Go's regexp is RE2 and Python's is not the same language. The two constructs
// that actually differ are refused here with the line, rather than emitted as a
// file that raises on import: a Unicode class (`\p{L}`), which RE2 has and
// Python does not, and an inline flag group anywhere but the start of the
// pattern, which Python 3.11 and later reject.
func pattern(p string, line int) (string, error) {
	if i := strings.Index(p, `\p{`); i >= 0 {
		return "", unicodeClassError(p, line, i)
	}
	if i := strings.Index(p, `\P{`); i >= 0 {
		return "", unicodeClassError(p, line, i)
	}
	if i := inlineFlags(p); i > 0 {
		return "", fmt.Errorf("line %d: the pattern /%s/ sets flags at offset %d; "+
			"python accepts an inline flag group only at the start of a pattern, and has no `U`",
			line, p, i-1)
	}
	if !strings.Contains(p, `"`) && !strings.HasSuffix(p, `\`) {
		return "r\"" + p + "\"", nil
	}
	return quote(p), nil
}

func unicodeClassError(p string, line, at int) error {
	return fmt.Errorf("line %d: the pattern /%s/ uses a unicode class at offset %d, "+
		"which go's regexp has and python's re does not; "+
		"write the characters out or use a character class instead", line, p, at)
}

// inlineFlags is the 1-based offset of an inline flag group Python will not
// accept -- one that is not at the start of the pattern, or one setting RE2's
// `U` -- and 0 for a pattern Python will accept.
//
// Only `(?letters)` counts -- the flag-setting form. `(?:...)`, `(?P<n>...)`,
// `(?=...)` and the rest are groups Python has too.
func inlineFlags(p string) int {
	for i := 0; i+2 < len(p); i++ {
		if p[i] != '(' || p[i+1] != '?' {
			continue
		}
		if i > 0 && p[i-1] == '\\' {
			continue
		}
		j := i + 2
		for j < len(p) && strings.IndexByte("imsUx-", p[j]) >= 0 {
			j++
		}
		if j <= i+2 || j >= len(p) || p[j] != ')' {
			continue
		}
		// Position 0 is the one place Python accepts a flag group -- except for
		// RE2's `U`, which Python has no spelling for at all.
		if i > 0 || strings.ContainsRune(p[i+2:j], 'U') {
			return i + 1
		}
	}
	return 0
}

// The native `within` shapes.
//
// `expect visible(".modal") within "5s"` has a Playwright spelling that waits on
// the budget itself -- expect(page.locator(".modal")).to_be_visible(timeout=5000)
// -- and that is the line the issue asks for: a reader of the generated test sees
// Playwright's own waiting assertion, not a poll loop around a predicate.
//
// Only the shapes where the Playwright matcher means *exactly* what the DSL
// expression means are mapped. to_have_text is deliberately not among them: it
// normalises whitespace, so `text(".x") == "a  b"` would hold under it and not
// under the interpreter, and a divergence nobody can see in the generated line is
// worse than a poll loop they can. Everything unmapped falls back to art_within,
// which is pkg/steps/browserstep's settle loop and is exactly faithful.
//
// Each mapping also requires the operand it compares against to be statically a
// string or a number, because a Playwright matcher takes a value and not a
// predicate: `value("#q") == body.want` is a comparison, and comparisons are what
// the fallback is for.

// native is the Playwright assertion for x with the budget ms, and whether there
// is one.
func (f *pyFile) native(x ast.Expr, ms string) (string, bool, error) {
	x = unparen(x)

	// `not visible(sel)`: hidden, which Playwright reads as "not visible or not
	// in the page" -- the same answer as `not visible(sel)`, whose own false
	// covers both.
	if u, ok := x.(*ast.Unary); ok && u.Op.Kind == token.Ident && u.Op.Value == "not" {
		if sel, ok := elementCall(unparen(u.X), "visible"); ok {
			return f.matcher(sel, "to_be_hidden", nil, ms)
		}
		return "", false, nil
	}
	if sel, ok := elementCall(x, "visible"); ok {
		return f.matcher(sel, "to_be_visible", nil, ms)
	}

	b, ok := x.(*ast.Binary)
	if !ok {
		return "", false, nil
	}
	left := unparen(b.X)
	switch operator(b.Op) {
	case "==":
		if sel, ok := elementCall(left, "value"); ok && staticString(b.Y) {
			return f.matcher(sel, "to_have_value", []ast.Expr{b.Y}, ms)
		}
		if sel, ok := elementCall(left, "count"); ok && numberLiteral(b.Y) {
			return f.matcher(sel, "to_have_count", []ast.Expr{b.Y}, ms)
		}
		if c, ok := left.(*ast.Call); ok && callee(c) == "attr" && len(c.Args) == 2 &&
			staticString(c.Args[1].Value) && staticString(b.Y) {
			return f.matcher(c.Args[0].Value, "to_have_attribute",
				[]ast.Expr{c.Args[1].Value, b.Y}, ms)
		}
		if member, ok := f.pageMember(left); ok && staticString(b.Y) {
			switch member {
			case "url":
				return f.pageMatcher("to_have_url", b.Y, ms)
			case "title":
				return f.pageMatcher("to_have_title", b.Y, ms)
			}
		}
	case "contains":
		if sel, ok := elementCall(left, "text"); ok && staticString(b.Y) {
			return f.matcher(sel, "to_contain_text", []ast.Expr{b.Y}, ms)
		}
	}
	return "", false, nil
}

// matcher is `expect(page.locator(sel)).<name>(args..., timeout=ms)`.
func (f *pyFile) matcher(sel ast.Expr, name string, args []ast.Expr, ms string) (string, bool, error) {
	f.need("playwright")
	target, err := f.value(sel)
	if err != nil {
		return "", false, err
	}
	parts := make([]string, 0, len(args)+1)
	for _, a := range args {
		src, err := f.expr(a, precOr)
		if err != nil {
			return "", false, err
		}
		parts = append(parts, src)
	}
	parts = append(parts, "timeout="+ms)
	return "expect(page.locator(" + target + "))." + name + "(" + strings.Join(parts, ", ") + ")", true, nil
}

// pageMatcher is `expect(page).<name>(value, timeout=ms)`, for the two members of
// the `page` root.
func (f *pyFile) pageMatcher(name string, value ast.Expr, ms string) (string, bool, error) {
	f.need("playwright")
	src, err := f.expr(value, precOr)
	if err != nil {
		return "", false, err
	}
	return "expect(page)." + name + "(" + src + ", timeout=" + ms + ")", true, nil
}

// pageMember is the member of the `page` root x reads, and whether x is one.
func (f *pyFile) pageMember(x ast.Expr) (string, bool) {
	m, ok := x.(*ast.Member)
	if !ok {
		return "", false
	}
	id, ok := m.X.(*ast.Ident)
	if !ok || id.Name() != "page" || reference("page", f.step) != "page" {
		return "", false
	}
	return m.Name.Value, true
}

// elementCall is the selector argument of a one-argument element function call,
// and whether x is one.
func elementCall(x ast.Expr, name string) (ast.Expr, bool) {
	c, ok := x.(*ast.Call)
	if !ok || callee(c) != name || len(c.Args) != 1 {
		return nil, false
	}
	return c.Args[0].Value, true
}

// callee is a call's function name, or "".
func callee(c *ast.Call) string {
	if c.Callee == nil {
		return ""
	}
	return c.Callee.Name()
}

// numberLiteral reports whether x is a number written in the file, which is what
// to_have_count takes.
func numberLiteral(x ast.Expr) bool {
	l, ok := x.(*ast.Literal)
	return ok && l.Kind() == token.Number
}

// unparen strips an author's parentheses, for the shape match only. The emitted
// expression still goes through ast.Paren, so `expect (visible(".x"))` keeps its
// parentheses when it falls back.
func unparen(x ast.Expr) ast.Expr {
	for {
		p, ok := x.(*ast.Paren)
		if !ok {
			return x
		}
		x = p.X
	}
}

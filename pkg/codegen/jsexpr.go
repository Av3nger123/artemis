package codegen

import (
	"fmt"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// ast.Expr to JavaScript, and the only place the interpreter and this target
// diverge.
//
// The switch below is pythonexpr.go's switch in the same order, which is
// pkg/eval's -- so the three can be read side by side and a construct one
// handles and another does not is visible rather than inferred. Where JavaScript
// has the operator, JavaScript's operator is emitted; where it does not --
// `contains`, `matches`, `exists`, `is`, `match()` -- one of the helpers in
// jshelpers.go is, and the helper is written to pkg/eval's rules.
//
// # The two things that are not Python's
//
// A path read is art_at and not JavaScript's own `.`: `body.missing` is
// undefined where Python's subscript raises, so the native spelling would make
// an unresolvable path pass here and error under both the interpreter and the
// generated pytest.
//
// An element read is a promise. So every emitted expression also reports whether
// it contains an `await`, which is what decides whether an enclosing arrow
// function -- art_exists' thunk, art_within's predicate -- has to be async. The
// `await` itself is emitted at the call, so a synchronous helper wrapping an
// asynchronous read reads as art_contains(await art_text(page, ".x"), "y").
//
// The accepted divergence, stated in every generated file's header: artemis
// coerces across types on `==` and JavaScript's `===` does not, so
// `status == "200"` does not hold against a JSON 200 here -- which is exactly
// what the Python export does, so the two backends agree with each other.

// Binding powers, loosest first, mirroring parser/expr.go's level order.
//
// Not quite JavaScript's own table, and the difference is load-bearing: the DSL
// puts `not` *below* a comparison -- `not status == 404` negates the comparison
// -- while JavaScript's `!` binds tighter than `===`. So `not` is emitted at the
// unary level and its operand is required to bind at least that tightly, which
// is what puts the parentheses in `!(status === 404)`.
//
// jsPrecAtom is where a call, a literal and an author's own parentheses sit:
// never wrapped, because nothing can split them.
const (
	jsPrecOr = iota + 1
	jsPrecAnd
	jsPrecCmp
	jsPrecUnary
	jsPrecAtom
)

// jsExpr is an emitted expression: its source, how tightly it binds, and
// whether it contains an await.
type jsExpr struct {
	src   string
	prec  int
	await bool
}

// expr emits x at a position that binds at least min, parenthesising when x
// binds looser than that.
func (f *jsFile) expr(x ast.Expr, min int) (jsExpr, error) {
	out, err := f.exprPrec(x)
	if err != nil {
		return jsExpr{}, err
	}
	if out.prec < min {
		return jsExpr{src: "(" + out.src + ")", prec: jsPrecAtom, await: out.await}, nil
	}
	return out, nil
}

// atom is an emitted atom, for the many cases that are one.
func atom(src string, await bool) jsExpr {
	return jsExpr{src: src, prec: jsPrecAtom, await: await}
}

// exprPrec emits x and reports how tightly the result binds.
func (f *jsFile) exprPrec(x ast.Expr) (jsExpr, error) {
	switch x := x.(type) {
	case nil:
		return jsExpr{}, fmt.Errorf("there is no expression here")

	case *ast.Bad:
		return jsExpr{}, fmt.Errorf("line %d: this expression did not parse", x.Span().Line)

	case *ast.Literal:
		src, err := f.literal(x)
		return atom(src, false), err

	case *ast.Interp:
		return f.interp(x)

	case *ast.Ident:
		return atom(jsReference(x.Name(), f.step), false), nil

	case *ast.Paren:
		// Kept rather than folded: `(a or b) and c` is a different expression
		// from `a or b and c`, and the parentheses are source the author wrote.
		inner, err := f.expr(x.X, jsPrecOr)
		return atom("("+inner.src+")", inner.await), err

	case *ast.Unary:
		return f.unary(x)

	case *ast.Binary:
		return f.binary(x)

	case *ast.Exists:
		return f.exists(x)

	case *ast.IsType:
		return f.isType(x)

	case *ast.Member, *ast.Index:
		return f.path(x)

	case *ast.Call:
		return f.call(x)

	case *ast.Object:
		return f.object(x)

	case *ast.Array:
		return f.array(x)
	}
	return jsExpr{}, fmt.Errorf("line %d: cannot export %s", x.Span().Line, ast.Source(x))
}

// literal emits a number, string, regex, boolean or null.
//
// A number is emitted as the author wrote it. The evaluator reads every number
// as a float64 and so does JavaScript, so there is nothing here to reconcile --
// which is one fewer difference than the Python export has.
func (f *jsFile) literal(l *ast.Literal) (string, error) {
	switch l.Kind() {
	case token.Number:
		return l.Tok.Value, nil
	case token.String:
		return jsQuote(l.Tok.Value), nil
	case token.Bool:
		return l.Tok.Value, nil
	case token.Null:
		return "null", nil
	case token.Regex:
		return jsPattern(l.Tok.Value, l.Span().Line)
	}
	return "", fmt.Errorf("line %d: cannot export the literal %s", l.Span().Line, l.Tok.Text)
}

// interp emits an interpolated string as a template literal.
//
// Always a template literal, with no concatenating fallback: a template literal
// may hold a quote and a backslash, which is what forced the Python export to
// have two spellings. `${art_render(base)}/orders` is what a URL looks like.
//
// Every piece goes through art_render, because that is eval.Render: a number
// renders as written rather than as 42.0 and an object as compact JSON, so a URL
// built here and a URL built by `artemis run` are the same string. Template
// literals interpolate with String(), which would render an object as
// [object Object].
func (f *jsFile) interp(i *ast.Interp) (jsExpr, error) {
	var b strings.Builder
	await := false
	b.WriteString("`")
	for _, s := range i.Segments {
		piece, err := f.expr(s.Expr, jsPrecOr)
		if err != nil {
			return jsExpr{}, err
		}
		await = await || piece.await
		b.WriteString(escapeTemplate(s.Delim.Value))
		b.WriteString("${")
		b.WriteString(f.use(jsHelperRender) + "(" + piece.src + ")")
		b.WriteString("}")
	}
	b.WriteString(escapeTemplate(i.End.Value))
	b.WriteString("`")
	return atom(b.String(), await), nil
}

// unary is `not x` and `-x`.
func (f *jsFile) unary(u *ast.Unary) (jsExpr, error) {
	op := "!"
	if u.Op.Kind == token.Minus {
		op = "-"
	}
	// The operand is required to bind at the unary level, which is what wraps a
	// comparison: the DSL's `not` is below one and JavaScript's `!` is above.
	x, err := f.expr(u.X, jsPrecUnary)
	return jsExpr{src: op + x.src, prec: jsPrecUnary, await: x.await}, err
}

// binary is a comparison or a logical operator.
func (f *jsFile) binary(b *ast.Binary) (jsExpr, error) {
	op := operator(b.Op)
	switch op {
	case "and", "or":
		// Left-associative, and short-circuiting in both languages, so
		// `false and body.missing.x == 1` is false here too rather than a throw.
		js, prec := "&&", jsPrecAnd
		if op == "or" {
			js, prec = "||", jsPrecOr
		}
		left, err := f.expr(b.X, prec)
		if err != nil {
			return jsExpr{}, err
		}
		right, err := f.expr(b.Y, prec+1)
		if err != nil {
			return jsExpr{}, err
		}
		return jsExpr{
			src:   left.src + " " + js + " " + right.src,
			prec:  prec,
			await: left.await || right.await,
		}, nil

	case "contains":
		// Substring, element or key, off the left operand's type: one helper
		// rather than three spellings a reader would have to tell apart.
		return f.helper2(jsHelperContains, b.X, b.Y)

	case "matches":
		return f.helper2(jsHelperMatches, b.X, b.Y)

	case "==", "!=":
		return f.equality(b, op)

	case "<", "<=", ">", ">=":
		// Non-associative in the grammar, so neither side is ever a comparison
		// and JavaScript's chaining cannot be reached. Native, because
		// JavaScript's coercion here is closer to the interpreter's than
		// Python's raising is -- and the conformance corpus forbids comparing a
		// number against a string either way.
		return f.compare(b, op)
	}
	return jsExpr{}, fmt.Errorf("line %d: cannot export the operator %s", b.Op.Span.Line, op)
}

// equality emits `==` and `!=`.
//
// `===` when either operand is a scalar literal -- a number, string, boolean or
// null written in the file -- because `status === 200` is what a reader needs on
// the commonest line in the file and nothing about it can be wrong.
//
// art_eq otherwise, which is a structural compare. Python's `==` is structural,
// so `===` on two arrays would be a divergence between the two *exports* rather
// than between an export and the interpreter -- the one kind of difference this
// target has no excuse for.
func (f *jsFile) equality(b *ast.Binary, op string) (jsExpr, error) {
	if scalarLiteral(b.X) || scalarLiteral(b.Y) {
		return f.compare(b, strings.Replace(op, "=", "==", 1))
	}
	eq, err := f.helper2(jsHelperEq, b.X, b.Y)
	if err != nil || op == "==" {
		return eq, err
	}
	return jsExpr{src: "!" + eq.src, prec: jsPrecUnary, await: eq.await}, nil
}

// compare emits an infix comparison with both operands at the unary level.
func (f *jsFile) compare(b *ast.Binary, op string) (jsExpr, error) {
	left, err := f.expr(b.X, jsPrecUnary)
	if err != nil {
		return jsExpr{}, err
	}
	right, err := f.expr(b.Y, jsPrecUnary)
	if err != nil {
		return jsExpr{}, err
	}
	return jsExpr{
		src:   left.src + " " + op + " " + right.src,
		prec:  jsPrecCmp,
		await: left.await || right.await,
	}, nil
}

// scalarLiteral reports whether x is certainly a scalar: a number, string,
// boolean or null written in the file, or an interpolation -- which is a string
// by construction. These are the operands `===` is exactly right for, because
// none of them can be an array or an object and so none of them can need a
// structural compare.
func scalarLiteral(x ast.Expr) bool {
	if _, ok := x.(*ast.Interp); ok {
		return true
	}
	l, ok := x.(*ast.Literal)
	if !ok {
		return false
	}
	switch l.Kind() {
	case token.Number, token.String, token.Bool, token.Null:
		return true
	}
	return false
}

// helper2 emits a two-argument helper call over both operands.
func (f *jsFile) helper2(name string, x, y ast.Expr) (jsExpr, error) {
	left, err := f.expr(x, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	right, err := f.expr(y, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	return atom(
		f.use(name)+"("+left.src+", "+right.src+")",
		left.await || right.await,
	), nil
}

// exists is `body.x exists`, the one operator that answers about an absent path
// rather than reporting it.
//
// The operand goes in a thunk because that is the whole mechanism: a path read
// is art_at, which throws for an absent key, and art_exists is what turns that
// into false. The thunk is async when the operand awaits -- `text(".x") exists`
// is an element read -- and art_exists awaits it either way.
func (f *jsFile) exists(e *ast.Exists) (jsExpr, error) {
	x, err := f.expr(e.X, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	return atom("await "+f.use(jsHelperExists)+"("+thunk(x)+")", true), nil
}

// thunk is an arrow function over an emitted expression, async when it awaits.
func thunk(x jsExpr) string {
	if x.await {
		return "async () => " + x.src
	}
	return "() => " + x.src
}

// isType is `body.count is number`, over the JSON type names.
func (f *jsFile) isType(i *ast.IsType) (jsExpr, error) {
	x, err := f.expr(i.X, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	if !token.IsTypeName(i.Type.Value) {
		return jsExpr{}, fmt.Errorf("line %d: %q is not a type name", i.Type.Span.Line, i.Type.Value)
	}
	return atom(
		f.use(jsHelperIs)+"("+x.src+", "+jsQuote(i.Type.Value)+")",
		x.await,
	), nil
}

// path emits `body.data`, `body.items[0]`, `headers["content-type"]` -- and
// `page.url` and `page.title`.
//
// The whole chain becomes one art_at call rather than one per step, so a deep
// read is `art_at(body, "meta", "owner", "name")` and not four nested calls.
// art_at throws for a key that is not there, which is what makes an unresolvable
// path an errored assertion here, under the generated pytest and under the
// interpreter alike.
//
// The `page` root is the exception: its member set is closed and each member is
// a Playwright call, so `page.url` is a method on this side of the export and
// `page.title` is an awaited one.
func (f *jsFile) path(x ast.Expr) (jsExpr, error) {
	if member, ok := f.pageMember(x); ok {
		switch member {
		case "url":
			return atom("page.url()", false), nil
		case "title":
			return atom("await page.title()", true), nil
		default:
			return jsExpr{}, fmt.Errorf("line %d: page has no member %q", x.Span().Line, member)
		}
	}

	base, keys := flatten(x)
	parts := make([]string, 0, len(keys))
	await := false
	for _, k := range keys {
		src, err := f.key(k)
		if err != nil {
			return jsExpr{}, err
		}
		await = await || src.await
		parts = append(parts, src.src)
	}
	root, err := f.expr(base, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	return atom(
		f.use(jsHelperAt)+"("+root.src+", "+strings.Join(parts, ", ")+")",
		await || root.await,
	), nil
}

// key emits one step of a path: a member's name as a string, or an index's own
// expression.
func (f *jsFile) key(k pathKey) (jsExpr, error) {
	if k.name != "" || k.index == nil {
		return atom(jsQuote(k.name), false), nil
	}
	return f.expr(k.index, jsPrecOr)
}

// pathKey is one step of a path: a member name, or an index expression.
type pathKey struct {
	name  string
	index ast.Expr
}

// flatten walks a chain of members and subscripts down to the value it is read
// off, and returns the keys in reading order.
func flatten(x ast.Expr) (base ast.Expr, keys []pathKey) {
	for {
		switch n := x.(type) {
		case *ast.Member:
			name := n.Name.Value
			if name == "" {
				name = n.Name.Text
			}
			keys = append([]pathKey{{name: name}}, keys...)
			x = n.X
		case *ast.Index:
			keys = append([]pathKey{{index: n.Index}}, keys...)
			x = n.X
		default:
			return x, keys
		}
	}
}

// call emits a builtin.
//
// env() and match() are available everywhere; the five element functions need a
// page and the checker has already refused them in a step that has none.
// count() and visible() are Playwright calls directly -- a locator count does
// not wait and isVisible is a non-waiting predicate, which is exactly what
// SPEC.md says those two do -- while text(), value() and attr() need the
// count-then-read that makes "nothing matched" null instead of a timeout.
func (f *jsFile) call(c *ast.Call) (jsExpr, error) {
	if c.Callee == nil {
		return jsExpr{}, fmt.Errorf("line %d: this call has no function name", c.Span().Line)
	}
	name := c.Callee.Name()
	args, err := f.args(c)
	if err != nil {
		return jsExpr{}, err
	}
	if ok, min, max := arity(name, len(args)); !ok {
		if _, known := builtinArity[name]; !known {
			return jsExpr{}, fmt.Errorf("line %d: %s() is not a function artemis exports",
				c.Span().Line, name)
		}
		if min == max {
			return jsExpr{}, fmt.Errorf("line %d: %s() takes %d argument(s); this call has %d",
				c.Span().Line, name, max, len(args))
		}
		return jsExpr{}, fmt.Errorf("line %d: %s() takes %d to %d argument(s); this call has %d",
			c.Span().Line, name, min, max, len(args))
	}

	await := false
	for _, a := range args {
		await = await || a.await
	}
	src := func(call string) jsExpr { return atom(call, await) }
	asyncSrc := func(call string) jsExpr { return atom("await "+call, true) }

	switch name {
	case "env":
		// art_env rather than (process.env[name] ?? ""): ART-25 made an absent,
		// empty or blank variable an error in the interpreter, and the three
		// targets have to agree about what env() means.
		if len(args) == 2 {
			return src(f.use(jsHelperEnv) + "(" + args[0].src + ", " + args[1].src + ")"), nil
		}
		return src(f.use(jsHelperEnv) + "(" + args[0].src + ")"), nil
	case "match":
		return src(f.use(jsHelperMatch) + "(" + args[0].src + ", " + args[1].src + ")"), nil
	case "text":
		return asyncSrc(f.use(jsHelperText) + "(page, " + args[0].src + ")"), nil
	case "value":
		return asyncSrc(f.use(jsHelperValue) + "(page, " + args[0].src + ")"), nil
	case "attr":
		return asyncSrc(f.use(jsHelperAttr) + "(page, " + args[0].src + ", " + args[1].src + ")"), nil
	case "count":
		return asyncSrc("page.locator(" + args[0].src + ").count()"), nil
	case "visible":
		return asyncSrc("page.isVisible(" + args[0].src + ")"), nil
	}
	return jsExpr{}, fmt.Errorf("line %d: %s() is not a function artemis exports", c.Span().Line, name)
}

// args emits a call's arguments in order.
func (f *jsFile) args(c *ast.Call) ([]jsExpr, error) {
	out := make([]jsExpr, 0, len(c.Args))
	for _, a := range c.Args {
		s, err := f.expr(a.Value, jsPrecOr)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// object emits an object literal.
//
// A quoted key stays quoted, so the generated line and the .art line read alike.
// A key that is an expression -- `{"${k}": v}` parses -- becomes a computed key,
// which is the JavaScript spelling of the same thing.
func (f *jsFile) object(o *ast.Object) (jsExpr, error) {
	parts := make([]string, 0, len(o.Entries))
	await := false
	for _, e := range o.Entries {
		k, err := f.expr(e.Key, jsPrecOr)
		if err != nil {
			return jsExpr{}, err
		}
		v, err := f.expr(e.Value, jsPrecOr)
		if err != nil {
			return jsExpr{}, err
		}
		await = await || k.await || v.await
		key := k.src
		if !staticStringLiteral(e.Key) {
			key = "[" + key + "]"
		}
		parts = append(parts, key+": "+v.src)
	}
	if len(parts) == 0 {
		return atom("{}", false), nil
	}
	return atom("{ "+strings.Join(parts, ", ")+" }", await), nil
}

// array emits an array literal.
func (f *jsFile) array(a *ast.Array) (jsExpr, error) {
	parts := make([]string, 0, len(a.Elems))
	await := false
	for _, e := range a.Elems {
		v, err := f.expr(e.Value, jsPrecOr)
		if err != nil {
			return jsExpr{}, err
		}
		await = await || v.await
		parts = append(parts, v.src)
	}
	return atom("["+strings.Join(parts, ", ")+"]", await), nil
}

// staticStringLiteral reports whether x is a string written in the file, which
// is the one key shape that needs no computed-key brackets.
func staticStringLiteral(x ast.Expr) bool {
	l, ok := x.(*ast.Literal)
	return ok && l.Kind() == token.String
}

// jsQuote renders s as a JavaScript string literal, double-quoted.
func jsQuote(s string) string { return "\"" + escapeJS(s, '"') + "\"" }

// jsQuoteSingle renders s as a single-quoted literal, for an assertion's
// message: the message is the .art source, which is full of double quotes, and
// `'raw contains "alice"'` is readable where the escaped spelling is not.
func jsQuoteSingle(s string) string { return "'" + escapeJS(s, '\'') + "'" }

// escapeTemplate renders the literal text of a template literal, where a
// backtick ends it and `${` opens an interpolation.
func escapeTemplate(s string) string {
	s = escapeJS(s, '`')
	return strings.ReplaceAll(s, "${", "\\${")
}

// escapeJS renders s's contents for a literal delimited by quote.
func escapeJS(s string, quote byte) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == rune(quote):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jsPattern renders a regex literal as a JavaScript regular expression.
//
// A regex literal where one is possible, because that is how a JavaScript author
// writes a regex, with `/` escaped; a pattern holding a line terminator, or an
// empty one -- `//` is a comment -- falls back to a RegExp constructor.
//
// Go's regexp is RE2 and JavaScript's is not the same language. The differences
// that matter are handled rather than emitted as a file that throws on import:
//
//   - An inline flag group, `(?i)` and the rest, is refused with its offset.
//     JavaScript has no spelling for one anywhere in a pattern, not even at the
//     start, which is the one place Python accepts it.
//   - RE2's named group `(?P<n>...)` is JavaScript's `(?<n>...)`.
//   - A unicode class, `\p{L}`, needs the `u` flag, without which JavaScript
//     reads it as a literal `p{L}`. The flag is added only for a pattern that
//     uses one, because it also makes some escapes an error -- and a pattern
//     that does not use one has nothing to gain.
func jsPattern(p string, line int) (string, error) {
	if i := jsInlineFlags(p); i > 0 {
		return "", fmt.Errorf("line %d: the pattern /%s/ sets flags at offset %d; "+
			"javascript has no inline flag group, so write the flag into the pattern "+
			"-- `[aA]` for `(?i)a` -- or use a character class", line, p, i-1)
	}
	source := strings.ReplaceAll(p, "(?P<", "(?<")

	flags := ""
	if strings.Contains(source, `\p{`) || strings.Contains(source, `\P{`) {
		flags = "u"
	}
	if source != "" && !strings.ContainsAny(source, "\n\r") {
		return "/" + escapeSlashes(source) + "/" + flags, nil
	}
	return "new RegExp(" + jsQuote(source) + ", " + jsQuote(flags) + ")", nil
}

// escapeSlashes escapes every unescaped `/`, which is the one character a regex
// literal cannot hold.
func escapeSlashes(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == '\\' && i+1 < len(p):
			b.WriteByte(p[i])
			b.WriteByte(p[i+1])
			i++
		case p[i] == '/':
			b.WriteString(`\/`)
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

// jsInlineFlags is the 1-based offset of an inline flag group, and 0 for a
// pattern that has none.
//
// Only `(?letters)` counts -- the flag-setting form. `(?:...)`, `(?<n>...)`,
// `(?=...)` and the rest are groups JavaScript has too. Unlike Python,
// JavaScript accepts one nowhere, so position 0 is not excused.
func jsInlineFlags(p string) int {
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
		return i + 1
	}
	return 0
}

// The native `within` shapes.
//
// `expect visible(".modal") within "5s"` has a Playwright spelling that waits on
// the budget itself -- expect(page.locator(".modal")).toBeVisible({ timeout:
// 5000 }) -- and that is the line a reader of the generated test should see:
// Playwright's own waiting assertion, not a poll loop around a predicate.
//
// The set is the Python target's, one for one, because identical sets on both
// sides is what "Playwright on all three sides" buys. toHaveText is deliberately
// not among them, for the reason to_have_text is not: it normalises whitespace,
// so `text(".x") == "a  b"` would hold under it and not under the interpreter,
// and a divergence nobody can see in the generated line is worse than a poll
// loop they can. Everything unmapped falls back to art_within, which is
// pkg/steps/browserstep's settle loop and is exactly faithful.
//
// Each mapping also requires the operand it compares against to be statically a
// string or a number, because a Playwright matcher takes a value and not a
// predicate: `value("#q") == body.want` is a comparison, and comparisons are
// what the fallback is for.

// jsNative is the Playwright assertion for x with the budget ms, and whether
// there is one.
func (f *jsFile) jsNative(x ast.Expr, ms string) (string, bool, error) {
	x = unparen(x)

	// `not visible(sel)`: hidden, which Playwright reads as "not visible or not
	// in the page" -- the same answer as `not visible(sel)`, whose own false
	// covers both.
	if u, ok := x.(*ast.Unary); ok && u.Op.Kind == token.Ident && u.Op.Value == "not" {
		if sel, ok := elementCall(unparen(u.X), "visible"); ok {
			return f.matcher(sel, "toBeHidden", nil, ms)
		}
		return "", false, nil
	}
	if sel, ok := elementCall(x, "visible"); ok {
		return f.matcher(sel, "toBeVisible", nil, ms)
	}

	b, ok := x.(*ast.Binary)
	if !ok {
		return "", false, nil
	}
	left := unparen(b.X)
	switch operator(b.Op) {
	case "==":
		if sel, ok := elementCall(left, "value"); ok && staticString(b.Y) {
			return f.matcher(sel, "toHaveValue", []ast.Expr{b.Y}, ms)
		}
		if sel, ok := elementCall(left, "count"); ok && numberLiteral(b.Y) {
			return f.matcher(sel, "toHaveCount", []ast.Expr{b.Y}, ms)
		}
		if c, ok := left.(*ast.Call); ok && callee(c) == "attr" && len(c.Args) == 2 &&
			staticString(c.Args[1].Value) && staticString(b.Y) {
			return f.matcher(c.Args[0].Value, "toHaveAttribute",
				[]ast.Expr{c.Args[1].Value, b.Y}, ms)
		}
		if member, ok := f.pageMember(left); ok && staticString(b.Y) {
			switch member {
			case "url":
				return f.pageMatcher("toHaveURL", b.Y, ms)
			case "title":
				return f.pageMatcher("toHaveTitle", b.Y, ms)
			}
		}
	case "contains":
		if sel, ok := elementCall(left, "text"); ok && staticString(b.Y) {
			return f.matcher(sel, "toContainText", []ast.Expr{b.Y}, ms)
		}
	}
	return "", false, nil
}

// matcher is `await expect(page.locator(sel)).<name>(args..., { timeout: ms })`.
func (f *jsFile) matcher(sel ast.Expr, name string, args []ast.Expr, ms string) (string, bool, error) {
	target, err := f.value(sel)
	if err != nil {
		return "", false, err
	}
	parts := make([]string, 0, len(args)+1)
	for _, a := range args {
		src, err := f.expr(a, jsPrecOr)
		if err != nil {
			return "", false, err
		}
		parts = append(parts, src.src)
	}
	parts = append(parts, "{ timeout: "+ms+" }")
	return "await " + f.expectName() + "(page.locator(" + target.src + "))." +
		name + "(" + strings.Join(parts, ", ") + ")", true, nil
}

// pageMatcher is `await expect(page).<name>(value, { timeout: ms })`, for the
// two members of the `page` root.
func (f *jsFile) pageMatcher(name string, value ast.Expr, ms string) (string, bool, error) {
	src, err := f.expr(value, jsPrecOr)
	if err != nil {
		return "", false, err
	}
	return "await " + f.expectName() + "(page)." + name +
		"(" + src.src + ", { timeout: " + ms + " })", true, nil
}

// pageMember is the member of the `page` root x reads, and whether x is one.
func (f *jsFile) pageMember(x ast.Expr) (string, bool) {
	m, ok := x.(*ast.Member)
	if !ok {
		return "", false
	}
	id, ok := m.X.(*ast.Ident)
	if !ok || id.Name() != "page" || jsReference("page", f.step) != "page" {
		return "", false
	}
	return m.Name.Value, true
}

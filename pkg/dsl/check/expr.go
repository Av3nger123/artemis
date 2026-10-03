package check

import (
	"regexp"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// signatures are what a builtin's call looks like, for an arity hint. A hint
// that shows the shape is worth more than one that restates the count.
var signatures = map[string]string{
	"env":     `env("API_URL")`,
	"match":   `match(raw, /id=([0-9]+)/)`,
	"text":    `text("[role=status]")`,
	"value":   `value("#email")`,
	"attr":    `attr("#link", "href")`,
	"count":   `count(".invoice")`,
	"visible": `visible(".modal")`,
}

// counts and numbers spell a small number for a message, because "takes 1
// argument; this call has 2" reads like a template and "takes one argument;
// this call has two" reads like a sentence. A count with no word falls back to
// digits, which only an absurd call reaches.
var (
	counts  = map[int]string{1: "one argument", 2: "two arguments"}
	numbers = map[int]string{0: "none", 1: "one", 2: "two"}
)

func count(n int) string {
	if w, ok := counts[n]; ok {
		return w
	}
	return strconv.Itoa(n) + " arguments"
}

func number(n int) string {
	if w, ok := numbers[n]; ok {
		return w
	}
	return strconv.Itoa(n)
}

// ordinals name an argument's position for a message about a call with more
// than one.
var ordinals = []string{"first", "second"}

// maxGroups is how many capturing groups a `match()` pattern may have.
//
// One, because the value is group 1 and a pattern with two has an author who
// meant one of them. Taking the first silently -- which the YAML front end's
// pkg/shared/capture does -- is how a capture goes quietly wrong, and a group
// that is only there to group rewrites to `(?:...)` with no change in meaning.
const maxGroups = 1

// expr checks one expression in the scope view v.
//
// A nil expression and an *ast.Bad are both silence: the parser recovers
// rather than giving up, so a nil Expr is what a half-parsed line leaves
// behind and the diagnostic explaining it is already in the bag. Nothing
// downstream should report on a Bad again.
func (c *checker) expr(x ast.Expr, v *view) {
	switch x := x.(type) {
	case nil, *ast.Bad:
		return

	case *ast.Ident:
		if x != nil {
			c.ident(x, v)
		}

	case *ast.Literal:
		if x != nil {
			c.literal(x)
		}

	case *ast.Interp:
		// An interpolation's contents are an expression like any other: the
		// lexer hands real tokens out of `${ }` rather than a string to
		// re-scan, so an unknown name in a URL gets the same diagnostic at
		// its own span inside the string.
		//
		// Unless the string itself did not lex. `get "${base/orders"` leaves
		// an Interp whose later "expressions" are whatever the parser could
		// salvage from the rest of the file -- the next line's `expect`, for
		// one -- and resolving those names would report on source the author
		// never wrote as an expression. The lexical diagnostic is already in
		// the bag; this adds nothing to it.
		if hasInvalid(x) {
			return
		}
		for _, s := range x.Segments {
			c.expr(s.Expr, v)
		}

	case *ast.Unary:
		c.expr(x.X, v)

	case *ast.Binary:
		c.expr(x.X, v)
		c.expr(x.Y, v)

	case *ast.Exists:
		c.expr(x.X, v)

	case *ast.IsType:
		c.expr(x.X, v)
		c.typeName(x.Type)

	case *ast.Member:
		c.member(x, v)

	case *ast.Index:
		c.expr(x.X, v)
		c.expr(x.Index, v)

	case *ast.Call:
		c.call(x, v)

	case *ast.Paren:
		c.expr(x.X, v)

	case *ast.Object:
		for _, e := range x.Entries {
			c.expr(e.Key, v)
			c.expr(e.Value, v)
		}

	case *ast.Array:
		for _, e := range x.Elems {
			c.expr(e.Value, v)
		}
	}
}

// ident resolves a name used as a value.
func (c *checker) ident(x *ast.Ident, v *view) {
	t := x.Tok
	if t.Kind != token.Ident {
		return
	}
	if c.reserved(t, "name") {
		return
	}
	if v.has(t.Value) {
		return
	}
	// An element function named without its parentheses is not a value, and a
	// did-you-mean against the roots would be the wrong advice: the name is
	// right and the call is what is missing. Checked after the scope, so a
	// `var text = ...` that shadows one still resolves.
	if elementFnOf[t.Value] {
		c.bag.Error(t.Span, diag.UnknownIdentifier, "%q is a function, not a value", t.Value).
			Hintf("call it: %s", signatures[t.Value]).
			Suggest(signatures[t.Value])
		return
	}
	c.notFound(t.Span, t.Value, v)
}

// member checks `a.b`.
//
// Only the *root* is resolved, and only a root with a closed member set has
// its member checked. `body.data.count` stops at `body`, because a response's
// shape is a run-time fact and checking it would reject correct files;
// `page.titl` is caught, because a page has exactly a url and a title.
func (c *checker) member(m *ast.Member, v *view) {
	c.expr(m.X, v)

	root, ok := m.X.(*ast.Ident)
	if !ok || root == nil || root.Tok.Kind != token.Ident || !v.has(root.Name()) {
		return
	}
	members, closed := memberRoots[root.Name()]
	if !closed || m.Name.Kind != token.Ident {
		return
	}
	if contains(members, m.Name.Value) {
		return
	}
	ref := c.bag.Error(m.Name.Span, diag.UnknownField, "unknown field %q", m.Name.Value)
	if !ref.DidYouMean(m.Name.Value, members) {
		ref.Hintf("%s holds: %s", root.Name(), strings.Join(members, ", "))
	}
}

// call checks a builtin call: that the callee is one, that it is one *here*,
// its arity, that each argument is the kind of value its parameter takes, and
// -- for `match()` -- that its pattern has at most one capturing group.
func (c *checker) call(x *ast.Call, v *view) {
	for _, a := range x.Args {
		c.expr(a.Value, v)
	}
	if x.Callee == nil || x.Callee.Tok.Kind != token.Ident {
		return
	}
	t := x.Callee.Tok
	name := t.Value

	if c.reserved(t, "function name") {
		return
	}

	switch {
	case freeFnOf[name]:
		// env() and match() are legal in every scope, including a var's
		// value. That is the design's whole point about env(): it is an
		// ordinary expression legal wherever an expression is, rather than a
		// template form that only works inside a variable's value. match()
		// joins it because searching a string needs no page and no response.
	case elementFnOf[name]:
		if v.typ != Browser {
			c.notInScopeFn(t.Span, name, v)
			return
		}
	default:
		ref := c.bag.Error(t.Span, diag.UnknownIdentifier, "unknown function %q", name)
		if !ref.DidYouMean(name, Functions(v.typ)) {
			ref.Hintf("callable here: %s", strings.Join(Functions(v.typ), "(), ")+"()")
		}
		return
	}

	ps := params[name]
	if len(x.Args) != len(ps) {
		c.bag.Error(x.Span(), diag.BadArity,
			"%s() takes %s; this call has %s", name, count(len(ps)), number(len(x.Args))).
			Hintf("%s", signatures[name])
		return
	}
	// Each parameter says what it takes -- a string for a variable name, a
	// selector, an attribute name or the text to search; a regex or a string
	// for `match()`'s pattern. Only a literal of the wrong kind is reported:
	// an identifier or an interpolation could hold the right value and this
	// stage does not evaluate.
	for i, a := range x.Args {
		lit, ok := a.Value.(*ast.Literal)
		if !ok || lit == nil || ps[i].accepts(lit.Kind()) {
			continue
		}
		// "argument 1" of a one-argument function is noise; a second argument
		// is the only place the position is worth saying.
		which := ""
		if len(ps) > 1 {
			which = ordinals[i] + " "
		}
		c.bag.Error(lit.Span(), diag.BadValue,
			"%s()'s %sargument must be %s, not %s", name, which, ps[i].want, describeLiteral(lit)).
			Hintf("%s", signatures[name])
	}
	if name == "match" && len(x.Args) == 2 {
		c.groups(x.Args[1].Value)
	}
}

// accepts reports whether a literal of kind k satisfies this parameter.
func (p param) accepts(k token.Kind) bool {
	for _, want := range p.kinds {
		if k == want {
			return true
		}
	}
	return false
}

// groups rejects a `match()` pattern with more than one capturing group.
//
// Only a literal pattern is counted, because only a literal is known here. A
// pattern that arrives as a string in a var is counted by pkg/eval at run
// time and gives the same reason as an errored assertion -- which is the
// usual split in this language: what is checked here is whichever half of the
// rule can be settled here, not a different rule.
//
// A pattern that does not compile is already reported by literal() below, and
// counting one that does not compile would be a second diagnostic about one
// mistake.
func (c *checker) groups(pattern ast.Expr) {
	lit, ok := pattern.(*ast.Literal)
	if !ok || lit == nil || lit.Kind() != token.Regex {
		return
	}
	re, err := regexp.Compile(lit.Tok.Value)
	if err != nil {
		return
	}
	n := re.NumSubexp()
	if n <= maxGroups {
		return
	}
	c.bag.Error(lit.Span(), diag.BadValue,
		"match() reads one capturing group, and this pattern has %s", number(n)).
		Hintf("make the groups you do not want non-capturing: (?:...)")
}

// literal compiles a regex literal.
//
// This is the second of the two faults the design moves from run time to
// compile time. The lexer scans `/order-(\d+/` happily -- balancing
// parentheses is not a lexical property -- so a scenario that fails forty
// minutes into a suite over an unbalanced paren is exactly the class of
// mistake this stage exists to delete. Go's own error is carried through,
// because it names which paren.
func (c *checker) literal(l *ast.Literal) {
	if l.Kind() != token.Regex {
		return
	}
	if _, err := regexp.Compile(l.Tok.Value); err != nil {
		c.bag.Error(l.Span(), diag.InvalidRegex, "regex does not compile: %v", err)
	}
}

// typeName checks the right-hand side of `is`.
//
// The parser accepts any identifier there so that a typo gets a did-you-mean
// against six short words rather than a bare syntax error.
func (c *checker) typeName(t token.Token) {
	if t.Kind != token.Ident || token.IsTypeName(t.Value) {
		return
	}
	ref := c.bag.Error(t.Span, diag.UnknownType, "unknown type name %q", t.Value)
	if !ref.DidYouMean(t.Value, token.TypeNames) {
		ref.Hintf("a type is one of: %s", strings.Join(token.TypeNames, ", "))
	}
}

// describeLiteral names a literal's kind for a message: "a number", "a
// regex".
func describeLiteral(l *ast.Literal) string {
	switch l.Kind() {
	case token.Number:
		return "a number"
	case token.Bool:
		return "a boolean"
	case token.Null:
		return "null"
	case token.Regex:
		return "a regex"
	case token.String:
		return "a string"
	}
	return "a value"
}

// hasInvalid reports whether a node was built over a lexical error.
//
// Only interpolated strings are asked, because they are the one construct
// whose *structure* can be destroyed by a lexical fault: an unterminated
// string swallows the rest of the file, and every token it swallowed is still
// in the tree, in an expression position, as a name.
func hasInvalid(n ast.Node) bool {
	for _, t := range n.Tokens(nil) {
		if t.Kind == token.Invalid {
			return true
		}
	}
	return false
}

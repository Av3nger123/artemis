package ast

import "artemis/pkg/dsl/token"

// Ident is a name: a variable, a capture, a bound root like `status`, or the
// callee of a call.
type Ident struct {
	Tok token.Token
}

// Name is the identifier's text, which is what the checker resolves.
func (i *Ident) Name() string                           { return i.Tok.Value }
func (i *Ident) Tokens(dst []token.Token) []token.Token { return appendTok(dst, i.Tok) }
func (i *Ident) Span() token.Span                       { return i.Tok.Span }
func (i *Ident) expr()                                  {}

// Literal is a number, a string with no interpolation in it, a regex, a
// boolean, or null.
//
// The decoded value is Tok.Value -- a string's text with its escapes resolved,
// a regex's pattern with \/ unescaped -- and Tok.Text is the source with its
// delimiters, which is what the round trip needs. Nothing here converts a
// number to a float or compiles a regex: this issue parses, and both of those
// are errors someone has to be told about with a span, which is the evaluator's
// and the checker's job.
type Literal struct {
	Tok token.Token
}

// Kind is the literal's token kind: token.Number, String, Regex, Bool or Null.
func (l *Literal) Kind() token.Kind                       { return l.Tok.Kind }
func (l *Literal) Tokens(dst []token.Token) []token.Token { return appendTok(dst, l.Tok) }
func (l *Literal) Span() token.Span                       { return l.Tok.Span }
func (l *Literal) expr()                                  {}

// Segment is one piece of an interpolated string: a delimiter token and the
// expression that follows it.
//
// Delim is a token.StringStart for the first segment and a token.StringMid for
// every later one, and its Text carries the literal text either side of the
// braces -- `"a${` then `}b${`. So each delimiter belongs to exactly one
// segment and concatenating the segments' tokens is the string again, which is
// the shape the lexer chose for exactly this reason.
type Segment struct {
	Delim token.Token
	Expr  Expr
}

// Interp is a double-quoted string with at least one `${...}` in it.
//
//	"a${x}b${y}c"  ->  Segments[{`"a${`, x}, {`}b${`, y}], End: `}c"`
//
// A string with no interpolation is a Literal, not an Interp with no segments:
// the lexer emits a single token.String for it, and keeping the two apart
// means a client asking "is this a plain string" does not have to count
// segments.
type Interp struct {
	Segments []Segment
	End      token.Token // the closing token.StringEnd
}

func (i *Interp) Tokens(dst []token.Token) []token.Token {
	for _, s := range i.Segments {
		dst = appendTok(dst, s.Delim)
		dst = appendNode(dst, s.Expr)
	}
	return appendTok(dst, i.End)
}

func (i *Interp) Span() token.Span { return spanOf(i) }
func (i *Interp) expr()            {}

// Unary is `not x` or `-x`.
//
// Op is an Ident token whose text is "not", or a token.Minus. The two share a
// node because they share a shape; they do not share a precedence level, and
// the gap between them is where `exists` and `is` sit. See parser/expr.go.
type Unary struct {
	Op token.Token
	X  Expr
}

func (u *Unary) Tokens(dst []token.Token) []token.Token {
	return appendNode(appendTok(dst, u.Op), u.X)
}

func (u *Unary) Span() token.Span { return spanOf(u) }
func (u *Unary) expr()            {}

// Binary is an infix operator: `and`, `or`, `==`, `!=`, `<`, `<=`, `>`, `>=`,
// `contains` or `matches`.
//
// The word operators arrive as Ident tokens, because the grammar has no
// lexical keywords -- `capture contains = ...` is a file someone will write --
// so the operator is Op.Text, not Op.Kind.
type Binary struct {
	X  Expr
	Op token.Token
	Y  Expr
}

func (b *Binary) Tokens(dst []token.Token) []token.Token {
	dst = appendNode(dst, b.X)
	dst = appendTok(dst, b.Op)
	return appendNode(dst, b.Y)
}

func (b *Binary) Span() token.Span { return spanOf(b) }
func (b *Binary) expr()            {}

// Exists is the postfix predicate `body.x exists`.
type Exists struct {
	X  Expr
	Op token.Token // exists
}

func (e *Exists) Tokens(dst []token.Token) []token.Token {
	return appendTok(appendNode(dst, e.X), e.Op)
}

func (e *Exists) Span() token.Span { return spanOf(e) }
func (e *Exists) expr()            {}

// IsType is the postfix predicate `body.count is number`.
//
// Type is the type-name token (one of token.TypeNames). The parser accepts any
// identifier there so a typo gets a did-you-mean against six short words
// rather than a syntax error.
type IsType struct {
	X    Expr
	Op   token.Token // is
	Type token.Token // string, number, boolean, object, array, null
}

func (i *IsType) Tokens(dst []token.Token) []token.Token {
	dst = appendNode(dst, i.X)
	dst = appendTok(dst, i.Op)
	return appendTok(dst, i.Type)
}

func (i *IsType) Span() token.Span { return spanOf(i) }
func (i *IsType) expr()            {}

// Member is `body.data`.
type Member struct {
	X    Expr
	Dot  token.Token
	Name token.Token
}

func (m *Member) Tokens(dst []token.Token) []token.Token {
	dst = appendNode(dst, m.X)
	dst = appendTok(dst, m.Dot)
	return appendTok(dst, m.Name)
}

func (m *Member) Span() token.Span { return spanOf(m) }
func (m *Member) expr()            {}

// Index is `body.items[0]` or `headers["content-type"]`.
type Index struct {
	X        Expr
	LBracket token.Token
	Index    Expr
	RBracket token.Token
}

func (i *Index) Tokens(dst []token.Token) []token.Token {
	dst = appendNode(dst, i.X)
	dst = appendTok(dst, i.LBracket)
	dst = appendNode(dst, i.Index)
	return appendTok(dst, i.RBracket)
}

func (i *Index) Span() token.Span { return spanOf(i) }
func (i *Index) expr()            {}

// Arg is one call argument and the comma after it, which is zero for the last
// one unless the author left a trailing comma.
type Arg struct {
	Value Expr
	Comma token.Token
}

// Call is `env("API_URL")` or `text("[role=status]")`.
//
// Callee is an *Ident: the grammar is `Call = Ident "(" ... ")"`, so there are
// no first-class functions and no `body.f()`. Which names are callable is
// token.Builtins, and the checker is what rejects a browser element function
// in an api step.
type Call struct {
	Callee *Ident
	LParen token.Token
	Args   []Arg
	RParen token.Token
}

func (c *Call) Tokens(dst []token.Token) []token.Token {
	dst = appendNode(dst, c.Callee)
	dst = appendTok(dst, c.LParen)
	for _, a := range c.Args {
		dst = appendNode(dst, a.Value)
		dst = appendTok(dst, a.Comma)
	}
	return appendTok(dst, c.RParen)
}

func (c *Call) Span() token.Span { return spanOf(c) }
func (c *Call) expr()            {}

// Entry is one `"key": value` pair in an object literal, with the comma after
// it.
//
// Key is an Expr because the grammar says String and an interpolated string is
// one: `{"${k}": v}` parses. The checker is what insists a key be a string.
type Entry struct {
	Key   Expr
	Colon token.Token
	Value Expr
	Comma token.Token
}

// Object is an object literal, `{"username": "alice", "password": pw}`.
//
// A request body is this, serialised to JSON by the lowerer -- not a string
// with placeholders spliced into it, which is the failure mode where a
// captured object reached a body by text concatenation.
type Object struct {
	LBrace  token.Token
	Entries []Entry
	RBrace  token.Token
}

func (o *Object) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, o.LBrace)
	for _, e := range o.Entries {
		dst = appendNode(dst, e.Key)
		dst = appendTok(dst, e.Colon)
		dst = appendNode(dst, e.Value)
		dst = appendTok(dst, e.Comma)
	}
	return appendTok(dst, o.RBrace)
}

func (o *Object) Span() token.Span { return spanOf(o) }
func (o *Object) expr()            {}

// Elem is one element of an array literal and the comma after it.
type Elem struct {
	Value Expr
	Comma token.Token
}

// Array is an array literal, `["-f", "seed.sql"]`.
type Array struct {
	LBracket token.Token
	Elems    []Elem
	RBracket token.Token
}

func (a *Array) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, a.LBracket)
	for _, e := range a.Elems {
		dst = appendNode(dst, e.Value)
		dst = appendTok(dst, e.Comma)
	}
	return appendTok(dst, a.RBracket)
}

func (a *Array) Span() token.Span { return spanOf(a) }
func (a *Array) expr()            {}

// Paren is `( expr )`.
//
// It is a node rather than being folded away, because folding it would make
// the printer reproduce `(a or b) and c` as `a or b and c`, which is a
// different expression. Parentheses are source the round trip owes the author.
type Paren struct {
	LParen token.Token
	X      Expr
	RParen token.Token
}

func (p *Paren) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, p.LParen)
	dst = appendNode(dst, p.X)
	return appendTok(dst, p.RParen)
}

func (p *Paren) Span() token.Span { return spanOf(p) }
func (p *Paren) expr()            {}

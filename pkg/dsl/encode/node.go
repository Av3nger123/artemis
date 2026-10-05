package encode

import (
	"reflect"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// The node kinds. They are the encoding's vocabulary, so they are constants in
// one place: the encoder writes them, Decode dispatches on them, and Schema()
// lists them, and a kind spelled three times could be spelled three ways.
const (
	kindScenario   = "scenario"
	kindConfig     = "config"
	kindVar        = "var"
	kindStep       = "step"
	kindRequest    = "request"
	kindRun        = "run"
	kindBrowser    = "browser"
	kindBrowserAct = "browserAct"
	kindBlock      = "block"
	kindField      = "field"
	kindExpect     = "expect"
	kindCapture    = "capture"
	kindIdent      = "ident"
	kindLiteral    = "literal"
	kindInterp     = "interp"
	kindUnary      = "unary"
	kindBinary     = "binary"
	kindExists     = "exists"
	kindIsType     = "isType"
	kindMember     = "member"
	kindIndex      = "index"
	kindCall       = "call"
	kindObject     = "object"
	kindArray      = "array"
	kindParen      = "paren"
	kindBad        = "bad"
)

// node encodes one node, or returns nil for one that is not there -- which a
// recovered parse leaves wherever a child was optional.
//
// The key order is the same for every kind: the kind, the node's own fields in
// grammar order, its span, the checker's labels, then its comments. A reader
// scanning a golden file always finds the same thing in the same place.
func (e *enc) node(n ast.Node) *obj {
	if isNil(n) {
		return nil
	}
	switch v := n.(type) {
	case *ast.Scenario:
		return e.scenario(v)
	case *ast.ConfigDecl:
		return e.config(v)
	case *ast.VarDecl:
		return e.varDecl(v)
	case *ast.StepDecl:
		return e.step(v)
	case *ast.Request:
		return e.request(v)
	case *ast.Run:
		return e.run(v)
	case *ast.Browser:
		return e.browser(v)
	case *ast.BrowserAct:
		return e.browserAct(v)
	case *ast.Block:
		return e.block(v)
	case *ast.Field:
		return e.field(v)
	case *ast.Expect:
		return e.expect(v)
	case *ast.Capture:
		return e.capture(v)
	case *ast.Bad:
		return e.bad(v)
	}
	return e.expr(n)
}

// expr encodes the expression kinds. They are split out from node only because
// one switch over twenty-six types is harder to read than two.
func (e *enc) expr(n ast.Node) *obj {
	switch v := n.(type) {
	case *ast.Ident:
		return start(kindIdent, v).set("name", v.Tok.Text).done()
	case *ast.Literal:
		return start(kindLiteral, v).
			set("literal", v.Kind().String()).
			set("text", v.Tok.Text).
			set("value", v.Tok.Value).done()
	case *ast.Interp:
		return e.interp(v)
	case *ast.Unary:
		return start(kindUnary, v).set("op", v.Op.Text).set("x", e.node(v.X)).done()
	case *ast.Binary:
		return start(kindBinary, v).
			set("op", v.Op.Text).set("x", e.node(v.X)).set("y", e.node(v.Y)).done()
	case *ast.Exists:
		return start(kindExists, v).set("x", e.node(v.X)).done()
	case *ast.IsType:
		return start(kindIsType, v).set("x", e.node(v.X)).set("type", v.Type.Text).done()
	case *ast.Member:
		return start(kindMember, v).set("x", e.node(v.X)).set("name", v.Name.Text).done()
	case *ast.Index:
		return start(kindIndex, v).set("x", e.node(v.X)).set("index", e.node(v.Index)).done()
	case *ast.Call:
		return e.call(v)
	case *ast.Object:
		return e.object(v)
	case *ast.Array:
		return start(kindArray, v).set("elems", e.elems(v)).done()
	case *ast.Paren:
		return start(kindParen, v).set("x", e.node(v.X)).done()
	}
	return nil
}

func (e *enc) scenario(s *ast.Scenario) *obj {
	return start(kindScenario, s).
		set("name", nameObj(s.Name)).
		set("body", e.list(toNodes(s.Body))).
		span().
		comments(toks(s.Keyword, s.Name), s.LBrace, s.RBrace).done()
}

func (e *enc) config(d *ast.ConfigDecl) *obj {
	return start(kindConfig, d).
		set("subject", d.Subject.Text).
		set("block", e.node(d.Block)).
		span().
		comments(toks(d.Keyword, d.Subject), zero, zero).done()
}

func (e *enc) varDecl(d *ast.VarDecl) *obj {
	return start(kindVar, d).
		set("name", d.Name.Text).
		set("value", e.node(d.Value)).
		span().
		comments(d.Tokens(nil), zero, zero).done()
}

// step is the one node carrying all three of the checker's conclusions: the
// type its action implies, the names a path picker may offer inside it, and --
// on each expect below -- the simple/complex label.
//
// scope is here rather than derivable from `artemis grammar` because it depends
// on where the step sits: it is the step type's own roots, then the scenario's
// vars, then the captures of the steps *above* this one. Nothing outside the
// checker knows it.
func (e *enc) step(s *ast.StepDecl) *obj {
	return start(kindStep, s).
		set("name", nameObj(s.Name)).
		set("action", e.node(s.Action)).
		set("body", e.list(toNodes(s.Body))).
		span().
		set("stepType", e.info.StepType(s).String()).
		set("scope", e.info.Scope(s)).
		comments(toks(s.Keyword, s.Name), s.LBrace, s.RBrace).done()
}

// request carries its method's spelling, which is the whole of the step-type
// inference: there is no type key in the language, and a verb here is what
// makes this an api step.
func (e *enc) request(r *ast.Request) *obj {
	return start(kindRequest, r).
		set("method", r.Method.Text).
		set("url", e.node(r.URL)).
		set("block", e.node(r.Block)).
		span().
		comments(append(toks(r.Method), nodeToks(r.URL)...), zero, zero).done()
}

func (e *enc) run(r *ast.Run) *obj {
	return start(kindRun, r).
		set("command", e.node(r.Command)).
		set("block", e.node(r.Block)).
		span().
		comments(append(toks(r.Keyword), nodeToks(r.Command)...), zero, zero).done()
}

func (e *enc) browser(b *ast.Browser) *obj {
	return start(kindBrowser, b).
		set("acts", e.list(toNodes(b.Acts))).
		span().
		comments(toks(b.Keyword), b.LBrace, b.RBrace).done()
}

func (e *enc) browserAct(a *ast.BrowserAct) *obj {
	return start(kindBrowserAct, a).
		set("name", a.Name.Text).
		set("target", e.node(a.Target)).
		set("value", e.node(a.Value)).
		span().
		comments(a.Tokens(nil), zero, zero).done()
}

// block has no head of its own -- its opening brace sits at the end of the line
// its parent renders -- so it carries only the three brace comment slots.
func (e *enc) block(b *ast.Block) *obj {
	return start(kindBlock, b).
		set("fields", e.list(toNodes(b.Fields))).
		span().
		comments(nil, b.LBrace, b.RBrace).done()
}

// field is eleven of the grammar's productions in one node. Exactly one of
// value and block is set, and key only for `header` and `query`, so both are
// optional here for the same reason they are optional there.
func (e *enc) field(f *ast.Field) *obj {
	own := append(toks(f.Name), nodeToks(f.Key)...)
	own = append(own, toks(f.Assign)...)
	own = append(own, nodeToks(f.Value)...)
	own = append(own, toks(f.Comma)...)
	return start(kindField, f).
		set("name", f.Name.Text).
		set("key", e.node(f.Key)).
		set("value", e.node(f.Value)).
		set("block", e.node(f.Block)).
		span().
		comments(own, zero, zero).done()
}

// expect carries the checker's class, which is the contract the design document
// asks for by name: "the label is in the JSON encoding, so it is a documented
// property of the tree rather than something each client re-derives".
func (e *enc) expect(x *ast.Expect) *obj {
	return start(kindExpect, x).
		set("value", e.node(x.Value)).
		set("budget", e.node(x.Budget)).
		span().
		set("class", e.info.Class(x).String()).
		comments(x.Tokens(nil), zero, zero).done()
}

func (e *enc) capture(c *ast.Capture) *obj {
	return start(kindCapture, c).
		set("name", c.Name.Text).
		set("value", e.node(c.Value)).
		span().
		comments(c.Tokens(nil), zero, zero).done()
}

// bad is source that did not parse, kept verbatim.
//
// Encoding is total: the invalid corpus is what a file mid-edit in a UI looks
// like, and `artemis ast` has to work on one. The other direction is not --
// Decode refuses a document holding one of these -- because the design settles
// that a UI owns incomplete state in its own memory and only ever serialises a
// complete tree.
func (e *enc) bad(b *ast.Bad) *obj {
	return start(kindBad, b).set("source", ast.Source(b)).span().done()
}

func (e *enc) interp(i *ast.Interp) *obj {
	segs := make([]any, 0, len(i.Segments))
	for _, s := range i.Segments {
		o := newObj()
		o.set("delim", s.Delim.Text)
		o.set("expr", e.node(s.Expr))
		segs = append(segs, o)
	}
	return start(kindInterp, i).set("segments", segs).set("end", i.End.Text).done()
}

func (e *enc) call(c *ast.Call) *obj {
	args := make([]any, 0, len(c.Args))
	for _, a := range c.Args {
		if o := e.node(a.Value); o != nil {
			args = append(args, o)
		}
	}
	callee := ""
	if c.Callee != nil {
		callee = c.Callee.Tok.Text
	}
	return start(kindCall, c).set("callee", callee).set("args", args).done()
}

func (e *enc) object(v *ast.Object) *obj {
	entries := make([]any, 0, len(v.Entries))
	for _, en := range v.Entries {
		o := newObj()
		o.set("key", e.node(en.Key))
		o.set("value", e.node(en.Value))
		entries = append(entries, o)
	}
	return start(kindObject, v).set("entries", entries).done()
}

func (e *enc) elems(a *ast.Array) []any {
	out := make([]any, 0, len(a.Elems))
	for _, el := range a.Elems {
		if o := e.node(el.Value); o != nil {
			out = append(out, o)
		}
	}
	return out
}

// list encodes a list of children, dropping the nil ones a recovered parse
// leaves behind. An empty list is absent from its parent rather than `[]`,
// which is obj.set's rule.
func (e *enc) list(ns []ast.Node) []any {
	out := make([]any, 0, len(ns))
	for _, n := range ns {
		if o := e.node(n); o != nil {
			out = append(out, o)
		}
	}
	return out
}

// nameObj is a quoted name -- a scenario's or a step's -- as both the source
// text the printer writes and the decoded value a form field shows.
//
// Nil when the token was never in the file, which is a step whose name the
// parser had to recover past.
func nameObj(t token.Token) *obj {
	if t.Text == "" {
		return nil
	}
	o := newObj()
	o.set("text", t.Text)
	o.set("value", t.Value)
	return o
}

// builder assembles one node's object in the fixed key order. It exists so that
// each encoder above reads as the list of fields its node has, with the span and
// the comments as one call rather than four lines of plumbing each.
type builder struct {
	o *obj
	n ast.Node
}

func start(kind string, n ast.Node) *builder {
	b := &builder{o: newObj(), n: n}
	b.o.set("kind", kind)
	return b
}

func (b *builder) set(key string, v any) *builder {
	b.o.set(key, v)
	return b
}

// span writes the node's span. An expression leaf's span is written by done, so
// only the kinds that have fields after their span call this explicitly.
func (b *builder) span() *builder {
	b.o.set("span", spanOf(b.n.Span()))
	return b
}

func (b *builder) comments(own []token.Token, lbrace, rbrace token.Token) *builder {
	b.o.set("comments", nodeComments(own, lbrace, rbrace))
	return b
}

// done writes the span if nothing has yet, and returns the object. Every node
// carries one: that is R1, and the kinds with no comments and no labels reach it
// here.
func (b *builder) done() *obj {
	b.span()
	return b.o
}

// zero is the token that was never in a file, for the nodes with no braces of
// their own.
var zero token.Token

// toks is a token list literal, dropping the tokens that were never in the file
// -- an absent `within`, an absent closing brace -- the way ast's own appendTok
// does.
func toks(ts ...token.Token) []token.Token {
	out := make([]token.Token, 0, len(ts))
	for _, t := range ts {
		if t.Text != "" || len(t.Leading) > 0 || len(t.Trailing) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// nodeToks is a child's tokens, so the node that renders it also owns its
// comments.
func nodeToks(n ast.Node) []token.Token {
	if isNil(n) {
		return nil
	}
	return n.Tokens(nil)
}

// isNil reports whether n holds no node, interface nil or typed nil alike. The
// typed-nil case is the one that matters: a parser helper that returned
// (*ast.Ident)(nil) stores a non-nil ast.Expr holding a nil pointer, and
// calling Tokens on it panics.
func isNil(n ast.Node) bool {
	if n == nil {
		return true
	}
	switch v := reflect.ValueOf(n); v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return false
}

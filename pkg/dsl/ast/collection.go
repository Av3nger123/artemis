package ast

import "artemis/pkg/dsl/token"

// Import is `import "collections/auth.art"`. It sits in File.Scenarios with
// the scenarios and collections, because that slice is the file's top level
// and is what keeps Source exact.
type Import struct {
	Keyword token.Token // import
	Path    token.Token // the String
}

func (i *Import) Tokens(dst []token.Token) []token.Token {
	return appendTok(appendTok(dst, i.Keyword), i.Path)
}
func (i *Import) Span() token.Span { return spanOf(i) }
func (i *Import) decl()            {}

// Collection is `collection "orders" { request ... flow ... }`.
type Collection struct {
	Keyword token.Token
	Name    token.Token
	LBrace  token.Token
	Items   []Decl // *RequestDecl, *FlowDecl, or *Bad
	RBrace  token.Token
}

func (c *Collection) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, c.Keyword)
	dst = appendTok(dst, c.Name)
	dst = appendTok(dst, c.LBrace)
	for _, d := range c.Items {
		dst = appendNode(dst, d)
	}
	return appendTok(dst, c.RBrace)
}
func (c *Collection) Span() token.Span { return spanOf(c) }
func (c *Collection) decl()            {}

// Param is one parameter: `sku`, `qty = 1`, or `secret password`.
type Param struct {
	Secret  token.Token // zero when absent
	Name    token.Token
	Assign  token.Token // zero when there is no default
	Default Expr        // nil when required
	Comma   token.Token
}

func (p *Param) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, p.Secret)
	dst = appendTok(dst, p.Name)
	dst = appendTok(dst, p.Assign)
	dst = appendNode(dst, p.Default)
	return appendTok(dst, p.Comma)
}
func (p *Param) Span() token.Span { return spanOf(p) }

// Params is the parenthesised parameter list of a request or a flow.
type Params struct {
	LParen token.Token
	List   []*Param
	RParen token.Token
}

func (p *Params) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, p.LParen)
	for _, x := range p.List {
		dst = appendNode(dst, x)
	}
	return appendTok(dst, p.RParen)
}
func (p *Params) Span() token.Span { return spanOf(p) }

// RequestDecl is `request create(sku, qty = 1) { <action> <stmt>... }`: the
// body of a step, with parameters and without a quoted name.
type RequestDecl struct {
	Keyword token.Token
	Name    token.Token
	Params  *Params
	LBrace  token.Token
	Action  Action
	Body    []Stmt
	RBrace  token.Token
}

func (r *RequestDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, r.Keyword)
	dst = appendTok(dst, r.Name)
	dst = appendNode(dst, r.Params)
	dst = appendTok(dst, r.LBrace)
	for _, it := range r.items() {
		dst = appendNode(dst, it)
	}
	return appendTok(dst, r.RBrace)
}

// items is the action and statements in source order, as StepDecl.items.
func (r *RequestDecl) items() []Node {
	return (&StepDecl{Action: r.Action, Body: r.Body}).items()
}
func (r *RequestDecl) Span() token.Span { return spanOf(r) }
func (r *RequestDecl) decl()            {}

// FlowDecl is `flow checkout(user) { step ... use ... }`.
type FlowDecl struct {
	Keyword token.Token
	Name    token.Token
	Params  *Params
	LBrace  token.Token
	Body    []Decl // *StepDecl, *UseDecl, or *Bad
	RBrace  token.Token
}

func (f *FlowDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, f.Keyword)
	dst = appendTok(dst, f.Name)
	dst = appendNode(dst, f.Params)
	dst = appendTok(dst, f.LBrace)
	for _, d := range f.Body {
		dst = appendNode(dst, d)
	}
	return appendTok(dst, f.RBrace)
}
func (f *FlowDecl) Span() token.Span { return spanOf(f) }
func (f *FlowDecl) decl()            {}

// UseDecl is `use auth.login as admin { user = "alice" ... }`. It is legal in
// a scenario body and in a flow body, and pkg/dsl/expand replaces it with the
// steps it names before the checker sees the file.
type UseDecl struct {
	Keyword    token.Token // use
	Collection token.Token // zero for a bare sibling ref
	Dot        token.Token // zero for a bare sibling ref
	Item       token.Token
	As         token.Token // zero when absent
	Alias      token.Token // zero when absent
	LBrace     token.Token // zero when there is no block
	Lines      []Stmt      // *Field, *BodySet, *Drop, *Expect, *In, *Bad
	RBrace     token.Token
}

func (u *UseDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, u.Keyword)
	dst = appendTok(dst, u.Collection)
	dst = appendTok(dst, u.Dot)
	dst = appendTok(dst, u.Item)
	dst = appendTok(dst, u.As)
	dst = appendTok(dst, u.Alias)
	dst = appendTok(dst, u.LBrace)
	for _, l := range u.Lines {
		dst = appendNode(dst, l)
	}
	return appendTok(dst, u.RBrace)
}
func (u *UseDecl) Span() token.Span { return spanOf(u) }
func (u *UseDecl) decl()            {}

// Ref is the reference as written: "auth.login", or "create" for a sibling.
func (u *UseDecl) Ref() string {
	if u.Collection.Text == "" {
		return u.Item.Value
	}
	return u.Collection.Value + "." + u.Item.Value
}

// PathPart is one `.name` of a BodySet path.
type PathPart struct{ Dot, Name token.Token }

// BodySet is `body.qty = 5` in a use block: set one field of a literal body.
type BodySet struct {
	Body   token.Token
	Path   []PathPart
	Assign token.Token
	Value  Expr
	Comma  token.Token
}

func (b *BodySet) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, b.Body)
	for _, p := range b.Path {
		dst = appendTok(appendTok(dst, p.Dot), p.Name)
	}
	dst = appendTok(dst, b.Assign)
	dst = appendNode(dst, b.Value)
	return appendTok(dst, b.Comma)
}
func (b *BodySet) Span() token.Span { return spanOf(b) }
func (b *BodySet) stmt()            {}

// Keys is the path as names: body.a.b -> ["a", "b"].
func (b *BodySet) Keys() []string {
	out := make([]string, len(b.Path))
	for i, p := range b.Path {
		out[i] = p.Name.Value
	}
	return out
}

// Drop is `drop expects`.
type Drop struct{ Keyword, What, Comma token.Token }

func (d *Drop) Tokens(dst []token.Token) []token.Token {
	return appendTok(appendTok(appendTok(dst, d.Keyword), d.What), d.Comma)
}
func (d *Drop) Span() token.Span { return spanOf(d) }
func (d *Drop) stmt()            {}

// In is `in "pay" { header "X" = "1" }`: overrides aimed at one step of a flow.
type In struct {
	Keyword token.Token
	Step    token.Token
	LBrace  token.Token
	Lines   []Stmt
	RBrace  token.Token
}

func (n *In) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(appendTok(appendTok(dst, n.Keyword), n.Step), n.LBrace)
	for _, l := range n.Lines {
		dst = appendNode(dst, l)
	}
	return appendTok(dst, n.RBrace)
}
func (n *In) Span() token.Span { return spanOf(n) }
func (n *In) stmt()            {}

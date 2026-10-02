package ast

import "artemis/pkg/dsl/token"

// Expect is `expect <expr>` with an optional `within "10s"`.
//
// One Expect is one assertion in ART-1's result tree, even when its expression
// is an `and` chain: source line to reported assertion stays one to one, and
// two assertions means two expect lines. That is a semantic rule the lowerer
// enforces, recorded here because this node is where it is visible.
//
// Within and Budget are zero and nil when there is no `within` clause. Budget
// is an Expr rather than a string token for uniformity with every other value
// position -- `within "${t}"` needs no grammar change -- and the checker is
// what insists it be a parseable duration.
type Expect struct {
	Keyword token.Token // expect
	Value   Expr
	Within  token.Token // within; zero when absent
	Budget  Expr        // nil when absent
	Comma   token.Token // zero when the separator was a newline
}

func (e *Expect) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, e.Keyword)
	dst = appendNode(dst, e.Value)
	dst = appendTok(dst, e.Within)
	dst = appendNode(dst, e.Budget)
	return appendTok(dst, e.Comma)
}

func (e *Expect) Span() token.Span { return spanOf(e) }
func (e *Expect) stmt()            {}

// Capture is `capture token = body.data.access_token`.
//
// Name is the identifier bound for every later step in the scenario, which is
// what makes it a declaration as much as a statement; the checker adds it to
// the scope of the steps that follow.
type Capture struct {
	Keyword token.Token // capture
	Name    token.Token // the Ident being bound
	Assign  token.Token // =
	Value   Expr
	Comma   token.Token // zero when the separator was a newline
}

func (c *Capture) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, c.Keyword)
	dst = appendTok(dst, c.Name)
	dst = appendTok(dst, c.Assign)
	dst = appendNode(dst, c.Value)
	return appendTok(dst, c.Comma)
}

func (c *Capture) Span() token.Span { return spanOf(c) }
func (c *Capture) stmt()            {}

// Bad is source that did not parse.
//
// It is the price of recovering at statement boundaries rather than giving up:
// the tokens a failed statement consumed have to go somewhere or Source would
// lose them, and a consumer that walks the tree needs to see that something is
// missing here rather than find a gap it cannot explain. A Bad node always
// carries at least one token, because a recovery that consumed nothing would
// loop.
//
// It satisfies every node interface, so one type covers a bad declaration, a
// bad statement, a bad field and a bad expression. The diagnostic explaining
// it is already in the bag; nothing downstream should report on a Bad again.
type Bad struct {
	Toks []token.Token
}

func (b *Bad) Tokens(dst []token.Token) []token.Token { return append(dst, b.Toks...) }
func (b *Bad) Span() token.Span                       { return spanOf(b) }
func (b *Bad) decl()                                  {}
func (b *Bad) stmt()                                  {}
func (b *Bad) action()                                {}
func (b *Bad) expr()                                  {}

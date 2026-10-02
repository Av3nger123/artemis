package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// tryParseAction parses a step's action block if the cursor is on one, and
// reports whether it was.
//
// Which of the three it is decides the step's type, and that is the whole
// mechanism -- there is no `type:` key anywhere in the language. An HTTP verb
// from token.Methods opens an api step, `run` a terminal step, `browser` a
// browser step, and the checker reads nothing but the node's Go type to know
// which roots the step binds.
func (p *parser) tryParseAction() (ast.Action, bool) {
	t := p.peek()
	if t.Kind != token.Ident {
		return nil, false
	}
	switch {
	case token.IsMethod(t.Value):
		return p.parseRequest(), true
	case t.Value == "run":
		return p.parseRun(), true
	case t.Value == "browser":
		return p.parseBrowser(), true
	}
	return nil, false
}

// parseRequest is `Method String [ "{" { ReqField } "}" ]`.
//
// The URL is an Expr, not a string token, because `get "${url}/orders"` is the
// normal case and a URL held in a variable is legal too.
func (p *parser) parseRequest() ast.Action {
	r := &ast.Request{Method: p.next()}
	r.URL = p.parseExpr()

	// The block is optional, and only a brace on the *same line* opens it: a
	// `{` on the next line is the start of something else, and reading it as
	// this request's block would swallow it.
	if p.at(token.LBrace) && p.continues() {
		r.Block = p.parseBlock()
	}
	p.endDecl("a request")
	return r
}

// parseRun is `run Expr [ "{" { RunField } "}" ]`.
func (p *parser) parseRun() ast.Action {
	r := &ast.Run{Keyword: p.next()}
	r.Command = p.parseExpr()

	if p.at(token.LBrace) && p.continues() {
		r.Block = p.parseBlock()
	}
	p.endDecl("a run action")
	return r
}

// parseBrowser is `browser "{" { BrowserAct } "}"`. The block is required:
// a browser step with no actions does nothing at all.
func (p *parser) parseBrowser() ast.Action {
	b := &ast.Browser{Keyword: p.next()}

	open, ok := p.expect(token.LBrace, "\"{\" to open the browser block")
	if !ok {
		return p.badFrom(b.Keyword)
	}
	b.LBrace = open

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		b.Acts = append(b.Acts, p.parseBrowserAct())
		if p.i == before {
			b.Acts = append(b.Acts, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}

	if p.at(token.EOF) {
		p.unclosed(open, "browser block")
	} else {
		b.RBrace = p.next()
	}
	p.endDecl("a browser action block")
	return b
}

// parseBrowserAct is one statement inside a browser block: a name, a target
// expression, and for `fill`, `select` and `upload` a value after an `=`.
//
// Arity is not enforced here. The parser reads whichever shape is written, so
// `click "x" = 1` becomes an act with a value and the checker reports an arity
// error naming `click` -- which is a better diagnostic than a syntax error
// about an unexpected `=`, and the only place the per-action arity table
// belongs.
func (p *parser) parseBrowserAct() ast.Stmt {
	t := p.peek()
	if t.Kind != token.Ident {
		p.errorf(t, diag.UnexpectedToken, "expected a browser action, found %s", describe(t))
		return p.recoverTo()
	}
	if !token.IsBrowserAction(t.Value) {
		// Unknown rather than misshapen, so it gets a did-you-mean against
		// eight short words -- `clcik` is the mistake, and a suggestion fixes
		// it in one click.
		ref := p.errorf(t, diag.UnexpectedToken, "unknown browser action %s", describe(t))
		if !ref.DidYouMean(t.Value, token.BrowserActions) {
			ref.Hintf("a browser block holds: %s", list(token.BrowserActions))
		}
		return p.recoverTo()
	}

	act := &ast.BrowserAct{Name: p.next()}
	act.Target = p.parseExpr()

	if p.at(token.Assign) && p.continues() {
		act.Assign = p.next()
		act.Value = p.parseExpr()
	}
	act.Comma = p.endStatement("a browser action")
	return act
}

// parseBlock is `"{" [ Field { Sep Field } ] "}"`, the body shared by `config
// browser`, a request, a `run`, a `retry` and a `run` block's `env`.
//
// They are one function because they are one shape. Which field names are
// legal in which of them is token/tables.go's business and the checker's, which
// is what lets a misspelled field get a did-you-mean instead of a syntax
// error.
func (p *parser) parseBlock() *ast.Block {
	b := &ast.Block{LBrace: p.next()}
	if !p.enter(b.LBrace) {
		b.Fields = append(b.Fields, p.recoverTo())
		return b
	}
	defer p.leave()

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		b.Fields = append(b.Fields, p.parseField())
		if p.i == before {
			b.Fields = append(b.Fields, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}

	if p.at(token.EOF) {
		p.unclosed(b.LBrace, "block")
		return b
	}
	b.RBrace = p.next()
	return b
}

// parseField is `name = value`, `name "key" = value`, or `name { ... }`.
//
// Any identifier is accepted in the name position. That is the parser's half
// of the shape/name split: `statu = 3` parses into a Field and becomes
// ART-33's unknown-field with a mechanical fix, where rejecting it here would
// leave an author with "unexpected token" and nothing to act on.
func (p *parser) parseField() ast.Stmt {
	name, ok := p.expectIdent("a field name")
	if !ok {
		return p.badFrom()
	}
	f := &ast.Field{Name: name}

	// A string before the `=` is a key: `header "Content-Type" = ...`. Which
	// field names take one is the checker's table, so the parser takes one
	// wherever it is written.
	if p.at(token.String) || p.at(token.StringStart) {
		f.Key = p.parsePostfix()
	}

	switch {
	case p.at(token.LBrace):
		f.Block = p.parseBlock()
	case p.at(token.Assign):
		f.Assign = p.next()
		f.Value = p.parseExpr()
	default:
		p.errorf(p.peek(), diag.UnexpectedToken,
			"expected \"=\" and a value after %q, found %s", name.Value, describe(p.peek()))
		return p.badFrom(fieldTokens(f)...)
	}

	f.Comma = p.endStatement("a field")
	return f
}

// fieldTokens is a half-built field's tokens, so abandoning it keeps its
// bytes.
func fieldTokens(f *ast.Field) []token.Token { return f.Tokens(nil) }

// list joins words for a hint: "goto, click, fill, select, press, hover,
// upload, wait".
func list(words []string) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out
}

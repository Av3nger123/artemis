package parser

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// parseImport is `import String`.
func (p *parser) parseImport() ast.Decl {
	imp := &ast.Import{Keyword: p.next()}
	path, ok := p.expect(token.String, "a file path in quotes")
	if !ok {
		return p.badFrom(imp.Keyword)
	}
	imp.Path = path
	p.endDecl("an import")
	return imp
}

// parseCollection is `collection String "{" { RequestDecl | FlowDecl } "}"`.
func (p *parser) parseCollection() ast.Decl {
	c := &ast.Collection{Keyword: p.next()}
	name, ok := p.expectIdentOrString("a collection name in quotes")
	if !ok {
		return p.badFrom(c.Keyword)
	}
	c.Name = name
	if !isIdentName(name.Value) {
		p.errorf(name, diag.BadCollectionName,
			"a collection name must be an identifier: a use names it as one, so %q could never be used", name.Value).
			Hintf("name it %s", identLike(name.Value))
	}
	open, ok := p.expect(token.LBrace, "\"{\" to open the collection")
	if !ok {
		return p.badFrom(c.Keyword, c.Name)
	}
	c.LBrace = open

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.atWord("request"):
			c.Items = append(c.Items, p.parseRequestDecl())
		case p.atWord("flow"):
			c.Items = append(c.Items, p.parseFlowDecl())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"request\" or \"flow\", found %s", describe(p.peek())).
				DidYouMean(p.peek().Value, token.CollectionItems)
			c.Items = append(c.Items, p.recoverTo())
		}
		if p.i == before {
			c.Items = append(c.Items, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, "collection")
	} else {
		c.RBrace = p.next()
	}
	p.endDecl("a collection")
	return c
}

// parseParams is `"(" [ Param { "," Param } [ "," ] ] ")"`.
//
// It reports false when the list does not close, and the caller abandons the
// declaration into a Bad holding every token read so far.
func (p *parser) parseParams() (*ast.Params, bool) {
	open, ok := p.expect(token.LParen, "\"(\" and the parameters")
	if !ok {
		return nil, false
	}
	ps := &ast.Params{LParen: open}
	p.depth++
	for !p.at(token.RParen) && !p.at(token.EOF) && !p.at(token.LBrace) {
		before := p.i
		prm := &ast.Param{}
		// `secret` is a modifier only when a name follows it, so a parameter
		// may itself be called secret.
		if p.atWord("secret") && p.peekKindAt(1, token.Ident) {
			prm.Secret = p.next()
		}
		name, ok := p.expectIdent("a parameter name")
		if !ok {
			break
		}
		prm.Name = name
		if token.IsUseLine(name.Value) {
			p.errorf(name, diag.ReservedParam,
				"a parameter cannot be named %s: a use block reads %s as an override", name.Value, name.Value).
				Hintf("rename it, for example %s_value", name.Value)
		}
		if p.at(token.Assign) {
			prm.Assign = p.next()
			prm.Default = p.parseExpr()
		}
		prm.Comma = p.eatComma()
		ps.List = append(ps.List, prm)
		// No comma means the list is over; anything but ")" here is reported
		// by the expect below.
		if prm.Comma.Text == "" || p.i == before {
			break
		}
	}
	p.depth--
	rp, ok := p.expect(token.RParen, "\")\" to close the parameters")
	if !ok {
		return ps, false
	}
	ps.RParen = rp
	return ps, true
}

// parseRequestDecl is `request Ident Params "{" Action { StepStmt } "}"`.
//
// The body is parsed by the same loop as a step's, so every rule a step has --
// one action, action first -- holds here with the same diagnostics.
func (p *parser) parseRequestDecl() ast.Decl {
	r := &ast.RequestDecl{Keyword: p.next()}
	name, ok := p.expectIdent("a request name")
	if !ok {
		return p.badFrom(r.Keyword)
	}
	r.Name = name
	params, ok := p.parseParams()
	r.Params = params
	if !ok {
		return p.badFrom(r.Tokens(nil)...)
	}
	if !p.at(token.LBrace) {
		p.errorf(p.peek(), diag.UnexpectedToken, "expected \"{\" to open the request, found %s", describe(p.peek()))
		return p.badFrom(r.Tokens(nil)...)
	}
	step := p.parseStepBody("request", r.Name)
	r.LBrace, r.Action, r.Body, r.RBrace = step.LBrace, step.Action, step.Body, step.RBrace
	p.endDecl("a request")
	return r
}

// parseFlowDecl is `flow Ident Params "{" { StepDecl | UseDecl } "}"`.
func (p *parser) parseFlowDecl() ast.Decl {
	f := &ast.FlowDecl{Keyword: p.next()}
	name, ok := p.expectIdent("a flow name")
	if !ok {
		return p.badFrom(f.Keyword)
	}
	f.Name = name
	params, ok := p.parseParams()
	f.Params = params
	if !ok {
		return p.badFrom(f.Tokens(nil)...)
	}
	open, ok := p.expect(token.LBrace, "\"{\" to open the flow")
	if !ok {
		return p.badFrom(f.Tokens(nil)...)
	}
	f.LBrace = open
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.atWord("step"):
			f.Body = append(f.Body, p.parseStep())
		case p.atWord("use"):
			f.Body = append(f.Body, p.parseUse())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"step\" or \"use\", found %s", describe(p.peek())).
				DidYouMean(p.peek().Value, []string{"step", "use"})
			f.Body = append(f.Body, p.recoverTo())
		}
		if p.i == before {
			f.Body = append(f.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, "flow")
	} else {
		f.RBrace = p.next()
	}
	p.endDecl("a flow")
	return f
}

// parseUse is `use ItemRef [ "as" Ident ] [ "{" { UseLine } "}" ]`.
func (p *parser) parseUse() ast.Decl {
	u := &ast.UseDecl{Keyword: p.next()}
	first, ok := p.expectIdent("what to use, like auth.login")
	if !ok {
		return p.badFrom(u.Keyword)
	}
	if p.at(token.Dot) {
		u.Collection, u.Dot = first, p.next()
		item, ok := p.expectIdent("the request or flow after the dot")
		if !ok {
			return p.badFrom(u.Tokens(nil)...)
		}
		u.Item = item
	} else {
		u.Item = first
	}
	if p.atWord("as") {
		u.As = p.next()
		alias, ok := p.expectIdent("a name after \"as\"")
		if !ok {
			return p.badFrom(u.Tokens(nil)...)
		}
		u.Alias = alias
	}
	if p.at(token.LBrace) {
		u.LBrace, u.Lines, u.RBrace = p.parseUseLines("use")
	}
	p.endDecl("a use")
	return u
}

// parseUseLines reads `"{" { UseLine } "}"`. A line's first word decides what
// it is; any other identifier is an argument, parsed as a field so that
// `user = "alice"` and `header "X" = v` share parseField.
func (p *parser) parseUseLines(what string) (token.Token, []ast.Stmt, token.Token) {
	open := p.next()
	if !p.enter(open) {
		return open, []ast.Stmt{p.recoverTo()}, token.Token{}
	}
	defer p.leave()

	var lines []ast.Stmt
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.atWord("expect"):
			lines = append(lines, p.parseExpect())
		case p.atWord("drop"):
			lines = append(lines, p.parseDrop())
		case p.atWord("in") && p.peekKindAt(1, token.String):
			lines = append(lines, p.parseIn())
		case p.atWord("body") && p.peekKindAt(1, token.Dot):
			lines = append(lines, p.parseBodySet())
		case p.at(token.Ident):
			lines = append(lines, p.parseField())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected an argument or an override, found %s", describe(p.peek())).
				Hintf("a use block holds: name = value, header, query, body, drop expects, drop captures, expect, in \"step\" { ... }")
			lines = append(lines, p.recoverTo())
		}
		if p.i == before {
			lines = append(lines, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, what+" block")
		return open, lines, token.Token{}
	}
	return open, lines, p.next()
}

func (p *parser) parseDrop() ast.Stmt {
	d := &ast.Drop{Keyword: p.next()}
	what, ok := p.expectIdent("\"expects\" or \"captures\" after \"drop\"")
	if !ok {
		return p.badFrom(d.Keyword)
	}
	if !token.IsDropTarget(what.Value) {
		p.errorf(what, diag.UnexpectedToken, "expected \"expects\" or \"captures\" after \"drop\", found %s", describe(what)).
			DidYouMean(what.Value, token.DropTargets)
	}
	d.What = what
	d.Comma = p.endStatement("a drop")
	return d
}

func (p *parser) parseIn() ast.Stmt {
	n := &ast.In{Keyword: p.next(), Step: p.next()}
	if !p.at(token.LBrace) {
		p.errorf(p.peek(), diag.UnexpectedToken, "expected \"{\" after the step name, found %s", describe(p.peek()))
		return p.badFrom(n.Keyword, n.Step)
	}
	n.LBrace, n.Lines, n.RBrace = p.parseUseLines("in")
	// endDecl, not endStatement: In has no Comma field, so a comma after it is
	// reported and left for the enclosing loop's recovery to keep.
	p.endDecl("an in block")
	return n
}

func (p *parser) parseBodySet() ast.Stmt {
	b := &ast.BodySet{Body: p.next()}
	for p.at(token.Dot) {
		dot := p.next()
		name, ok := p.expectIdent("a field name after \".\"")
		if !ok {
			return p.badFrom(append(b.Tokens(nil), dot)...)
		}
		b.Path = append(b.Path, ast.PathPart{Dot: dot, Name: name})
	}
	assign, ok := p.expect(token.Assign, "\"=\" and a value")
	if !ok {
		return p.badFrom(b.Tokens(nil)...)
	}
	b.Assign = assign
	b.Value = p.parseExpr()
	b.Comma = p.endStatement("a body field")
	return b
}

// isIdentName reports whether s is spelled like an Ident: a letter or _
// followed by letters, digits and _.
func isIdentName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// identLike is s made into an identifier for a hint: every other character
// becomes _, and a leading digit gets a _ in front. "my-coll" is my_coll.
func identLike(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i == 0 && '0' <= r && r <= '9' {
			b.WriteByte('_')
		}
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

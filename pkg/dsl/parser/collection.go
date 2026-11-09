package parser

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// parseImport is `import String [ "as" Ident ]`.
func (p *parser) parseImport() ast.Decl {
	imp := &ast.Import{Keyword: p.next()}
	path, ok := p.expect(token.String, "a file path in quotes")
	if !ok {
		return p.badFrom(imp.Keyword)
	}
	imp.Path = path
	if p.atWord("as") {
		imp.As = p.next()
		name, ok := p.expectIdent("the name a use reads it by, after \"as\"")
		if !ok {
			return p.badFrom(imp.Tokens(nil)...)
		}
		imp.Name = name
	}
	p.endDecl("an import")
	return imp
}

// parseCollection is `collection String "{" { ItemDecl } "}"`.
func (p *parser) parseCollection() ast.Decl {
	c := &ast.Collection{Keyword: p.next()}
	name, ok := p.expectIdentOrString("a collection id, like orders")
	if !ok {
		return p.badFrom(c.Keyword)
	}
	c.Name = name
	c.LabelAs, c.Label = p.parseLabel()
	open, ok := p.expect(token.LBrace, "\"{\" to open the collection")
	if !ok {
		return p.badFrom(c.Keyword, c.Name, c.LabelAs, c.Label)
	}
	c.LBrace = open

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.at(token.Ident) && token.IsCollectionItem(p.peek().Value):
			c.Items = append(c.Items, p.parseItemDecl())
		case p.atWord("flow"):
			// A collection is single requests; steps in order are a scenario's.
			p.errorf(p.peek(), diag.UnexpectedToken, "a collection holds requests, one action each: steps in order are a scenario").
				Hintf("write these steps in a scenario, using the requests one by one")
			c.Items = append(c.Items, p.recoverTo())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"request\", \"command\" or \"query\", found %s", describe(p.peek())).
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

// parseItemDecl is
// `( "request" | "command" | "query" ) Ident [ Params ] "{" { VarDecl } Action { StepStmt } "}"`.
//
// The keyword is the item's type. An item with no parameter list declares its
// inputs as the var lines above its action; one with a list is the older form
// and reads no vars there. The body is parsed by the same loop as a step's,
// so every rule a step has -- one action, action first -- holds here with the
// same diagnostics, and then the action is held to the keyword.
func (p *parser) parseItemDecl() ast.Decl {
	r := &ast.RequestDecl{Keyword: p.next()}
	what := r.Keyword.Value
	name, ok := p.expectIdent("a " + what + " name")
	if !ok {
		return p.badFrom(r.Keyword)
	}
	r.Name = name
	r.LabelAs, r.Label = p.parseLabel()
	if p.at(token.LParen) {
		params, ok := p.parseParams()
		r.Params = params
		if !ok {
			return p.badFrom(r.Tokens(nil)...)
		}
	}
	if !p.at(token.LBrace) {
		p.errorf(p.peek(), diag.UnexpectedToken, "expected \"{\" to open the %s, found %s", what, describe(p.peek()))
		return p.badFrom(r.Tokens(nil)...)
	}
	var head func(*ast.StepDecl)
	if r.Params == nil {
		head = func(s *ast.StepDecl) { p.parseInputs(r, s) }
	}
	step := p.parseStepBodyWith(what, r.Name, head)
	r.LBrace, r.Action, r.Body, r.RBrace = step.LBrace, step.Action, step.Body, step.RBrace
	p.checkItemAction(r)
	p.endDecl("a " + what)
	return r
}

// parseInputs reads an item's input vars, `var` and `private var` lines,
// until the first line that is neither. A var that does not parse ends them:
// its Bad goes to the step body, s, which keeps every byte in source order.
func (p *parser) parseInputs(r *ast.RequestDecl, s *ast.StepDecl) {
	for p.atWord("var") || p.atWord("private") && p.peekWordAt(1, "var") {
		d := p.parseVar()
		v, ok := d.(*ast.VarDecl)
		if !ok {
			s.Body = append(s.Body, &ast.Bad{Toks: d.Tokens(nil)})
			return
		}
		if token.IsUseLine(v.Name.Value) {
			p.errorf(v.Name, diag.ReservedParam,
				"an input cannot be named %s: a step that calls the item reads %s as an override", v.Name.Value, v.Name.Value).
				Hintf("rename it, for example %s_value", v.Name.Value)
		}
		r.Inputs = append(r.Inputs, v)
	}
}

// itemActions is, for each step type, the item keyword that holds it and the
// words that open its action, as a hint writes them.
var itemActions = map[string]struct{ keyword, words string }{
	"api":      {"request", "get, post, put, patch or delete"},
	"terminal": {"command", "run"},
	"db":       {"query", "db"},
	"browser":  {"", "browser"},
}

// checkItemAction reports an item whose action is not the type its keyword
// names: `run` in a request, `get` in a command.
func (p *parser) checkItemAction(r *ast.RequestDecl) {
	got := actionType(r.Action)
	want := r.Type()
	if got == "" || got == want {
		return
	}
	word := tokenAt(r.Action)
	ref := p.errorf(word, diag.ItemActionMismatch,
		"a %s holds %s %s action, and this one is %s", r.Keyword.Value, article(want), want, word.Value)
	if kw := itemActions[got].keyword; kw != "" {
		ref.Hintf("make it a %s, or use %s", kw, itemActions[want].words)
	} else {
		ref.Hintf("use %s", itemActions[want].words)
	}
}

// actionType is the step type an action makes, "" for no action.
func actionType(a ast.Action) string {
	if isNilAction(a) {
		return ""
	}
	switch a.(type) {
	case *ast.Request:
		return "api"
	case *ast.Run:
		return "terminal"
	case *ast.DB:
		return "db"
	case *ast.Browser:
		return "browser"
	}
	return ""
}

// article is "an" before a word that starts with a vowel, else "a".
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// parseUse is `use ItemRef [ "as" Ident ] [ "{" { UseLine } "}" ]`.
func (p *parser) parseUse() ast.Decl {
	u := &ast.UseDecl{Keyword: p.next()}
	if !p.parseItemRef(u, "what to use, like auth.login") {
		return p.badFrom(u.Tokens(nil)...)
	}
	if p.at(token.LBrace) {
		u.LBrace, u.Lines, u.RBrace = p.parseUseLines("use")
	}
	p.endDecl("a use")
	return u
}

// parseStepCall is the rest of `step String "=" ItemRef [ "as" Ident ] "{"
// { UseLine } "}"`, once parseStep has read the keyword and the name and seen
// the "=": a step that calls a collection item. It is the same node as a use,
// with the step's keyword, name and "=" in place of `use`.
func (p *parser) parseStepCall(kw, name, labelAs, label token.Token) ast.Decl {
	u := &ast.UseDecl{Step: kw, Name: name, LabelAs: labelAs, Label: label, Assign: p.next()}
	if !p.parseItemRef(u, "the item the step calls, like auth.login") {
		return p.badFrom(u.Tokens(nil)...)
	}
	if p.at(token.LBrace) {
		u.LBrace, u.Lines, u.RBrace = p.parseUseLines("step")
	} else {
		// The step keeps what it parsed, and whatever follows is the next
		// line's to read: recovering here would take that line with it.
		p.errorf(p.peek(), diag.UnexpectedToken,
			"expected \"{\" after %s, found %s", u.Ref(), describe(p.peek())).
			Hintf("a step that calls an item has a block, even an empty one: step %s = %s { }", name.Text, u.Ref())
	}
	p.endDecl("a step")
	return u
}

// parseItemRef is `ItemRef [ "as" Ident ]` into u: `auth.login`, or a bare
// `login` for a sibling, then the alias. what describes the reference for the
// diagnostic when there is none. It reports false having consumed only what
// u now holds, and the caller abandons the declaration into a Bad.
func (p *parser) parseItemRef(u *ast.UseDecl, what string) bool {
	first, ok := p.expectIdent(what)
	if !ok {
		return false
	}
	if p.at(token.Dot) {
		u.Collection, u.Dot = first, p.next()
		item, ok := p.expectIdent("the request after the dot")
		if !ok {
			return false
		}
		u.Item = item
	} else {
		u.Item = first
	}
	if p.atWord("as") {
		u.As = p.next()
		alias, ok := p.expectIdent("a name after \"as\"")
		if !ok {
			return false
		}
		u.Alias = alias
	}
	return true
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
	p.useLines++
	defer func() { p.useLines-- }()

	var lines []ast.Stmt
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.atWord("expect"):
			lines = append(lines, p.parseExpect())
		case p.atWord("drop"):
			lines = append(lines, p.parseDrop())
		case p.atWord("body") && p.peekKindAt(1, token.Dot):
			lines = append(lines, p.parseBodySet())
		case p.at(token.Ident):
			lines = append(lines, p.parseField())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected an argument or an override, found %s", describe(p.peek())).
				Hintf("%s", useLinesHint(what))
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

// useLinesHint is what a use block, or a step that calls an item, may hold.
func useLinesHint(what string) string {
	if what == "step" {
		return "a step that calls an item holds: name = value, header, query, body, drop expects, drop vars, expect"
	}
	return "a use block holds: name = value, header, query, body, drop expects, drop captures, expect"
}

func (p *parser) parseDrop() ast.Stmt {
	d := &ast.Drop{Keyword: p.next()}
	what, ok := p.expectIdent("\"expects\", \"captures\" or \"vars\" after \"drop\"")
	if !ok {
		return p.badFrom(d.Keyword)
	}
	if !token.IsDropTarget(what.Value) {
		p.errorf(what, diag.UnexpectedToken, "expected \"expects\", \"captures\" or \"vars\" after \"drop\", found %s", describe(what)).
			DidYouMean(what.Value, token.DropTargets)
	}
	d.What = what
	d.Comma = p.endStatement("a drop")
	return d
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

// IsIdentName reports whether s can be what a use names a collection by.
func IsIdentName(s string) bool { return isIdentName(s) }

// ImportName is the name `import "<path>"` with no `as` gives the file's
// collection: the file's name less .art, and less a .collection before it
// (orders.art and orders.collection.art are both orders). ok is false when
// that cannot be a use's, and the import needs an `as`.
func ImportName(path string) (name string, ok bool) {
	base := path[strings.LastIndexAny(path, "/\\")+1:]
	name = strings.TrimSuffix(strings.TrimSuffix(base, ".art"), ".collection")
	return name, isIdentName(name)
}

// IdentLike is s made into an identifier, for a hint.
func IdentLike(s string) string { return identLike(s) }

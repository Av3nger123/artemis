package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// parseStepStmt is one statement in a step after its action: `expect`,
// `capture`, or a field -- `timeout = "5s"` and `retry { ... }` are fields of
// the step, which is why ast.Field is a Stmt.
func (p *parser) parseStepStmt() ast.Stmt {
	switch {
	case p.atWord("expect"):
		return p.parseExpect()
	case p.atWord("capture"), p.atWord("secret") && p.peekWordAt(1, "capture"):
		return p.parseCapture()
	}

	// `secret` before a word it cannot modify gets its own message, and has to
	// get it here: the field fallback below would otherwise read `secret` as a
	// field name and report a bad field, which names the wrong thing.
	if p.atWord("secret") {
		at := p.next()
		p.errorf(p.peek(), diag.UnexpectedToken,
			"expected \"capture\" after \"secret\", found %s", describe(p.peek())).
			DidYouMean(p.peek().Value, []string{"capture"})
		return p.badFrom(at)
	}

	// Anything else identifier-shaped is read as a field, so `timeout`,
	// `retry` and a misspelling of either all take the same path and the
	// checker names what is wrong with the name.
	if p.at(token.Ident) {
		return p.parseField()
	}

	ref := p.errorf(p.peek(), diag.UnexpectedToken,
		"expected a step statement, found %s", describe(p.peek()))
	ref.Hintf("a step holds an action block and then: %s", list(token.StepStatements))
	return p.recoverTo()
}

// parseExpect is `expect Expr [ "within" String ]`.
//
// One expect is one assertion in the result tree, even when its expression is
// an `and` chain: source line to reported assertion stays one to one, which is
// what preserves ART-1's granularity. Two assertions means two expect lines.
func (p *parser) parseExpect() ast.Stmt {
	e := &ast.Expect{Keyword: p.next()}
	e.Value = p.parseExpr()

	// `within` is an ordinary identifier -- the grammar has no keywords, so
	// `capture within = ...` is a legal line -- and it is this expect's only
	// if it is on the same line.
	if p.atWord("within") && p.continues() {
		e.Within = p.next()
		e.Budget = p.parseExpr()
	}

	e.Comma = p.endStatement("an expect")
	return e
}

// parseCapture is `[ "secret" ] "capture" <ident> = Expr`.
//
// The name is bound for every later step in the scenario, which the checker
// adds to their scope; nothing here resolves it.
func (p *parser) parseCapture() ast.Stmt {
	c := &ast.Capture{}
	if p.atWord("secret") {
		c.Secret = p.next()
	}
	c.Keyword = p.next()

	name, ok := p.expectIdent("a name to capture into")
	if !ok {
		return p.badFrom(c.Secret, c.Keyword)
	}
	c.Name = name

	assign, ok := p.expect(token.Assign, "\"=\" and an expression")
	if !ok {
		return p.badFrom(c.Secret, c.Keyword, c.Name)
	}
	c.Assign = assign
	c.Value = p.parseExpr()
	c.Comma = p.endStatement("a capture")
	return c
}

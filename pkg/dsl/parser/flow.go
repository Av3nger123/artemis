package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// flowWords are the words that open a control-flow block where a step could
// be written. Each is positional: `var repeat = 1` is still a var.
var flowWords = token.Flows

// atFlow reports whether the cursor is on a word that opens a block.
func (p *parser) atFlow() bool {
	for _, w := range flowWords {
		if p.atWord(w) {
			return true
		}
	}
	return false
}

// parseFlow is one control-flow block:
//
//	Flow   = "parallel" Body
//	       | "if" Expr Body { "else" "if" Expr Body } [ "else" Body ]
//	       | "switch" Expr "{" { "case" Expr Body } [ "default" Body ] "}"
//	       | "try" Body [ "catch" Body ] [ "finally" Body ]
//	       | "repeat" { Option } Body "until" Expr ;
//	Option = ( "every" | "within" | "max" ) Expr ;
//	Body   = "{" { StepDecl | UseDecl | Flow } "}" ;
func (p *parser) parseFlow() ast.Decl {
	f := &ast.FlowDecl{}
	switch kw := p.peek().Value; kw {
	case ast.FlowParallel:
		f.Clauses = append(f.Clauses, p.parseClause(false))
	case ast.FlowIf:
		f.Clauses = append(f.Clauses, p.parseClause(true))
		for p.atWord("else") {
			c := &ast.Clause{Keyword: p.next()}
			if p.atWord("if") {
				c.If = p.next()
				c.Expr = p.parseExpr()
			}
			p.parseBody(c)
			f.Clauses = append(f.Clauses, c)
			if c.If.Text == "" {
				break // an else with no if is the last clause
			}
		}
	case ast.FlowSwitch:
		f.Clauses = append(f.Clauses, p.parseSwitch())
	case ast.FlowTry:
		f.Clauses = append(f.Clauses, p.parseClause(false))
		for _, w := range []string{"catch", "finally"} {
			if p.atWord(w) {
				f.Clauses = append(f.Clauses, p.parseClause(false))
			}
		}
		if len(f.Clauses) == 1 {
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"catch\" or \"finally\" after a try block, found %s", describe(p.peek())).
				Hintf("try { ... } catch { ... } finally { ... }")
		}
	case ast.FlowRepeat:
		c := &ast.Clause{Keyword: p.next()}
		for p.atWord("every") || p.atWord("within") || p.atWord("max") {
			o := &ast.Option{Name: p.next()}
			o.Value = p.parseExpr()
			c.Options = append(c.Options, o)
		}
		p.parseBody(c)
		f.Clauses = append(f.Clauses, c)
		if !p.atWord("until") {
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"until\" and the condition that ends the repeat, found %s", describe(p.peek())).
				Hintf(`repeat every "2s" within "2m" { ... } until state == "done"`)
			return f
		}
		f.Until = p.next()
		f.Cond = p.parseExpr()
	default:
		p.errorf(p.peek(), diag.UnexpectedToken, "expected a block, found %s", describe(p.peek()))
		return p.recoverTo()
	}
	p.endDecl("a " + f.Kind() + " block")
	return f
}

// parseClause is a keyword, an expression when withExpr, and a body.
func (p *parser) parseClause(withExpr bool) *ast.Clause {
	c := &ast.Clause{Keyword: p.next()}
	if withExpr {
		c.Expr = p.parseExpr()
	}
	p.parseBody(c)
	return c
}

// parseSwitch is `switch Expr { case Expr Body ... default Body }`. Its body
// holds the case clauses, which hold the steps.
func (p *parser) parseSwitch() *ast.Clause {
	s := &ast.Clause{Keyword: p.next()}
	s.Expr = p.parseExpr()
	open, ok := p.expect(token.LBrace, "\"{\" to open the switch")
	if !ok {
		return s
	}
	s.LBrace = open
	if !p.enter(open) {
		s.Body = append(s.Body, p.recoverTo())
		return s
	}
	defer p.leave()
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		switch {
		case p.atWord("case"):
			s.Body = append(s.Body, p.parseClause(true))
		case p.atWord("default"):
			s.Body = append(s.Body, p.parseClause(false))
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"case\" or \"default\" in a switch, found %s", describe(p.peek())).
				Hintf(`case "pro" { ... }`)
			s.Body = append(s.Body, p.recoverTo())
		}
		if p.i == before {
			s.Body = append(s.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, "switch")
		return s
	}
	s.RBrace = p.next()
	return s
}

// parseBody reads a clause's `{ ... }`: steps, uses and nested blocks.
func (p *parser) parseBody(c *ast.Clause) {
	open, ok := p.expect(token.LBrace, "\"{\" to open the "+c.Keyword.Value+" block")
	if !ok {
		return
	}
	c.LBrace = open
	if !p.enter(open) {
		c.Body = append(c.Body, p.recoverTo())
		return
	}
	defer p.leave()
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		c.Body = append(c.Body, p.parseFlowItem(c.Keyword.Value))
		if p.i == before {
			c.Body = append(c.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, c.Keyword.Value+" block")
		return
	}
	c.RBrace = p.next()
}

// parseFlowItem is one thing a block's body holds: a step, a use, or another
// block.
func (p *parser) parseFlowItem(in string) ast.Decl {
	switch {
	case p.atWord("step"):
		return p.parseStep()
	case p.atWord("use"):
		return p.parseUse()
	case p.atFlow():
		return p.parseFlow()
	}
	p.errorf(p.peek(), diag.UnexpectedToken,
		"expected a step, a use or a block in %s, found %s", in, describe(p.peek())).
		Hintf("a block holds steps and other blocks; a var belongs to the scenario")
	return p.recoverTo()
}

package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// A file that uses the word flow at its top level is one of two new kinds.
//
// A flow file is its imports and one `flow checkout { ... }`, whose body is
// what a scenario's body holds. A scenario file is `flow "../flows/x.art"` on
// its first line, then scenarios that hold data only: `name = value` lines and
// tags. The two are told apart by what follows the word, an Ident or a String,
// which is why a flow's name is an identifier and not a quoted title.
//
// What a file of either kind may not hold is reported here, as shape: a
// second flow, a header below the first line, a collection or a scenario in a
// flow file, and a step in a data scenario. Whether a set names a var of the
// flow is a name fault and belongs to expansion, which reads the flow file.

// parseFlowHead is either form opened by the word flow at the top level. first
// is whether nothing came before it in the file.
func (p *parser) parseFlowHead(first bool) ast.Decl {
	next := p.peekAt(1)
	switch {
	case next.Kind == token.String:
		return p.parseFlowRef(first)
	case next.Kind == token.Ident && !next.StartsLine():
		return p.parseFlowDecl()
	}
	kw := p.next()
	p.errorf(p.peek(), diag.UnexpectedToken,
		"expected a flow name or the path of a flow file after \"flow\", found %s", describe(p.peek())).
		Hintf(`flow checkout { ... } declares a flow; flow "../flows/checkout.art" starts a scenario file`)
	return p.badFrom(kw)
}

// parseFlowRef is `flow "<path>"`, the header of a scenario file. The first
// header makes the scenarios after it data scenarios, even when it is not on
// the first line: one diagnostic about where it stands is more use than one
// per line of every scenario below it.
func (p *parser) parseFlowRef(first bool) ast.Decl {
	r := &ast.FlowRef{Keyword: p.next(), Path: p.next()}
	switch {
	case p.header != nil:
		p.errorf(r.Keyword, diag.SecondFlow,
			"this file already names its flow, on line %d", p.header.Keyword.Span.Line).
			Hintf("a scenario file runs one flow; put the scenarios of %s in a file of their own", r.Path.Text)
	case p.flow != nil:
		p.errorf(r.Keyword, diag.SecondFlow,
			"this file already has the flow %s, on line %d", p.flow.Name.Text, p.flow.Keyword.Span.Line).
			Hintf("a flow file holds one flow and its imports; flow %s starts a scenario file of its own", r.Path.Text)
	case !first:
		p.errorf(r.Keyword, diag.FlowRefPlacement,
			"flow %s must be the first line of the file", r.Path.Text).
			Hintf("move it above everything else; the scenarios below it run the flow it names")
	}
	if p.header == nil && p.flow == nil {
		p.header = r
		p.scenarioFile = true
	}
	p.endDecl("the flow header")
	return r
}

// parseFlowDecl is `flow <ident> { ... }`.
func (p *parser) parseFlowDecl() ast.Decl {
	f := &ast.Flow{Keyword: p.next(), Name: p.next()}
	switch {
	case p.flow != nil:
		p.errorf(f.Keyword, diag.SecondFlow,
			"this file already has the flow %s, on line %d", p.flow.Name.Text, p.flow.Keyword.Span.Line).
			Hintf("a flow file holds one flow; put %s in a file of its own", f.Name.Text)
	case p.header != nil:
		p.errorf(f.Keyword, diag.SecondFlow,
			"this file already names its flow, on line %d", p.header.Keyword.Span.Line).
			Hintf("a scenario file runs the flow it names; put %s in a flow file of its own", f.Name.Text)
	default:
		p.flow = f
	}

	open, ok := p.expect(token.LBrace, "\"{\" to open the flow")
	if !ok {
		return p.badFrom(f.Keyword, f.Name)
	}
	f.LBrace = open
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		f.Body = append(f.Body, p.parseScenarioDecl())
		if p.i == before {
			f.Body = append(f.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, "flow")
		return f
	}
	f.RBrace = p.next()
	return f
}

// parseDataScenario is `scenario "name" { { Set | TagsDecl } }`, a scenario in
// a scenario file. Sets may share a line, `{ quantity = 3  coupon = "TEN" }`,
// because a value ends where the next name begins.
func (p *parser) parseDataScenario() ast.Decl {
	s := &ast.Scenario{Keyword: p.next()}
	name, ok := p.expectIdentOrString("a scenario name in quotes")
	if !ok {
		return p.badFrom(s.Keyword)
	}
	s.Name = name

	open, ok := p.expect(token.LBrace, "\"{\" to open the scenario")
	if !ok {
		return p.badFrom(s.Keyword, s.Name)
	}
	s.LBrace = open
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		s.Body = append(s.Body, p.parseDataLine())
		if p.i == before {
			s.Body = append(s.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}
	if p.at(token.EOF) {
		p.unclosed(open, "scenario")
		return s
	}
	s.RBrace = p.next()
	return s
}

// parseDataLine is one line of a data scenario: tags, a set, or a Bad.
func (p *parser) parseDataLine() ast.Decl {
	switch {
	case p.atWord("tags") && p.peekKindAt(1, token.Assign):
		d := &ast.TagsDecl{Keyword: p.next(), Assign: p.next()}
		d.Value = p.parseExpr()
		return d
	case p.at(token.Ident) && p.peekKindAt(1, token.Assign):
		s := &ast.Set{Name: p.next(), Assign: p.next()}
		s.Value = p.parseExpr()
		return s
	case p.at(token.Ident) && !dataOnlyWords[p.peek().Value]:
		// A name the language gives no other meaning, with no "=" after it:
		// the author meant a set and left out the "=".
		t := p.peek()
		p.errorf(p.peekAt(1), diag.UnexpectedToken,
			"expected \"=\" and a value after %s, found %s", t.Text, describe(p.peekAt(1))).
			Hintf("a scenario sets a var of its flow: %s = <value>", t.Text)
		return p.recoverTo()
	}
	p.errorf(p.peek(), diag.DataOnlyScenario, "a scenario holds data only: name = value lines and tags").
		Hintf("put steps in the flow, %s", p.header.Path.Value)
	return p.recoverTo()
}

// dataOnlyWords are the words that open a line of a flow or a step, which a
// data scenario does not hold. Seeing one is data-only-scenario rather than a
// set that lost its "=".
var dataOnlyWords = func() map[string]bool {
	// The old forms' words too, so a capture or a use written in a data
	// scenario is told it holds data only.
	out := map[string]bool{"secret": true, "private": true, "capture": true, "use": true, "suite": true}
	for _, table := range [][]string{
		token.Blocks, token.BlockWords, token.StepStatements,
		token.Methods, token.Actions, token.UseLines,
	} {
		for _, w := range table {
			out[w] = true
		}
	}
	return out
}()

// flowFileHolds reports each collection and scenario in a flow file. It runs
// once the whole file is read, because either can stand above the flow.
func (p *parser) flowFileHolds(file *ast.File) {
	if p.flow == nil || p.header != nil {
		return
	}
	const msg = "a flow file holds one flow and its imports, and this is a %s"
	for _, d := range file.Scenarios {
		switch v := d.(type) {
		case *ast.Collection:
			p.errorf(v.Keyword, diag.FlowFileHolds, msg, "collection").
				Hintf("put collection %s in a file of its own, and import it here", v.Name.Text)
		case *ast.Scenario:
			if v.Keyword.Value == "suite" {
				p.errorf(v.Keyword, diag.FlowFileHolds, msg, "suite").
					Hintf("a suite goes in its folder's suite.art")
				continue
			}
			p.errorf(v.Keyword, diag.FlowFileHolds, msg, "scenario").
				Hintf("put it in a scenario file whose first line is flow \"<path to this file>\"")
		}
	}
}

// peekAt is the nth token after the cursor, counting the cursor itself as
// zero and skipping markers as peekWordAt does. Past the end it is EOF.
func (p *parser) peekAt(n int) token.Token {
	seen := 0
	for i := p.i; i < len(p.toks); i++ {
		if p.toks[i].IsMarker() {
			continue
		}
		if seen == n {
			return p.toks[i]
		}
		seen++
	}
	return p.toks[len(p.toks)-1]
}

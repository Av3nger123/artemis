package parser

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// parseFile is `File = { Import } { Collection | Scenario }`.
//
// An empty file is valid and yields a File with no scenarios. Anything at the
// top level that is none of the three is reported once and recovered past, so
// a stray line above a scenario does not cost the scenario. An import below a
// collection or a scenario still parses, and is reported where it stands.
func (p *parser) parseFile() *ast.File {
	file := &ast.File{}
	seenBody := false

	for !p.at(token.EOF) {
		before := p.i

		switch {
		case p.atWord("import"):
			imp := p.parseImport()
			if seenBody {
				p.errorf(tokenAt(imp), diag.ImportPlacement,
					"an import must come before every collection and scenario").
					Hintf("move it to the top of the file")
			}
			file.Scenarios = append(file.Scenarios, imp)
		case p.atWord("collection"):
			seenBody = true
			file.Scenarios = append(file.Scenarios, p.parseCollection())
		case p.atWord("scenario"):
			seenBody = true
			file.Scenarios = append(file.Scenarios, p.parseScenario())
		default:
			p.errorf(p.peek(), diag.UnexpectedToken,
				"expected \"import\", \"collection\" or \"scenario\", found %s", describe(p.peek())).
				Hintf("a file holds imports, then collections and scenarios")
			file.Scenarios = append(file.Scenarios, p.recoverTo())
		}

		// recoverTo always consumes a token when there is one, and
		// parseScenario consumes its keyword, so this cannot spin -- but the
		// loop asserts it rather than trusting it, because a hang is as much
		// a fuzz failure as a panic.
		if p.i == before {
			file.Scenarios = append(file.Scenarios, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}

	file.EOF = p.peek()
	return file
}

// parseScenario is `scenario "name" { { ConfigDecl | VarDecl | StepDecl } }`.
func (p *parser) parseScenario() ast.Decl {
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

	s.Body, s.RBrace = p.parseScenarioBody(open)
	return s
}

// parseScenarioBody reads declarations until the closing brace.
func (p *parser) parseScenarioBody(open token.Token) ([]ast.Decl, token.Token) {
	var body []ast.Decl

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i
		body = append(body, p.parseScenarioDecl())
		if p.i == before {
			body = append(body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}

	if p.at(token.EOF) {
		p.unclosed(open, "scenario")
		return body, token.Token{}
	}
	return body, p.next()
}

// parseScenarioDecl is one of the four declarations a scenario body holds.
func (p *parser) parseScenarioDecl() ast.Decl {
	switch {
	case p.atWord("config"):
		return p.parseConfig()
	case p.atWord("var"), p.atWord("secret") && p.peekWordAt(1, "var"):
		return p.parseVar()
	case p.atWord("step"):
		return p.parseStep()
	case p.atWord("use"):
		return p.parseUse()
	}

	// `secret` before a word it cannot modify gets its own message. Falling
	// through would suggest "config", "var", "step" or "use" in place of `secret`
	// itself, which is not what the author got wrong.
	if p.atWord("secret") {
		at := p.next()
		p.errorf(p.peek(), diag.UnexpectedToken,
			"expected \"var\" or \"capture\" after \"secret\", found %s", describe(p.peek())).
			DidYouMean(p.peek().Value, []string{"var", "capture"})
		return p.badFrom(at)
	}

	p.errorf(p.peek(), diag.UnexpectedToken,
		"expected \"config\", \"var\", \"step\" or \"use\", found %s", describe(p.peek())).
		DidYouMean(p.peek().Value, []string{"config", "var", "step", "use"})
	return p.recoverTo()
}

// parseConfig is `config <ident> { Setting... }`.
//
// The subject is any identifier, not just `browser`: the checker rejects the
// rest by name, with a did-you-mean, where a syntax error here would have
// nothing to suggest.
func (p *parser) parseConfig() ast.Decl {
	c := &ast.ConfigDecl{Keyword: p.next()}

	subject, ok := p.expectIdent("what to configure, like \"browser\"")
	if !ok {
		return p.badFrom(c.Keyword)
	}
	c.Subject = subject

	if !p.at(token.LBrace) {
		p.errorf(p.peek(), diag.UnexpectedToken,
			"expected \"{\" to open the config block, found %s", describe(p.peek()))
		return p.badFrom(c.Keyword, c.Subject)
	}
	c.Block = p.parseBlock()
	p.endDecl("a config block")
	return c
}

// parseVar is `[ "secret" ] "var" <ident> = Expr`.
//
// badFrom already drops a zero token, so the optional modifier needs no branch
// on the recovery paths.
func (p *parser) parseVar() ast.Decl {
	v := &ast.VarDecl{}
	if p.atWord("secret") {
		v.Secret = p.next()
	}
	v.Keyword = p.next()

	name, ok := p.expectIdent("a variable name")
	if !ok {
		return p.badFrom(v.Secret, v.Keyword)
	}
	v.Name = name

	assign, ok := p.expect(token.Assign, "\"=\" and a value")
	if !ok {
		return p.badFrom(v.Secret, v.Keyword, v.Name)
	}
	v.Assign = assign
	v.Value = p.parseExpr()
	p.endDecl("a var declaration")
	return v
}

// parseStep is `step "name" { Action StepStmt... }`.
//
// The body is read as a flat sequence and the shape rules are reported
// afterwards -- missing-action, duplicate-action, action-not-first -- rather
// than being enforced by the descent. A strict `Action` then `{ StepStmt }`
// turns a step whose author wrote `expect` above `get` into a cascade of
// unexpected tokens from the first line onward; this way one mistake produces
// one diagnostic and the rest of the step still lands in the tree.
func (p *parser) parseStep() ast.Decl {
	s := &ast.StepDecl{Keyword: p.next()}

	name, ok := p.expectIdentOrString("a step name in quotes")
	if !ok {
		return p.badFrom(s.Keyword)
	}

	body := p.parseStepBody("step", name)
	if body == nil {
		return p.badFrom(s.Keyword, name)
	}
	body.Keyword, body.Name = s.Keyword, name
	p.endDecl("a step")
	return body
}

// parseStepBody is a step's `"{" Action { StepStmt } "}"`, shared with a
// collection's request declaration so both hold the same shape rules with the
// same diagnostics. what is "step" or "request", and name is what the
// missing-action diagnostic points at. It returns nil, having consumed
// nothing, when there is no "{".
func (p *parser) parseStepBody(what string, name token.Token) *ast.StepDecl {
	s := &ast.StepDecl{}
	open, ok := p.expect(token.LBrace, "\"{\" to open the "+what)
	if !ok {
		return nil
	}
	s.LBrace = open

	var firstAction token.Span // where the action block already seen began
	sawStmt := false

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i

		if action, isAction := p.tryParseAction(); isAction {
			switch {
			case firstAction.IsZero():
				firstAction = action.Span()
				s.Action = action
				if sawStmt {
					p.errorf(tokenAt(action), diag.ActionNotFirst,
						"a step's action block must come before its statements").
						Hintf("move this above the expect, capture, retry and timeout lines")
				}
			default:
				p.errorf(tokenAt(action), diag.DuplicateAction,
					"a step has exactly one action block").
					Hintf("the first one is at line %d; a second action means a second step", firstAction.Line)
				// The duplicate still has to live somewhere or the file would
				// not round-trip, and a Bad in the body is where a consumer
				// will look for it.
				s.Body = append(s.Body, badOf(action))
			}
		} else {
			// No nil check: every branch of parseStepStmt returns a node, and
			// recoverTo is the one it returns when nothing parsed -- a Bad that
			// has to reach the body or the file would not round-trip.
			sawStmt = true
			s.Body = append(s.Body, p.parseStepStmt())
		}

		if p.i == before {
			s.Body = append(s.Body, &ast.Bad{Toks: []token.Token{p.consumeRaw()}})
		}
	}

	if p.at(token.EOF) {
		p.unclosed(open, what)
	} else {
		s.RBrace = p.next()
	}

	if isNilAction(s.Action) {
		// A step's type comes from its action, so a step without one has no
		// type and nothing to run. The span is the step's name, because that
		// is the line an author will go to.
		p.errorf(name, diag.MissingAction, "%s %s has no action block", what, name.Text).
			Hintf("a step does one of: an HTTP verb (get, post, ...), run \"<command>\", or browser { ... }")
	}
	return s
}

// expectIdentOrString consumes the name of a scenario or a step.
//
// Both are written as quoted strings, and an interpolated one is not a name:
// a step's identity is what the report prints and what a `--filter` matches,
// so it has to be fixed at parse time. An identifier is accepted and reported,
// because `scenario checkout {` is the mistake someone coming from HCL makes,
// and a suggestion that adds the quotes is a one-click fix.
func (p *parser) expectIdentOrString(what string) (token.Token, bool) {
	if p.at(token.String) {
		return p.next(), true
	}
	if p.at(token.Ident) {
		t := p.next()
		p.errorf(t, diag.UnexpectedToken, "expected %s, found %s", what, describe(t)).
			Hintf("names are quoted: \"%s\"", t.Value).
			Suggest(quote(t.Value))
		return t, true
	}
	if p.at(token.StringStart) {
		t := p.peek()
		x := p.parseInterp()
		p.errorf(t, diag.UnexpectedToken, "expected %s, found an interpolated string", what).
			Hintf("a name is fixed at parse time, because it is what the report prints")
		// An interpolated string is several tokens and the name slot holds
		// one, so they are fused into a single synthetic token carrying all
		// of their source. The scenario or step then parses normally -- one
		// diagnostic, and the block after the name is still checked -- and no
		// byte is lost, which a tree-shaped alternative would have cost a
		// field on every node for a mistake almost nobody makes.
		return fuse(x.Tokens(nil)), true
	}
	p.errorf(p.peek(), diag.UnexpectedToken, "expected %s, found %s", what, describe(p.peek()))
	return token.Token{}, false
}

// fuse collapses several tokens into one whose Source is theirs, for a
// position in the tree that holds a single token and was handed more than one.
//
// Kind is Invalid so that nothing downstream mistakes the result for a usable
// name: a step whose name did not parse has no identity, and silently handing
// back the first fragment would put a wrong label in the result tree.
func fuse(toks []token.Token) token.Token {
	if len(toks) == 0 {
		return token.Token{}
	}
	if len(toks) == 1 {
		return toks[0]
	}

	var lead strings.Builder
	token.WriteTrivia(&lead, toks[0].Leading)
	full := token.Source(toks)

	spans := make([]token.Span, 0, len(toks))
	for _, t := range toks {
		spans = append(spans, t.Span)
	}

	return token.Token{
		Kind:    token.Invalid,
		Text:    full[lead.Len():],
		Code:    string(diag.UnexpectedToken),
		Message: "this is several tokens where one name was wanted",
		Span:    ast.Join(spans...),
		Leading: toks[0].Leading,
	}
}

// badFrom builds a Bad node from tokens already consumed plus whatever
// recovery skips, so an abandoned declaration keeps every byte it read.
func (p *parser) badFrom(consumed ...token.Token) *ast.Bad {
	bad := &ast.Bad{}
	for _, t := range consumed {
		if !t.Span.IsZero() || t.Text != "" {
			bad.Toks = append(bad.Toks, t)
		}
	}
	bad.Toks = append(bad.Toks, p.recoverTo().Toks...)
	return bad
}

// badOf re-wraps a parsed node's tokens as a Bad, for a construct that parsed
// but is not allowed where it appeared.
func badOf(n ast.Node) *ast.Bad { return &ast.Bad{Toks: n.Tokens(nil)} }

// tokenAt is a node's first token, for a diagnostic that wants to point at the
// start of a construct rather than at its whole span.
func tokenAt(n ast.Node) token.Token { return firstToken(n) }

func firstToken(n ast.Node) token.Token {
	if toks := n.Tokens(nil); len(toks) > 0 {
		return toks[0]
	}
	return token.Token{}
}

// isNilAction reports whether a step has no action, typed nil included.
func isNilAction(a ast.Action) bool {
	if a == nil {
		return true
	}
	switch v := a.(type) {
	case *ast.Request:
		return v == nil
	case *ast.Run:
		return v == nil
	case *ast.Browser:
		return v == nil
	case *ast.Bad:
		return v == nil
	}
	return false
}

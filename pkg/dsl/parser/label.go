package parser

import (
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// parseLabel reads the `as "<label>"` an id may have after it, and returns
// zero tokens when there is no as. A label is plain text in quotes: an as
// followed by anything else is expected-label, and what followed is kept as
// the label so the file still prints as it was written -- an interpolated
// string folded into one token, an identifier as itself -- unless it is the
// "{" or "=" the declaration goes on with.
func (p *parser) parseLabel() (as, label token.Token) {
	if !p.atWord("as") {
		return token.Token{}, token.Token{}
	}
	as = p.next()
	switch {
	case p.at(token.String):
		return as, p.next()
	case p.at(token.StringStart):
		at := p.peek()
		label = template(p.parseInterp().Tokens(nil))
		p.errorf(at, diag.ExpectedLabel, "a label is plain text in quotes, and cannot interpolate").
			Hintf(`write the text out: step sign_in as "Sign in" { ... }`)
		return as, label
	case p.at(token.Ident):
		at := p.next()
		p.errorf(at, diag.ExpectedLabel, "expected a label in quotes after as, found %s", describe(at)).
			Hintf(`put the label in quotes: as "%s"`, at.Value)
		return as, at
	}
	p.errorf(p.peek(), diag.ExpectedLabel, "expected a label in quotes after as, found %s", describe(p.peek())).
		Hintf(`step sign_in as "Sign in" { ... }`)
	return as, token.Token{}
}

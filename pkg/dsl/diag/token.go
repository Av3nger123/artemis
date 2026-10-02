package diag

import "artemis/pkg/dsl/token"

// FromToken turns a lexer Invalid token into a diagnostic.
//
// pkg/dsl/token carries an error's Code and Message as plain strings precisely
// so that it depends on nothing; this is where that becomes a Diagnostic. It
// holds for both shapes of Invalid token -- a consuming one that read the bad
// source, and a marker that only has a position -- because both carry the same
// three fields.
//
// A token that is not Invalid, or an Invalid one with no code, reports false:
// an Invalid token with nothing to say would render as a blank error, which is
// worse than the parser's own message about the same token.
func FromToken(t token.Token) (Diagnostic, bool) {
	if t.Kind != token.Invalid || t.Code == "" {
		return Diagnostic{}, false
	}
	return Diagnostic{
		Span:     t.Span,
		Code:     Code(t.Code),
		Severity: Error,
		Message:  t.Message,
	}, true
}

// AddTokens appends a diagnostic for every Invalid token in a stream and
// returns how many it added. This is a whole lexical pass's worth of reporting:
// the lexer never stops at the first bad byte, so neither does this.
func (b *Bag) AddTokens(toks []token.Token) int {
	n := 0
	for _, t := range toks {
		if d, ok := FromToken(t); ok {
			b.Add(d)
			n++
		}
	}
	return n
}

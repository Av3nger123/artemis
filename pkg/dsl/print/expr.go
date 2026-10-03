package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// Expressions are always inline. An object literal, an array, a call and an
// interpolation render as one phrase however long they are, because the
// alternative -- breaking a long `body = {...}` across lines -- is a
// line-length-aware wrapper, and that is explicitly not this issue. A comment
// buried in an expression is lifted to the end of the statement's line by
// canon.line, so nothing here has to deal with one.
//
// What this file does *not* do is as important as what it does: no string is
// requoted, no number reformatted, no regex rewritten, no `(a or b)` unwrapped
// and no operator chain reassociated. Every leaf prints its token's Text, so
// canonical mode changes layout and nothing else, which R4 asserts by comparing
// the two token streams.

// expr renders e as a phrase. A nil child -- what a recovered parse leaves
// where an expression should be -- is the empty string, and join drops it, so
// the surrounding line closes up rather than showing a hole.
func (c *canon) expr(e ast.Expr) string {
	if isNil(e) {
		return ""
	}
	switch v := e.(type) {
	case *ast.Ident:
		return v.Tok.Text
	case *ast.Literal:
		return v.Tok.Text
	case *ast.Interp:
		var b strings.Builder
		for _, s := range v.Segments {
			b.WriteString(s.Delim.Text)
			b.WriteString(c.expr(s.Expr))
		}
		b.WriteString(v.End.Text)
		return b.String()
	case *ast.Unary:
		// `not x` needs its space and `-x` must not have one, and which is
		// which is the operator's kind: `not` arrives as an Ident because the
		// language has no lexical keywords.
		if v.Op.Kind == token.Minus {
			return v.Op.Text + c.expr(v.X)
		}
		return join(" ", v.Op.Text, c.expr(v.X))
	case *ast.Binary:
		return join(" ", c.expr(v.X), v.Op.Text, c.expr(v.Y))
	case *ast.Exists:
		return join(" ", c.expr(v.X), v.Op.Text)
	case *ast.IsType:
		return join(" ", c.expr(v.X), v.Op.Text, v.Type.Text)
	case *ast.Member:
		return c.expr(v.X) + v.Dot.Text + v.Name.Text
	case *ast.Index:
		return c.expr(v.X) + v.LBracket.Text + c.expr(v.Index) + v.RBracket.Text
	case *ast.Call:
		args := make([]string, 0, len(v.Args))
		for _, a := range v.Args {
			if s := c.expr(a.Value); s != "" {
				args = append(args, s)
			}
		}
		// The trailing comma an author may have left is dropped: `f(1,)` is
		// `f(1)`. It is layout, it is not in the token stream R4 compares --
		// which counts punctuation -- so it is one of the two places canonical
		// mode is allowed to change a token.
		return v.Callee.Tok.Text + v.LParen.Text + strings.Join(args, ", ") + v.RParen.Text
	case *ast.Object:
		if len(v.Entries) == 0 {
			return v.LBrace.Text + v.RBrace.Text
		}
		parts := make([]string, 0, len(v.Entries))
		for _, e := range v.Entries {
			parts = append(parts, c.expr(e.Key)+e.Colon.Text+" "+c.expr(e.Value))
		}
		return v.LBrace.Text + strings.Join(parts, ", ") + v.RBrace.Text
	case *ast.Array:
		parts := make([]string, 0, len(v.Elems))
		for _, e := range v.Elems {
			if s := c.expr(e.Value); s != "" {
				parts = append(parts, s)
			}
		}
		return v.LBracket.Text + strings.Join(parts, ", ") + v.RBracket.Text
	case *ast.Paren:
		return v.LParen.Text + c.expr(v.X) + v.RParen.Text
	case *ast.Bad:
		// Source that did not parse, folded into one phrase: an expression is
		// a phrase, and a Bad in an expression position has nowhere else to
		// go. It is built from the tokens rather than from the text so that no
		// byte is deleted and no comment is folded into the middle of a line
		// where it would swallow the rest of it -- its comments reach the end
		// of the line through canon.line, like every other comment.
		return badPhrase(v)
	}
	return ""
}

// badPhrase is an ast.Bad's tokens on one line, with a single space wherever
// the author had any whitespace and none where they had none, so `a.b` stays
// `a.b` and nothing is lost but the layout.
func badPhrase(b *ast.Bad) string {
	var out strings.Builder
	for i, t := range b.Toks {
		if t.Text == "" {
			continue
		}
		if out.Len() > 0 && (spaced(t.Leading) || (i > 0 && spaced(b.Toks[i-1].Trailing))) {
			out.WriteByte(' ')
		}
		out.WriteString(t.Text)
	}
	return out.String()
}

// spaced reports whether trivia holds any whitespace or line break, which is
// all badPhrase needs to know: one space, or none.
func spaced(trivia []token.Trivia) bool {
	for _, tr := range trivia {
		if tr.Kind == token.Whitespace || tr.Kind == token.Newline {
			return true
		}
	}
	return false
}

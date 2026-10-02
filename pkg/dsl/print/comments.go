package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// comment is one comment canonical mode has to place, and whether the author
// left a blank line above it.
type comment struct {
	blank bool
	text  string
}

// above returns the own-line comments in t's leading trivia, and whether a
// blank line precedes t itself.
//
// Every comment in a token's Leading is on a line of its own: a comment runs to
// the end of its line, and the lexer gives trivia up to the first newline to the
// *previous* token's Trailing, so a comment that trailed code is never here.
// That split is why canonical mode needs no heuristic to tell the two apart.
//
// A blank line is two consecutive Newline trivia, one terminator per trivium by
// token.Newline's contract. The count resets at each comment, so
//
//	a = 1
//
//	# why
//	b = 2
//
// reports a blank above the comment and none above `b`, which is what the author
// wrote.
func above(t token.Token) (cs []comment, blankBefore bool) {
	nl := 0
	for _, tr := range t.Leading {
		switch tr.Kind {
		case token.Newline:
			nl++
		case token.Comment:
			cs = append(cs, comment{blank: nl >= 2, text: trimTrail(tr.Text)})
			nl = 0
		}
	}
	return cs, nl >= 2
}

// rest returns every comment in toks except the own-line ones above the first,
// in source order.
//
// These are the comments canonical layout has nowhere to put: one that trailed
// the line, and one buried inside an expression -- `[1, # why` -- which cannot
// be left where it was without commenting out the rest of the line. Both end up
// at the end of the line the statement renders to, which keeps R3 ("never drop a
// comment") true without the renderers having to know where a comment came from.
func rest(toks []token.Token) []string {
	var out []string
	for i, t := range toks {
		if i > 0 {
			out = appendComments(out, t.Leading)
		}
		out = appendComments(out, t.Trailing)
	}
	return out
}

func appendComments(dst []string, trivia []token.Trivia) []string {
	for _, tr := range trivia {
		if tr.Kind == token.Comment {
			dst = append(dst, trimTrail(tr.Text))
		}
	}
	return dst
}

// trimTrail strips the blanks off the end of a line.
//
// The set is every byte the lexer counts as whitespace and not just space and
// tab: a comment may end in a vertical tab or a form feed, and a printer that
// trimmed one of them in one place and not another would claim to have dropped
// a comment it had merely tidied.
func trimTrail(s string) string { return strings.TrimRight(s, " \t\v\f\r") }

// hasComment reports whether n's subtree carries any comment, which is what
// stops a block being rendered inline: `{ times = 1 # why , delay = "2s" }` is
// not a file.
func hasComment(n ast.Node) bool {
	if isNil(n) {
		return false
	}
	for _, t := range n.Tokens(nil) {
		// Two loops, not append(t.Leading, t.Trailing...): that append can
		// write into Leading's spare capacity and corrupt the token stream the
		// round trip is checked against.
		if hasCommentTrivia(t.Leading) || hasCommentTrivia(t.Trailing) {
			return true
		}
	}
	return false
}

func hasCommentTrivia(trivia []token.Trivia) bool {
	for _, tr := range trivia {
		if tr.Kind == token.Comment {
			return true
		}
	}
	return false
}

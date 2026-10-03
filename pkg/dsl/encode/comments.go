package encode

import (
	"strings"

	"artemis/pkg/dsl/token"
)

// Canonical layout can put a comment in exactly five places, so the encoding has
// exactly five slots, named for them:
//
//	above        own-line comments before a statement, each with a blank flag
//	blankAbove   the author left a blank line between those and the statement
//	after        comments at the end of the statement's line
//	open         a comment at the end of the line a `{` opens
//	beforeClose  own-line comments above the matching `}`
//	afterClose   a comment at the end of the `}`'s line
//
// They are grouped under one "comments" key per node so that a node's own
// fields stay its own, and the group is absent when there is nothing in it.
//
// Everything here reads token.Trivia by the same rule pkg/dsl/print's
// comments.go does. The two are deliberately separate -- print is another
// package's area and nothing here should reach into it -- and the round-trip
// test is what stops them drifting: if this file extracted a comment print
// places differently, canonical source would not survive encode-then-decode.

// comment is one own-line comment and whether the author left a blank line
// above it. The end-of-line slots are plain strings: a blank line above a
// trailing comment is not a thing that can be written.
type comment struct {
	Text  string `json:"text"`
	Blank bool   `json:"blank,omitempty"`
}

// nodeComments is the group for one node: the three statement slots and,
// for a node with braces, the three brace slots. Nil when every slot is empty.
func nodeComments(own []token.Token, lbrace, rbrace token.Token) *obj {
	o := newObj()

	var first token.Token
	if len(own) > 0 {
		first = own[0]
	}
	above, blank := ownLine(first)
	o.set("above", above)
	o.set("blankAbove", blank)
	o.set("after", endOfLine(own))

	o.set("open", commentsIn(lbrace.Trailing))
	beforeClose, _ := ownLine(rbrace)
	o.set("beforeClose", beforeClose)
	o.set("afterClose", commentsIn(rbrace.Trailing))

	if len(o.keys) == 0 {
		return nil
	}
	return o
}

// stmtComments is nodeComments for a node with no braces of its own.
func stmtComments(own []token.Token) *obj {
	return nodeComments(own, token.Token{}, token.Token{})
}

// eofComments is the group on the document: the comments the end of the file is
// carrying, which print.Canonical writes after the last scenario.
func eofComments(eof token.Token) *obj {
	o := newObj()
	above, _ := ownLine(eof)
	o.set("above", above)
	o.set("after", commentsIn(eof.Trailing))
	if len(o.keys) == 0 {
		return nil
	}
	return o
}

// ownLine returns the own-line comments in t's leading trivia and whether a
// blank line separates them from t itself.
//
// Every comment in a token's Leading is on a line of its own: a comment runs to
// the end of its line, and the lexer gives trivia up to the first newline to the
// previous token's Trailing, so a comment that trailed code is never here.
//
// A blank line is two consecutive Newline trivia, one terminator per trivium by
// token.Newline's contract, and the count resets at each comment -- so a blank
// line written above a comment is reported on that comment and not on the
// statement below it.
func ownLine(t token.Token) (cs []comment, blankBefore bool) {
	nl := 0
	for _, tr := range t.Leading {
		switch tr.Kind {
		case token.Newline:
			nl++
		case token.Comment:
			cs = append(cs, comment{Text: trimTrail(tr.Text), Blank: nl >= 2})
			nl = 0
		}
	}
	return cs, nl >= 2
}

// endOfLine returns every comment in toks except the own-line ones above the
// first, in source order.
//
// These are the comments canonical layout has nowhere else to put: one that
// trailed the line, and one buried inside an expression, which cannot be left
// where it was without commenting out the rest of the line. Both reach the end
// of the statement's line, so one slot holds both.
func endOfLine(toks []token.Token) []string {
	var out []string
	for i, t := range toks {
		if i > 0 {
			out = append(out, commentsIn(t.Leading)...)
		}
		out = append(out, commentsIn(t.Trailing)...)
	}
	return out
}

// commentsIn is the comment trivia in trivia, as text.
func commentsIn(trivia []token.Trivia) []string {
	var out []string
	for _, tr := range trivia {
		if tr.Kind == token.Comment {
			out = append(out, trimTrail(tr.Text))
		}
	}
	return out
}

// trimTrail strips the blanks off the end of a comment line, over every byte
// the lexer counts as whitespace -- a comment may end in a vertical tab, and
// one trimmed in one place and not another would look like a comment that had
// been changed.
func trimTrail(s string) string { return strings.TrimRight(s, " \t\v\f\r") }

// attachAbove puts an above/blankAbove group back on a token's leading trivia,
// shaped so that ownLine -- and pkg/dsl/print's own reader of the same rule --
// reads out what was encoded.
//
// One Newline trivium per line break, two where a blank line is asked for,
// because that is token.Newline's contract and what the blank-line count reads.
func attachAbove(t *token.Token, above []comment, blankAbove bool) {
	if len(above) == 0 && !blankAbove {
		return
	}
	var leading []token.Trivia
	for _, c := range above {
		leading = append(leading, newlines(boolTo(c.Blank, 2, 1))...)
		leading = append(leading, token.Trivia{Kind: token.Comment, Text: c.Text})
	}
	leading = append(leading, newlines(boolTo(blankAbove, 2, 1))...)
	t.Leading = append(leading, t.Leading...)
}

// attachAfter puts an end-of-line comment group back on a token's trailing
// trivia. Every slot in the group lands on one token, in order, because
// canonical layout puts them all at the end of the same line anyway.
func attachAfter(t *token.Token, texts []string) {
	for _, s := range texts {
		t.Trailing = append(t.Trailing, token.Trivia{Kind: token.Comment, Text: s})
	}
}

func newlines(n int) []token.Trivia {
	out := make([]token.Trivia, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, token.Trivia{Kind: token.Newline, Text: "\n"})
	}
	return out
}

func boolTo(b bool, yes, no int) int {
	if b {
		return yes
	}
	return no
}

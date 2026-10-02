// Package print turns an ast tree back into .art source, in two modes.
//
//	Preserving  reproduce the file byte for byte; re-render only edited nodes
//	Canonical   normalise all layout
//
// Preserving mode owns the worklane's first hard acceptance gate:
//
//	print.Preserving(parser.Parse(file, src)) == src
//
// byte for byte, for every input -- valid, invalid, truncated, CRLF,
// BOM-prefixed, non-ASCII. A UI loads a file, changes one field and writes it
// back, and anything the front end discarded is destroyed at that moment; a
// front end that was not lossless from the start cannot be made lossless later
// without being rewritten. So the gate is tested here, over the whole fixture
// corpus and over fuzz-generated valid input, rather than left to the UI that
// will depend on it.
//
// # The two modes are one descent
//
// There is one layout engine, not two. Preserving mode walks only far enough to
// isolate the nodes something edited and copies everything else with
// ast.Source; inside an edited node it calls the canonical renderer. So
// canonical mode's rules are also the answer to "what does a freshly built node
// look like", and a layout rule cannot be right in one mode and wrong in the
// other.
//
// # Marking an edit
//
// A token whose Span is zero was never in the file -- the convention ast.Join
// and ast.Span already encode -- so it is the edit marker, and the unit of
// re-rendering is the node holding it. Synthetic is the one documented way to
// build such a token:
//
//	field.Value = &ast.Literal{Tok: print.Synthetic(token.String, `"30s"`)}
//
// Two things follow, and callers that get them wrong get stale bytes rather
// than an error. A synthetic token must carry its Text exactly as it should
// appear in the file, delimiters included -- `"30s"` with the quotes, not 30s:
// the printer lays out whitespace and never invents a token's spelling, which
// keeps it free of a quoting dialect that would be a second, silently divergent
// copy of the lexer's. And a caller that rewrites an existing token's text --
// ART-43's JSON decoder, say -- must zero that token's Span, or the printer
// will believe the old bytes.
package print

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// Preserving renders n as the source it was parsed from, re-rendering only the
// nodes that hold a synthetic token.
//
// For a tree nothing has touched this is ast.Source and nothing else, so the
// round trip is exact by construction rather than by a layout engine happening
// to agree with the author. For an edited tree the edited node is laid out
// canonically at the indentation of its neighbours and every other byte of the
// file is copied, which is what makes a UI write a one-line diff.
func Preserving(n ast.Node) string {
	if isNil(n) {
		return ""
	}
	if intact(n) {
		return ast.Source(n)
	}
	p := &pres{unit: detectUnit(n)}
	p.node(n, ctx{})
	if p.needNL {
		p.b.WriteString("\n")
	}
	return p.b.String()
}

// Canonical renders n with all layout normalised: two-space indentation, one
// space either side of an operator, one item per line in a block that does not
// fit inline, LF line endings, one trailing newline.
//
// It is idempotent -- printing its own output again changes nothing -- which is
// what makes it safe for `artemis fmt -w`. It changes layout and nothing else:
// no string is requoted, no number reformatted, no regex rewritten, no
// expression reassociated, and no comment dropped.
//
// It is total. A tree holding ast.Bad nodes prints those nodes' source
// verbatim, so formatting never deletes the bytes of a line that did not parse;
// whether a file with diagnostics should be formatted at all is the caller's
// call, and idempotence is only claimed for a file that parses clean.
func Canonical(n ast.Node) string {
	if isNil(n) {
		return ""
	}
	c := &canon{w: newWriter()}
	c.node(n)
	c.w.flush()
	return c.w.String()
}

// Synthetic builds a token that was never in a file: a zero span, which is what
// marks the node holding it as edited, and the exact text it should print as.
//
// text is source, not a value: a string's text has its quotes and its escapes,
// a regex's its slashes. See the package comment for why the printer will not
// add them for you.
func Synthetic(kind token.Kind, text string) token.Token {
	return token.Token{Kind: kind, Text: text, Value: valueOf(kind, text)}
}

// valueOf fills in the decoded Value for the kinds where it is the text
// itself. For a string or a regex it is left empty: decoding one means
// resolving escapes, which is the lexer's job and would be a second
// implementation of it here. Nothing in the printer reads Value, and a caller
// that needs a decoded value on a synthetic token can set it.
func valueOf(kind token.Kind, text string) string {
	switch kind {
	case token.Ident, token.Number, token.Bool, token.Null:
		return text
	}
	return ""
}

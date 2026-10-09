// Package token defines the lexical vocabulary of the Artemis DSL: the token
// kinds, the span that locates a token in its file, and the trivia -- comments,
// blank lines, spacing -- that a lossless front end must not discard.
//
// Retaining trivia is the one decision in the front end that cannot be
// retrofitted. A UI that loads a file, changes one field and writes it back
// destroys everything the lexer threw away, so the lexer throws away nothing:
// every byte of a file is either a token's Text or a piece of trivia attached
// to one. See lexer.Lex for the invariant that makes that checkable.
package token

import "strings"

// Span locates a run of source. These are exactly the six fields
// docs/artemis-dsl-design.md promises the tree's JSON encoding, so a span
// serialises without a translation layer.
//
// Line and Col are 1-based and Col counts bytes, like go/token -- a multi-byte
// rune advances the column by its length. Offset is a 0-based byte offset.
// EndLine and EndCol are exclusive: they are the position just past the last
// byte, which is why the caret under `statu` in a diagnostic runs col 10 to
// endCol 15.
type Span struct {
	File    string
	Line    int
	Col     int
	EndLine int
	EndCol  int
	Offset  int

	// Via is zero for source written where it stands. A token that
	// pkg/dsl/expand copied out of a collection carries one plus the index of
	// the use that copied it in the expansion's use table, so two uses of one
	// request yield spans that compare unequal and a diagnostic can name the
	// use line. It is not one of the six encoded fields and no JSON writes it.
	Via int
}

// IsZero reports whether s locates nothing, which is what a span built in Go
// rather than read from a file looks like.
func (s Span) IsZero() bool { return s.Line == 0 && s.Col == 0 && s.Offset == 0 }

// Token is one lexical unit plus the trivia around it.
//
// Text is the exact source the token was read from, delimiters included: a
// string token's Text has its quotes, a regex token's has its slashes. Nothing
// else reproduces the file.
//
// Value is the token's decoded content, and only for the kinds that have one:
// a string's text with escapes resolved, a regex's pattern with \/ unescaped,
// and the text itself for Ident, Number, Bool and Null. It is empty for
// operators and delimiters, where Text says everything.
type Token struct {
	Kind  Kind
	Text  string
	Value string

	// Code and Message are set on Invalid tokens only. Code is a stable
	// identifier a UI can key behaviour off -- "unterminated-string",
	// "unclosed-interpolation", "unterminated-regex", "invalid-escape",
	// "unexpected-character" -- and Message is prose for a human. They are
	// plain strings rather than a diag.Diagnostic so this package depends on
	// nothing; turning them into diagnostics is pkg/dsl/diag's job.
	Code    string
	Message string

	Span Span

	// Leading is the trivia before this token, Trailing the trivia after it on
	// the same line. The lexer's attachment rule is: trivia between two tokens
	// goes to the earlier one's Trailing up to the first newline, and to the
	// later one's Leading from that newline on. So a blank line is two
	// consecutive newline trivia in some token's Leading, which is how "blank
	// lines are retained" is actually represented.
	Leading  []Trivia
	Trailing []Trivia
}

// Source is the token and its trivia as they were written. Summing this over a
// whole stream reproduces the file byte for byte; that is the front end's
// foundational invariant and lexer's round-trip test asserts exactly it.
func (t Token) Source() string {
	var b strings.Builder
	WriteTrivia(&b, t.Leading)
	b.WriteString(t.Text)
	WriteTrivia(&b, t.Trailing)
	return b.String()
}

// StartsLine reports whether a newline separates this token from the one
// before it, which is how the parser sees a statement separator: the grammar's
// `Sep = "," | newline` needs newlines, but making them tokens would give a
// newline two possible homes -- a token, or trailing trivia -- and that
// ambiguity is what makes a lossless printer fragile. So newlines stay trivia
// and the parser asks this.
//
// For the first token in a file there is nothing to be separated from, so this
// is false unless the file opens with a comment or a blank line. That is the
// right answer for a parser looking for a separator and the wrong one for
// anything asking about column 1, which nothing does.
func (t Token) StartsLine() bool {
	for _, tr := range t.Leading {
		if tr.Kind == Newline {
			return true
		}
	}
	return false
}

// IsMarker reports whether t is a position-only error token.
//
// Invalid tokens come in two shapes. A *consuming* one carries the source it
// read -- a stray character, an unterminated string or regex -- and so takes
// part in the round trip like any other token. A *marker* has empty Text and
// only a position, for an error discovered inside another token: an unclosed
// ${, a bad escape in an otherwise fine string. A marker is emitted just before
// the token whose scan found it, contributes nothing to the source, and is
// skipped by the parser, which has already handed it to diagnostics.
func (t Token) IsMarker() bool { return t.Kind == Invalid && t.Text == "" }

// Source is every token's Source joined, which is the file.
func Source(toks []Token) string {
	var b strings.Builder
	for _, t := range toks {
		WriteTrivia(&b, t.Leading)
		b.WriteString(t.Text)
		WriteTrivia(&b, t.Trailing)
	}
	return b.String()
}

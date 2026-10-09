package ast

import (
	"reflect"

	"artemis/pkg/dsl/token"
)

// Join returns the span covering every span given, ignoring zero ones.
//
// Zero spans are the normal case, not an edge case: a request block is
// optional, a `within` clause is optional, and the comma after the last field
// in a list is usually absent, so a node routinely holds tokens that were
// never in the file. Skipping them is what lets Span be written once here
// instead of guarded at every call site.
//
// The result's start is the earliest by offset and its end the latest, rather
// than the first and last argument, because a node built during recovery can
// hold its tokens out of order.
//
// Spans from another file, or from another copy (a different Via), than the
// first non-zero one are ignored. pkg/dsl/expand builds nodes that mix tokens
// copied out of a collection with an argument written at the use, and an
// offset in one place says nothing about an offset in the other; comparing
// them would move the node's start -- and reorder a step's statements, which
// are sorted by it.
func Join(spans ...token.Span) token.Span {
	out := token.Span{}
	for _, s := range spans {
		if s.IsZero() {
			continue
		}
		if out.IsZero() {
			out = s
			continue
		}
		if s.File != out.File || s.Via != out.Via {
			continue
		}
		if s.Offset < out.Offset {
			out.File, out.Line, out.Col, out.Offset = s.File, s.Line, s.Col, s.Offset
		}
		if s.EndLine > out.EndLine || (s.EndLine == out.EndLine && s.EndCol > out.EndCol) {
			out.EndLine, out.EndCol = s.EndLine, s.EndCol
		}
	}
	return out
}

// spanOf is Join over the tokens a node holds. Every node's Span is this, so
// "a node's span runs from its first token to its last" is true by
// construction and cannot drift from what Tokens reports.
func spanOf(n Node) token.Span {
	var buf [16]token.Token
	toks := n.Tokens(buf[:0])
	spans := make([]token.Span, 0, len(toks))
	for _, t := range toks {
		spans = append(spans, t.Span)
	}
	return Join(spans...)
}

// appendNode appends n's tokens when n is non-nil. A nil Expr or Action is
// what a recovered parse leaves behind, and the typed-nil case matters: a
// (*Ident)(nil) stored in an Expr is not == nil, so callers pass the interface
// and this checks it.
func appendNode(dst []token.Token, n Node) []token.Token {
	if isNil(n) {
		return dst
	}
	return n.Tokens(dst)
}

// isNil reports whether n holds no node, interface nil or typed nil alike.
//
// The typed-nil case is the one that matters: a parser helper that returns
// (*Ident)(nil) on a failed parse stores a non-nil Expr holding a nil pointer,
// and calling Tokens on it panics. The parser is written not to do that, and
// this is the second line of defence, because R1 says no input panics and a
// reflect call on a tree walk is not a cost anything here can measure.
func isNil(n Node) bool {
	if n == nil {
		return true
	}
	switch v := reflect.ValueOf(n); v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return false
}

// appendTok appends t unless it is a token that was never in the file. Kind
// EOF is the one zero-width token that is real, so it is matched on its span
// rather than its kind.
func appendTok(dst []token.Token, t token.Token) []token.Token {
	if t.Span.IsZero() && t.Text == "" && len(t.Leading) == 0 && len(t.Trailing) == 0 {
		return dst
	}
	return append(dst, t)
}

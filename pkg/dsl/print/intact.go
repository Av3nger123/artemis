package print

import (
	"reflect"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// intact reports whether every token in n's subtree came from a file.
//
// This is the whole of edit detection. A node is intact when none of the tokens
// it reports has a zero span, and an intact node prints as the bytes it was
// parsed from -- so Preserving's descent stops at the highest intact node it
// can, which for an untouched tree is the root.
//
// It reads n.Tokens rather than walking ast.Children because Tokens is the one
// place a node says what it is made of: a node that gained a field and forgot
// to report it already fails the round-trip test, and this cannot disagree with
// that.
func intact(n ast.Node) bool {
	if isNil(n) {
		return true
	}
	for _, t := range n.Tokens(nil) {
		if synthetic(t) {
			return false
		}
	}
	return true
}

// synthetic reports whether t was built in Go rather than read from a file.
//
// A real token always has a 1-based line and column, so a zero span is
// unambiguous -- including for the first token in a file, which is at 1:1 and
// offset 0. A marker (an Invalid token with no text, carrying only a position)
// is real: it has a span, contributes nothing to the source, and is not an
// edit.
func synthetic(t token.Token) bool { return t.Span.IsZero() }

// isNil reports whether n holds no node, interface nil or typed nil alike.
//
// ast has the same check and keeps it unexported, so the printer repeats it
// rather than reach for it: a recovered parse leaves nil where an optional
// child was, and a (*ast.Ident)(nil) stored in an ast.Expr is not == nil, so a
// printer that only checked for interface nil would panic on exactly the trees
// R7 names.
func isNil(n ast.Node) bool {
	if n == nil {
		return true
	}
	switch v := reflect.ValueOf(n); v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return false
}

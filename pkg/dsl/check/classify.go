package check

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// classify labels an expect simple or complex.
//
// This is a UI contract, not a semantic one. Nearly every assertion has the
// shape `<path> <op> <literal>`, which is three widgets -- a path picker, an
// operator dropdown and a value field -- and the ones that do not have to
// degrade to a single raw expression field with live validation. The label
// lives here, and in the JSON encoding, because it is a documented property of
// the tree: a second client re-deriving it would be a second client that can
// disagree about it.
//
// The simple shapes are exactly the four the design document lists:
//
//	<path> <op> <literal>
//	<path> exists
//	<path> is <type>
//	not <any of the above>
//
// and nothing else. A bare path with no operator (`expect visible(".modal")`)
// is complex, which is the strict reading: widening the set later is additive,
// and a form that renders an operator dropdown for an expression that has no
// operator is not.
func classify(e *ast.Expect) Class {
	if e != nil && isSimple(e.Value) {
		return Simple
	}
	return Complex
}

func isSimple(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Unary:
		// `not <simple>`. The other Unary is `-x`, which is a value and not an
		// assertion shape.
		return x.Op.Kind == token.Ident && x.Op.Value == "not" && isSimple(x.X)

	case *ast.Binary:
		// A comparison. `and` and `or` are Binary too and are deliberately
		// not here: an `and` chain is one assertion but it is not three
		// widgets.
		if !token.IsComparison(x.Op) {
			return false
		}
		return isPath(x.X) && isLiteral(x.Y)

	case *ast.Exists:
		return isPath(x.X)

	case *ast.IsType:
		// An unresolvable type name is still the simple *shape*; the
		// diagnostic about it is a separate concern from how a form renders
		// it.
		return isPath(x.X)
	}
	return false
}

// isPath reports whether x is something a path picker can hold: an identifier
// root with any chain of `.name` and `[literal]`, or a builtin call whose
// arguments are all literals.
//
// The call case is what makes a browser assertion form-authorable:
// `text("[role=status]") contains "Pro"` is the normal browser `expect`, and a
// picker whose entries are the element functions with a selector field is
// exactly the widget for it.
func isPath(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return x != nil && x.Tok.Kind == token.Ident

	case *ast.Member:
		return isPath(x.X) && x.Name.Kind == token.Ident

	case *ast.Index:
		return isPath(x.X) && isLiteral(x.Index)

	case *ast.Call:
		if x.Callee == nil || x.Callee.Tok.Kind != token.Ident {
			return false
		}
		if !token.IsBuiltin(x.Callee.Name()) {
			return false
		}
		for _, a := range x.Args {
			if !isLiteral(a.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// isLiteral reports whether x is a single literal token.
//
// An interpolated string is not one. A form's value field holding `"${x}"`
// contains a small expression language, which is the complex case by
// definition -- and the span data that lets a bad `${` be underlined precisely
// is why that field is validated rather than treated as text.
func isLiteral(x ast.Expr) bool {
	if u, ok := x.(*ast.Unary); ok && u != nil && u.Op.Kind == token.Minus {
		// `-1` is two tokens and one value. A form's number field holds it,
		// so treating it as a literal is what keeps `expect body.delta == -1`
		// out of the raw-expression fallback.
		return isLiteral(u.X)
	}
	l, ok := x.(*ast.Literal)
	return ok && l != nil
}

// Package taint reports whether an expression can read a secret binding.
//
// This is the whole meaning of the `secret` modifier. A scenario declares a
// binding secret, every value an expression builds from it is secret too, and
// pkg/report prints a placeholder in place of such a value. ART-54.
//
// # Why a walk is exact and not an approximation
//
// A static analysis of a data flow is normally an approximation, because a value
// can arrive through one of two branches and the compiler cannot know which one
// ran. This language has no branch: `if`, `else`, `for`, `while` and `parallel`
// are reserved words and not features -- see token.Reserved and README.md's "Not
// yet" -- so the provenance of every value is a property of the text.
//
// So a taint carried on a run-time value would be no more precise than this,
// and it would need a new type inside the domain pkg/eval/value.go calls JSON's,
// which every switch in pkg/eval would then have to handle.
//
// # Where it runs
//
// pkg/dsl/lower calls this, not pkg/dsl/check. check reports diagnostics and
// returns nothing a later stage reads, and what needs the answer here is each
// value lower builds.
package taint

import "artemis/pkg/dsl/ast"

// Secret reports whether evaluating e can observe one of the named bindings.
//
// A nil expression and an empty set are both false, so a caller lowering a
// scenario that declares no secret does not special-case either.
func Secret(e ast.Expr, secrets map[string]bool) bool {
	if e == nil || len(secrets) == 0 {
		return false
	}
	switch v := e.(type) {
	case *ast.Ident:
		return secrets[v.Name()]

	case *ast.Call:
		// Callee is deliberately not read. It is an *ast.Ident holding a
		// function name, so a binding that happens to be called `env` must not
		// make every env() call secret.
		for _, a := range v.Args {
			if Secret(a.Value, secrets) {
				return true
			}
		}
		return false

	case *ast.Interp:
		// The case ART-54 exists for: `"Bearer ${token}"` is secret because a
		// segment of it reads a secret binding.
		for _, s := range v.Segments {
			if Secret(s.Expr, secrets) {
				return true
			}
		}
		return false

	case *ast.Member:
		// Name is a token and not an Ident, so `body.pw` reads no binding
		// called `pw`. Only the subject can.
		return Secret(v.X, secrets)

	case *ast.Index:
		return Secret(v.X, secrets) || Secret(v.Index, secrets)

	case *ast.Object:
		for _, en := range v.Entries {
			if Secret(en.Key, secrets) || Secret(en.Value, secrets) {
				return true
			}
		}
		return false

	case *ast.Array:
		for _, el := range v.Elems {
			if Secret(el.Value, secrets) {
				return true
			}
		}
		return false

	case *ast.Unary:
		return Secret(v.X, secrets)
	case *ast.Binary:
		return Secret(v.X, secrets) || Secret(v.Y, secrets)
	case *ast.Exists:
		return Secret(v.X, secrets)
	case *ast.IsType:
		return Secret(v.X, secrets)
	case *ast.Paren:
		return Secret(v.X, secrets)

	case *ast.Literal, *ast.Bad:
		// A literal holds no binding. A Bad node holds tokens the parser could
		// not make sense of, and a file with one does not run.
		return false
	}

	// A node this switch does not name reads as public, which is a leak and not
	// a compile error. TestSecretCoversEveryExprNode is what makes a new
	// expression node fail here instead of passing quietly.
	return false
}

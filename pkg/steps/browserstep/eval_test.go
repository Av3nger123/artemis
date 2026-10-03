package browserstep

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/eval"
)

// evalIn parses one expression and evaluates it against env.
//
// It goes through the real parser rather than building an ast.Expr by hand, so
// a test of `attr("#home", "href")` is a test of the thing an author writes:
// the call is resolved by pkg/eval's own dispatch, which is the path a .art
// file takes. The expression is wrapped in a scenario because that is the only
// thing the parser parses.
func evalIn(t *testing.T, env *eval.Env, src string) (any, error) {
	t.Helper()
	tree, bag := parser.Parse("t.art", `scenario "s" {
  step "s" {
    browser { goto "http://127.0.0.1:1/" }
    expect `+src+`
  }
}`)
	if bag.HasErrors() {
		t.Fatalf("the expression %q does not parse: %v", src, bag.All()[0].Message)
	}
	return eval.Eval(firstExpect(t, tree), env)
}

// firstExpect is the one `expect` of the one step of the one scenario, which is
// the shape evalIn builds. A walk rather than an index chain, so a change to
// the wrapper above does not quietly pick the wrong node.
func firstExpect(t *testing.T, tree *ast.File) ast.Expr {
	t.Helper()
	var found ast.Expr
	ast.Walk(tree, func(n ast.Node) bool {
		if e, ok := n.(*ast.Expect); ok && found == nil {
			found = e.Value
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("the wrapper holds no expect")
	}
	return found
}

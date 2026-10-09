package print

import (
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// TestAnEditInsideACollectionTouchesOnlyItsOwnLines holds preserving mode to
// R6 for the nodes collections added: a new line in a use block, a new step in
// a flow, and a changed argument each move only their own lines.
func TestAnEditInsideACollectionTouchesOnlyItsOwnLines(t *testing.T) {
	src := read(t, filepath.Join("..", "parser", "testdata", "collections.art"))

	cases := []edit{{
		name: "an argument's value",
		mutate: func(t *testing.T, f *ast.File) {
			field(t, f, "sku").Value = &ast.Literal{Tok: Synthetic(token.String, `"B-2"`)}
		},
		removed: []string{`    use create { sku = "A-1", qty = 2, base = base }`},
		added:   []string{`    use create { sku = "B-2", qty = 2, base = base }`},
	}, {
		name: "a new line in a use block",
		mutate: func(t *testing.T, f *ast.File) {
			u := use(t, f, "orders.checkout")
			u.Lines = append(u.Lines, &ast.Drop{
				Keyword: Synthetic(token.Ident, "drop"),
				What:    Synthetic(token.Ident, "expects"),
			})
		},
		added: []string{`    drop expects`},
	}, {
		name: "a new step in a flow",
		mutate: func(t *testing.T, f *ast.File) {
			var flow *ast.FlowDecl
			ast.Inspect(f, func(n ast.Node) {
				if v, ok := n.(*ast.FlowDecl); ok {
					flow = v
				}
			})
			flow.Body = append(flow.Body, &ast.StepDecl{
				Keyword: Synthetic(token.Ident, "step"),
				Name:    Synthetic(token.String, `"done"`),
				LBrace:  Synthetic(token.LBrace, "{"),
				Action: &ast.Request{
					Method: Synthetic(token.Ident, "get"),
					URL:    &ast.Literal{Tok: Synthetic(token.String, `"/done"`)},
				},
				RBrace: Synthetic(token.RBrace, "}"),
			})
		},
		added: []string{`    step "done" {`, `      get "/done"`, `    }`},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, bag := parser.Parse("collections.art", src)
			if bag.Len() > 0 {
				t.Fatalf("fixture does not parse: %v", bag.All())
			}
			tc.mutate(t, tree)
			out := Preserving(tree)
			added, removed := lineDiff(src, out)
			assertLines(t, "added", added, tc.added)
			assertLines(t, "removed", removed, tc.removed)
			if _, outBag := parser.Parse("edited.art", out); outBag.Len() > 0 {
				t.Errorf("the edited file does not parse:\n%s\n%v", out, outBag.All())
			}
		})
	}
}

func use(t *testing.T, f *ast.File, ref string) *ast.UseDecl {
	t.Helper()
	var found *ast.UseDecl
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.UseDecl); ok && found == nil && v.Ref() == ref {
			found = v
		}
	})
	if found == nil {
		t.Fatalf("no use of %s in the fixture", ref)
	}
	return found
}

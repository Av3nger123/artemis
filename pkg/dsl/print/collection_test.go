package print

import (
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// TestAnEditInsideACollectionTouchesOnlyItsOwnLines holds preserving mode to
// R6 for the nodes collections added: a new line in a use block and a changed
// argument each move only their own lines.
func TestAnEditInsideACollectionTouchesOnlyItsOwnLines(t *testing.T) {
	src := read(t, filepath.Join("..", "parser", "testdata", "collections.art"))

	cases := []edit{{
		name: "an argument's value",
		mutate: func(t *testing.T, f *ast.File) {
			field(t, f, "sku").Value = &ast.Literal{Tok: Synthetic(token.String, `"B-2"`)}
		},
		removed: []string{`    sku = "A-1"`},
		added:   []string{`    sku = "B-2"`},
	}, {
		name: "a new line in a use block",
		mutate: func(t *testing.T, f *ast.File) {
			u := use(t, f, "orders.pay")
			u.Lines = append(u.Lines, &ast.Drop{
				Keyword: Synthetic(token.Ident, "drop"),
				What:    Synthetic(token.Ident, "expects"),
			})
		},
		added: []string{`    drop expects`},
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

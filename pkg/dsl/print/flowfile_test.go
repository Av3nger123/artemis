package print

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// TestDataScenarioLayout is the one-line rule: at most three sets and no tags
// stay on one line, and anything else is one line each with the "=" signs
// aligned, however the author wrote it.
func TestDataScenarioLayout(t *testing.T) {
	head := "flow \"f.art\"\n\n"
	for _, tc := range []struct{ name, src, want string }{
		{"three sets stay on one line",
			"scenario \"a\" {\n  quantity = 3\n  coupon = \"TEN\"\n  total = 270\n}\n",
			"scenario \"a\" { quantity = 3  coupon = \"TEN\"  total = 270 }\n"},
		{"four sets break and align",
			"scenario \"a\" { a = 1  bb = 2  ccc = 3  d = skip }\n",
			"scenario \"a\" {\n  a   = 1\n  bb  = 2\n  ccc = 3\n  d   = skip\n}\n"},
		{"tags break the line",
			"scenario \"a\" { tags = [\"smoke\"]  code = 422 }\n",
			"scenario \"a\" {\n  tags = [\"smoke\"]\n  code = 422\n}\n"},
		{"a comment inside breaks the line",
			"scenario \"a\" { # why\n  code = 422 }\n",
			"scenario \"a\" { # why\n  code = 422\n}\n"},
		{"an empty scenario",
			"scenario \"a\" { }\n",
			"scenario \"a\" {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, bag := parser.Parse("s.art", head+tc.src)
			if bag.HasErrors() {
				t.Fatalf("diagnostics: %v", bag.All())
			}
			if got := Canonical(tree); got != head+tc.want {
				t.Errorf("Canonical =\n%s\nwant\n%s", got, head+tc.want)
			}
		})
	}
}

func TestFlowAndHeaderLayout(t *testing.T) {
	src := "import \"a.art\"\nflow   checkout   {\nvar code = 200\nstep \"s\" {\nget \"/\"\n}\n}\n"
	want := "import \"a.art\"\n\nflow checkout {\n  var code = 200\n\n  step \"s\" {\n    get \"/\"\n  }\n}\n"
	tree, bag := parser.Parse("f.art", src)
	if bag.HasErrors() {
		t.Fatalf("diagnostics: %v", bag.All())
	}
	if got := Canonical(tree); got != want {
		t.Errorf("Canonical =\n%s\nwant\n%s", got, want)
	}
	if got := Canonical(&ast.FlowRef{Keyword: Synthetic(token.Ident, "flow"), Path: Synthetic(token.String, `"f.art"`)}); got != "flow \"f.art\"\n" {
		t.Errorf("a header prints %q", got)
	}
}

// TestANewSetGetsALine is preserving mode's half: a set added to a scenario
// lands on a line of its own, and every other byte stays.
func TestANewSetGetsALine(t *testing.T) {
	src := "flow \"f.art\"\n\nscenario \"a\" {\n  coupon = \"TEN\"\n}\n"
	tree, _ := parser.Parse("s.art", src)
	s := tree.Scenarios[1].(*ast.Scenario)
	s.Body = append(s.Body, &ast.Set{
		Name:   Synthetic(token.Ident, "code"),
		Assign: Synthetic(token.Assign, "="),
		Value:  &ast.Literal{Tok: Synthetic(token.Number, "422")},
	})
	want := "flow \"f.art\"\n\nscenario \"a\" {\n  coupon = \"TEN\"\n  code = 422\n}\n"
	if got := Preserving(tree); got != want {
		t.Errorf("Preserving = %q, want %q", got, want)
	}
}

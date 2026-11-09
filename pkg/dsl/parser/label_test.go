package parser

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// labelled is the name and label of the one node each case is about.
type labelled struct{ name, label token.Token }

func firstLabelled(t *testing.T, f *ast.File) labelled {
	t.Helper()
	var out *labelled
	ast.Inspect(f, func(n ast.Node) {
		if out != nil {
			return
		}
		switch v := n.(type) {
		case *ast.Collection:
			if len(v.Items) == 0 {
				out = &labelled{v.Name, v.Label}
			}
		case *ast.RequestDecl:
			out = &labelled{v.Name, v.Label}
		case *ast.Flow:
			if len(v.Body) == 0 {
				out = &labelled{v.Name, v.Label}
			}
		case *ast.StepDecl:
			out = &labelled{v.Name, v.Label}
		case *ast.UseDecl:
			out = &labelled{v.Name, v.Label}
		case *ast.Scenario:
			out = &labelled{v.Name, v.Label}
		}
	})
	if out == nil {
		t.Fatalf("no labelled node in %s", ast.Source(f))
	}
	return *out
}

var labelCases = []struct{ src, display string }{
	{"collection admin as \"Admin\" {}\n", "Admin"},
	{"collection admin as \"Admin\" {\n  request legacy_readiness as \"Legacy readiness\" {\n    get \"x\"\n  }\n}\n", "Legacy readiness"},
	{"flow f as \"F\" {}\n", "F"},
	{"flow f {\n  step sign_in as \"Sign in\" {\n    get \"x\"\n  }\n}\n", "Sign in"},
	{"flow \"f.art\"\n\nscenario full_price as \"Full price\" {}\n", "Full price"},
	{"flow f {\n  step check_it {\n    get \"x\"\n  }\n}\n", "check_it"},
	{"flow f {\n  step sign_in as \"Sign in\" = auth.login {}\n}\n", "Sign in"},
}

func TestParseLabels(t *testing.T) {
	for _, c := range labelCases {
		f, bag := Parse("t.art", c.src)
		if bag.Len() != 0 {
			t.Errorf("%q: diagnostics %v", c.src, bag.All())
			continue
		}
		l := firstLabelled(t, f)
		if got := ast.Display(l.name, l.label); got != c.display {
			t.Errorf("%q: Display = %q, want %q", c.src, got, c.display)
		}
	}
}

func TestRoundTripLabels(t *testing.T) {
	for _, c := range labelCases {
		f, _ := Parse("t.art", c.src)
		if got := ast.Source(f); got != c.src {
			t.Errorf("Source = %q, want %q", got, c.src)
		}
	}
}

func TestParseStepCallLabelAndAlias(t *testing.T) {
	src := "flow f {\n  step rush_order as \"Rush order\" = orders.create as rush {}\n}\n"
	f, bag := Parse("t.art", src)
	if bag.Len() != 0 {
		t.Fatalf("diagnostics %v", bag.All())
	}
	u := f.Scenarios[0].(*ast.Flow).Body[0].(*ast.UseDecl)
	if u.Name.Value != "rush_order" || u.Label.Value != "Rush order" || u.Alias.Value != "rush" {
		t.Fatalf("name %q label %q alias %q", u.Name.Value, u.Label.Value, u.Alias.Value)
	}
	if got := ast.Source(f); got != src {
		t.Fatalf("Source = %q", got)
	}
}

func TestParseLabelErrors(t *testing.T) {
	for _, src := range []string{
		"flow f {\n  step x as {\n    get \"x\"\n  }\n}\n",
		"flow f {\n  step x as \"a${b}\" {\n    get \"x\"\n  }\n}\n",
		"flow f as g {}\n",
	} {
		_, bag := Parse("t.art", src)
		n := 0
		for _, d := range bag.All() {
			if d.Code == diag.ExpectedLabel {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%q: %d expected-label diagnostics, want 1: %v", src, n, bag.All())
		}
	}
}

func TestParseQuotedNameStillParses(t *testing.T) {
	_, bag := Parse("t.art", "flow f {\n  step \"log in\" {\n    get \"x\"\n  }\n}\n")
	if bag.Len() != 0 {
		t.Fatalf("diagnostics %v", bag.All())
	}
}

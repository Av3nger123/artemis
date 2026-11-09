package expand

import (
	"testing"

	"artemis/pkg/dsl/ast"
)

const settingsColl = "collection settings {\n  request client_login as \"Client login\" {\n    get \"x\"\n  }\n  request plain {\n    get \"y\"\n  }\n}\n"

// expandedSteps is every step of every scenario res.File holds, in order.
func expandedSteps(t *testing.T, e expanded) []*ast.StepDecl {
	t.Helper()
	if e.diags != "" {
		t.Fatalf("diagnostics:\n%s", e.diags)
	}
	var out []*ast.StepDecl
	ast.Inspect(e.res.File, func(n ast.Node) {
		if s, ok := n.(*ast.StepDecl); ok {
			out = append(out, s)
		}
	})
	return out
}

func scenarioRunning(t *testing.T, flow string) expanded {
	t.Helper()
	return expandFlows(t, "flow \"f.art\"\n\nscenario s {}\n", map[string]string{
		"settings.art": settingsColl,
		"f.art":        "import \"settings.art\"\n\nflow f {\n" + flow + "}\n",
	})
}

func TestItemCallInheritsItemLabel(t *testing.T) {
	s := expandedSteps(t, scenarioRunning(t, "  step sign_in = settings.client_login {}\n"))
	if len(s) != 1 || s[0].Name.Value != "sign_in" || ast.Display(s[0].Name, s[0].Label) != "Client login" {
		t.Fatalf("got %q / %q", s[0].Name.Value, ast.Display(s[0].Name, s[0].Label))
	}
}

func TestItemCallOwnLabelWins(t *testing.T) {
	s := expandedSteps(t, scenarioRunning(t, "  step sign_in as \"Sign in\" = settings.client_login {}\n"))
	if got := ast.Display(s[0].Name, s[0].Label); got != "Sign in" {
		t.Fatalf("Display = %q, want Sign in", got)
	}
}

func TestItemCallNoLabels(t *testing.T) {
	s := expandedSteps(t, scenarioRunning(t, "  step other = settings.plain {}\n"))
	if got := ast.Display(s[0].Name, s[0].Label); got != "other" {
		t.Fatalf("Display = %q, want other", got)
	}
}

func TestScenarioAndFlowLabels(t *testing.T) {
	e := expandFlows(t, "flow \"f.art\"\n\nscenario with_coupon as \"With a coupon\" {}\n", map[string]string{
		"f.art": "flow checkout as \"Checkout\" {\n  step a {\n    get \"x\"\n  }\n}\n",
	})
	if e.diags != "" {
		t.Fatalf("diagnostics:\n%s", e.diags)
	}
	sc := e.res.File.Scenarios[0].(*ast.Scenario)
	if sc.Name.Value != "with_coupon" || ast.Display(sc.Name, sc.Label) != "With a coupon" || sc.Flow != "checkout" || sc.FlowLabel != "Checkout" {
		t.Fatalf("scenario %q %q flow %q %q", sc.Name.Value, ast.Display(sc.Name, sc.Label), sc.Flow, sc.FlowLabel)
	}
}

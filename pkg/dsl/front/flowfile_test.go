package front

import (
	"path/filepath"
	"testing"
)

const flowSrc = "# the checkout\nimport \"../collections/auth.art\"\nimport \"../collections/x.art\" as x\n\n" +
	"flow checkout {\n  var code = 200\n  step \"pay\" {\n    post \"/pay\"\n    expect status == code\n  }\n}\n"

const scenarioFileSrc = "flow \"../flows/checkout.art\"\n\nscenario \"full price\" {}\n"

func TestFlowDeclFindsTheFlowBelowTheImports(t *testing.T) {
	kw, name, ok := FlowDecl("flows/checkout.art", flowSrc)
	if !ok || name != "checkout" {
		t.Fatalf("FlowDecl() = %q, %v, want checkout, true", name, ok)
	}
	if kw.Span.Line != 5 || kw.Span.Col != 1 {
		t.Errorf("the keyword is at %d:%d, want 5:1", kw.Span.Line, kw.Span.Col)
	}
}

func TestFlowDeclIsFalseForOtherFiles(t *testing.T) {
	for name, src := range map[string]string{
		"a scenario file":  scenarioFileSrc,
		"a scenario":       "scenario \"s\" {\n  step \"a\" {\n    get \"/\"\n  }\n}\n",
		"a collection":     "collection \"auth\" {\n  request login() {\n    post \"/l\"\n  }\n}\n",
		"a var named flow": "scenario \"s\" {\n  var flow = 1\n}\n",
		"an empty file":    "",
	} {
		if _, got, ok := FlowDecl("f.art", src); ok {
			t.Errorf("%s: FlowDecl() = %q, true, want false", name, got)
		}
	}
}

func TestFlowHeaderIsThePathAsWritten(t *testing.T) {
	path, ok := FlowHeader("scenarios/checkout.art", "# data\n"+scenarioFileSrc)
	if !ok || path != "../flows/checkout.art" {
		t.Fatalf("FlowHeader() = %q, %v, want ../flows/checkout.art, true", path, ok)
	}
	if _, ok := FlowHeader("flows/checkout.art", flowSrc); ok {
		t.Error("a flow file has no header")
	}
}

func TestHeaderNamesResolvesFromTheScenarioFile(t *testing.T) {
	scen := filepath.Join("suite", "scenarios", "checkout.art")
	flow := filepath.Join("suite", "flows", "checkout.art")
	if !HeaderNames(scen, "../flows/checkout.art", flow) {
		t.Error("../flows/checkout.art from suite/scenarios does not name suite/flows/checkout.art")
	}
	if HeaderNames(scen, "flows/checkout.art", flow) {
		t.Error("flows/checkout.art from suite/scenarios names suite/flows/checkout.art")
	}
}

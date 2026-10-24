package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

const orders = `collection "orders" {
  request create(sku) {
    post "x" { body = {"sku": sku} }
    expect status == 201
    capture order_id = body.id
  }
}
`

func ordersFiles(main string) map[string]string {
	return map[string]string{"auth.art": auth, "orders.art": orders, "main.art": "import \"orders.art\"\nimport \"auth.art\"\n\nscenario \"s\" {\n" + main + "}\n"}
}

func TestStepNames(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.create { sku = \"A\" }\n  use orders.create as rush { sku = \"B\" }\n"))
	noDiags(t, diags)
	for _, want := range []string{
		`step "orders.create"`,
		`step "rush"`,
	} {
		if !contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestViaDistinguishesTwoUses(t *testing.T) {
	files := ordersFiles("  use orders.create as a { sku = \"1\" }\n  use orders.create as b { sku = \"2\" }\n")
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	sc := res.File.Scenarios[0].(*ast.Scenario)
	e1 := sc.Body[0].(*ast.StepDecl).Body[0].Span()
	e2 := sc.Body[1].(*ast.StepDecl).Body[0].Span()
	if e1.Via == e2.Via || e1.Via == 0 {
		t.Fatalf("Via %d and %d", e1.Via, e2.Via)
	}
	if e1.File != "orders.art" {
		t.Fatalf("an expanded expect must keep its collection span, got %s", e1.File)
	}
	c1, c2 := res.Chain(e1), res.Chain(e2)
	if len(c1) != 1 || c1[0].Line != 5 || c2[0].Line != 6 {
		t.Fatalf("chains %v %v", c1, c2)
	}
}

func TestSecretParamHoistsALiteralArgumentIntoASecretVar(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { pw = \"hunter2\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `secret var r_pw = "hunter2"`) || !contains(got, `{"p": r_pw}`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestSecretParamFromAPlainCaptureIsAnError(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  step \"t\" {\n    get \"y\"\n    capture tok = body.t\n  }\n  use c.r { pw = tok }\n}\n",
	})
	if !contains(diags, "secret-argument") {
		t.Fatalf("got %s", diags)
	}
}

// Beyond the brief.

func TestAsLeavesTheUsesOwnArgumentsAlone(t *testing.T) {
	got, diags := run(t, ordersFiles("  step \"t\" {\n    get \"y\"\n    capture token = body.t\n  }\n  use auth.login as co { user = token }\n"))
	noDiags(t, diags)
	if !contains(got, `"username": token`) || !contains(got, "capture co_token = body.token") {
		t.Fatalf("got\n%s", got)
	}
}

func TestAsOnARequestPrefixesItsCapture(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.create as rush { sku = \"B\" }\n"))
	noDiags(t, diags)
	if !contains(got, "capture rush_order_id = body.id") {
		t.Fatalf("got\n%s", got)
	}
}

const secretColl = "collection \"c\" {\n  request r(secret pw, auth = \"Basic ${pw}\", secret key = \"k\") {\n    post \"x\" {\n      header \"A\" = auth\n      body = {\"p\": pw, \"k\": key}\n    }\n  }\n}\n"

func TestHoistedSecretVarsSitAboveTheirUseInUseOrderAndDefaultsReadThem(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  var base = \"b\"\n  use c.r { pw = base }\n  use c.r as two { pw = \"x\" }\n  var tail = 1\n}\n",
	})
	noDiags(t, diags)
	for _, want := range []string{
		"  var base = \"b\"\n  secret var r_pw = base\n  secret var r_key = \"k\"\n\n  step \"c.r\"",
		"  secret var two_pw = \"x\"\n  secret var two_key = \"k\"\n\n  step \"two\"",
	} {
		if !contains(got, want) {
			t.Fatalf("missing\n%s\nin\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "  var tail = 1\n}\n") {
		t.Fatalf("got\n%s", got)
	}
	if !contains(got, `header "A" = "Basic ${r_pw}"`) {
		t.Fatalf("a default built from a secret parameter must read the hoisted var:\n%s", got)
	}
}

func TestSecretParamFromASecretCaptureIsSubstitutedAsWritten(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  step \"t\" {\n    get \"y\"\n    secret capture tok = body.t\n  }\n  use c.r { pw = tok }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `"p": tok`) || contains(got, "secret var r_pw") {
		t.Fatalf("got\n%s", got)
	}
}

func TestSecretArgumentDiagnosticNamesTheParamAndTheCapture(t *testing.T) {
	files := map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  step \"t\" {\n    get \"y\"\n    capture tok = body.t\n  }\n  use c.r { pw = \"${tok}!\" }\n}\n",
	}
	tree, _ := parser.Parse("main.art", files["main.art"])
	_, bag := Expand(tree, MapLoader(files))
	ds := bag.All()
	if len(ds) != 1 || ds[0].Message != "pw is secret in c.r, so its argument must be secret too" ||
		ds[0].Hint != "make tok a secret capture" || ds[0].Span.Line != 8 {
		t.Fatalf("got %+v", ds)
	}
}

// The request's argument reads the scenario's var token while the request
// captures token too: as renames the capture and leaves the argument alone.
// (The request once named its parameter token as well; a parameter is a var
// to the standalone check, so that is now duplicate-binding.)
func TestAsRenamesTheCaptureAndLeavesTheArgument(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(want) {\n    get \"x\"\n    expect body.token == want\n    capture token = body.token\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  var token = 2\n  use c.r as a { want = token }\n}\n",
	})
	noDiags(t, diags)
	for _, want := range []string{"expect body.token == token", "capture a_token = body.token"} {
		if !contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestHoistedSecretVarsGoAboveTheSteps(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  var base = \"b\"\n  use c.r { pw = base }\n}\n",
	})
	noDiags(t, diags)
	want := "scenario \"s\" {\n  var base = \"b\"\n  secret var r_pw = base\n  secret var r_key = \"k\"\n\n  step \"c.r\""
	if !strings.HasPrefix(got, want) {
		t.Fatalf("got\n%s", got)
	}
}

func TestHoistedSecretVarsGoAtTheTopWithNoVars(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { pw = \"x\" }\n}\n",
	})
	noDiags(t, diags)
	if !strings.HasPrefix(got, "scenario \"s\" {\n  secret var r_pw = \"x\"\n  secret var r_key = \"k\"\n") {
		t.Fatalf("got\n%s", got)
	}
}

func TestAnExpandedSecretParameterChecks(t *testing.T) {
	files := map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { pw = \"k\" }\n}\n",
	}
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if bag.Len() != 0 {
		t.Fatalf("expand: %v", bag.All())
	}
	if _, cb := check.Check(res.File); cb.Len() != 0 {
		t.Fatalf("check of the expanded tree: %v", cb.All())
	}
}

func TestAHoistedVarSitsAboveItsUseEvenWithALaterVar(t *testing.T) {
	files := map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { pw = \"x\" }\n  var y = 1\n}\n",
	}
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if bag.Len() != 0 {
		t.Fatalf("expand: %v", bag.All())
	}
	if _, cb := check.Check(res.File); cb.Len() != 0 {
		t.Fatalf("check of the expanded tree: %v", cb.All())
	}
	got := print.Canonical(res.File)
	if !contains(got, "  secret var r_pw = \"x\"\n\n  step \"c.r\"") {
		t.Fatalf("got\n%s", got)
	}
}

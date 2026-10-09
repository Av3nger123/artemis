package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
)

const orders = `import "auth.art"

collection "orders" {
  request create(sku) {
    post "x" { body = {"sku": sku} }
    expect status == 201
    capture order_id = body.id
  }

  flow checkout(user) {
    use auth.login { user = user }
    use create { sku = "A-1" }
    step "pay" {
      post "${order_id}/pay"
      header "Authorization" = "Bearer ${token}"
      expect status == 200
    }
  }
}
`

func ordersFiles(main string) map[string]string {
	return map[string]string{"auth.art": auth, "orders.art": orders, "main.art": "import \"orders.art\"\n\nscenario \"s\" {\n" + main + "}\n"}
}

func TestStepNames(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.checkout { user = \"a\" }\n  use orders.create as rush { sku = \"B\" }\n"))
	noDiags(t, diags)
	for _, want := range []string{
		`step "orders.checkout / auth.login"`,
		`step "orders.checkout / orders.create"`,
		`step "orders.checkout / pay"`,
		`step "rush"`,
	} {
		if !contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestAsPrefixesCapturesAndTheirLaterReadsInsideTheFlow(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.checkout as co { user = \"a\" }\n"))
	noDiags(t, diags)
	for _, want := range []string{
		"capture co_token = body.token",
		"capture co_order_id = body.id",
		`post "${co_order_id}/pay"`,
		`"Bearer ${co_token}"`,
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
	if len(c1) != 1 || c1[0].Line != 4 || c2[0].Line != 5 {
		t.Fatalf("chains %v %v", c1, c2)
	}
}

func TestNestedChainIsInnermostFirst(t *testing.T) {
	files := ordersFiles("  use orders.checkout { user = \"a\" }\n")
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, _ := Expand(tree, MapLoader(files))
	login := res.File.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	chain := res.Chain(login.Name.Span)
	if len(chain) != 2 || chain[0].File != "orders.art" || chain[1].File != "main.art" {
		t.Fatalf("chain = %v", chain)
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

func TestAsNamesEveryStepOfAFlow(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.checkout as co { user = \"a\" }\n"))
	noDiags(t, diags)
	for _, want := range []string{`step "co / auth.login"`, `step "co / orders.create"`, `step "co / pay"`} {
		if !contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestAsLeavesTheUsesOwnArgumentsAlone(t *testing.T) {
	got, diags := run(t, ordersFiles("  step \"t\" {\n    get \"y\"\n    capture token = body.t\n  }\n  use orders.checkout as co { user = token }\n"))
	noDiags(t, diags)
	if !contains(got, `"username": token`) || !contains(got, "capture co_token = body.token") {
		t.Fatalf("got\n%s", got)
	}
}

func TestAsAppliesAfterAnInBlockThatNamesTheWrittenStep(t *testing.T) {
	got, diags := run(t, ordersFiles("  use orders.checkout as co { user = \"a\", in \"pay\" { header \"X\" = \"1\" } }\n"))
	noDiags(t, diags)
	at := strings.Index(got, `step "co / pay"`)
	if at < 0 || !contains(got[at:], `header "X" = "1"`) {
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

func TestHoistedSecretVarsGoLastInUseOrderAndDefaultsReadThem(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    secretColl,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  var base = \"b\"\n  use c.r { pw = base }\n  use c.r as two { pw = \"x\" }\n  var tail = 1\n}\n",
	})
	noDiags(t, diags)
	want := "  var tail = 1\n  secret var r_pw = base\n  secret var r_key = \"k\"\n  secret var two_pw = \"x\"\n  secret var two_key = \"k\"\n}\n"
	if !strings.HasSuffix(got, want) {
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

func TestNestedSecretParamFromAFlowParamIsHoistedTwice(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n  }\n  flow f(secret pass) {\n    use r { pw = pass }\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f { pass = \"hunter2\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, "secret var f_pass = \"hunter2\"\n  secret var r_pw = f_pass\n") || !contains(got, `{"p": r_pw}`) {
		t.Fatalf("got\n%s", got)
	}
}

package expand

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
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

// The request's argument reads the scenario's var token while the request
// captures token too: as renames the capture and leaves the argument alone.
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

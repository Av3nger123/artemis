package expand

import (
	"fmt"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

func TestSkipRemovesTheExpectsThatReadIt(t *testing.T) {
	got := expandFlows(t, "# flow \"flows/checkout.art\"\nscenario \"s\" { var total = skip }\n",
		map[string]string{"flows/checkout.art": "scenario \"checkout\" {\n  var total = 100\n" +
			"  step \"pay\" {\n    post \"/p\"\n    expect status == 200\n    expect body.total == total\n  }\n}\n"},
		checkoutFlow)
	noDiags(t, got.diags)
	if !contains(got.out, "expect status == 200") || contains(got.out, "body.total") || contains(got.out, "var total") {
		t.Fatalf("want the expect on total and the var gone, the other expect kept:\n%s", got.out)
	}
}

func TestSkipLeavesTheObjectFieldOut(t *testing.T) {
	got := expandFlows(t, "# flow \"flows/checkout.art\"\nscenario \"s\" { var coupon = skip }\n",
		map[string]string{"flows/checkout.art": "scenario \"checkout\" {\n  var sku = \"S\"\n  var coupon = \"\"\n" +
			"  step \"pay\" {\n    post \"/p\" { body = {\"sku\": sku, \"coupon\": coupon} }\n  }\n}\n"},
		checkoutFlow)
	noDiags(t, got.diags)
	if !contains(got.out, `body = {"sku": sku}`) {
		t.Fatalf("want the coupon field left out:\n%s", got.out)
	}
}

func TestSkipInAURLIsAnError(t *testing.T) {
	got := expandFlows(t, "# flow \"flows/checkout.art\"\nscenario \"s\" { var coupon = skip }\n",
		map[string]string{"flows/checkout.art": "scenario \"checkout\" {\n  var coupon = \"\"\n" +
			"  step \"pay\" {\n    post \"/p/${coupon}\"\n  }\n}\n"},
		checkoutFlow)
	ds := only(got.bag, diag.SkipRead)
	if len(ds) != 1 || ds[0].Span.File != "flows/checkout.art" || ds[0].Span.Line != 4 {
		t.Fatalf("want one skip-read at the URL, got:\n%s", got.diags)
	}
	if len(ds[0].UsedFrom) != 1 || ds[0].UsedFrom[0].File != "main.art" || ds[0].UsedFrom[0].Line != 2 {
		t.Fatalf("the read chains to the scenario that skips it, got %v", ds[0].UsedFrom)
	}
	if len(got.bag.All()) != 1 {
		t.Fatalf("a skip-read is the one fault; the reads left behind must not cascade:\n%s", got.diags)
	}
}

func TestSkipReachesIntoAnItem(t *testing.T) {
	got := expandFlows(t, "# flow \"flows/checkout.art\"\nscenario \"s\" { var total = skip }\n",
		map[string]string{
			"flows/checkout.art": "import \"../collections/api.art\"\n\nscenario \"checkout\" {\n  var total = 1\n  use api.pay { total = total }\n}\n",
			"collections/api.art": "collection \"api\" {\n  request pay(total) {\n    post \"/p\"\n" +
				"    expect status == 200\n    expect body.total == total\n  }\n}\n",
		}, checkoutFlow)
	noDiags(t, got.diags)
	if !contains(got.out, "expect status == 200") || contains(got.out, "body.total") {
		t.Fatalf("want the item's expect on total gone:\n%s", got.out)
	}
}

// An argument the flow passes is written in the flow, so a skip-read of it
// is at the flow's line. Read from the context (Task 8), the read is the
// item's own: TestSkipReadInsideAnItemNamesTheChain.
func TestSkipReadThroughAnItemArgument(t *testing.T) {
	got := expandFlows(t, "# flow \"flows/checkout.art\"\nscenario \"s\" { var token = skip }\n",
		map[string]string{
			"flows/checkout.art": "import \"../collections/auth.art\"\n\nscenario \"checkout\" {\n  var token = \"t\"\n  use auth.me { token = token }\n}\n",
			"collections/auth.art": "collection \"auth\" {\n  request me(token) {\n" +
				"    get \"/me\" { header \"Authorization\" = \"Bearer ${token}\" }\n  }\n}\n",
		}, checkoutFlow)
	ds := only(got.bag, diag.SkipRead)
	if len(ds) != 1 || ds[0].Span.File != "flows/checkout.art" || ds[0].Span.Line != 5 {
		t.Fatalf("want one skip-read at the flow's argument, got:\n%s", got.diags)
	}
	if ch := ds[0].UsedFrom; len(ch) != 1 || ch[0].File != "main.art" || ch[0].Line != 2 {
		t.Fatalf("the read chains to the scenario; got %v", ch)
	}
}

// A token an item's step brought into a flow chains to the step, then to the
// scenario that brought the step in.
func TestChainFollowsAFlowsStepToItsScenario(t *testing.T) {
	scenario := token.Span{File: "main.art", Line: 2}
	step := token.Span{File: "flows/checkout.art", Line: 5, Via: 1}
	r := &Result{Uses: []Use{{Span: scenario}, {Span: step}}}
	ch := r.Chain(token.Span{File: "collections/auth.art", Line: 3, Via: 2})
	if len(ch) != 2 || ch[0] != step || ch[1] != scenario {
		t.Fatalf("want the step, then the scenario; got %v", ch)
	}
	if ch := r.Chain(token.Span{File: "x.art", Via: 3}); ch != nil {
		t.Fatalf("a Via past the table chains to nothing, got %v", ch)
	}
}

// pruned is src's one scenario with name pruned as skip, and each report as
// line:col code.
func pruned(t *testing.T, src, name string) (string, []string) {
	t.Helper()
	tree, bag := parser.Parse("main.art", src)
	if bag.HasErrors() {
		t.Fatalf("does not parse: %v", bag.All())
	}
	s := tree.Scenarios[0].(*ast.Scenario)
	var got []string
	s.Body = prune(s.Body, name, func(at token.Span, c diag.Code, _ string) {
		got = append(got, fmt.Sprintf("%d:%d %s", at.Line, at.Col, c))
	})
	return print.Canonical(tree), got
}

func TestSkipEndsWhenAStepWritesTheName(t *testing.T) {
	out, reports := pruned(t, "scenario \"s\" {\n  var total = 1\n"+
		"  step \"a\" {\n    get \"/\"\n    expect body.total == total\n    capture total = body.t\n  }\n"+
		"  step \"b\" {\n    get \"/x?t=${total}\"\n    expect total == 1\n  }\n}\n", "total")
	if len(reports) != 0 {
		t.Fatalf("after a write, total is a value: %v", reports)
	}
	if contains(out, "body.total") || !contains(out, "expect total == 1") || !contains(out, "capture total = body.t") {
		t.Fatalf("the expect before the write goes, the one after stays:\n%s", out)
	}
}

func TestSkipWrittenInOneBranchIsAnError(t *testing.T) {
	_, reports := pruned(t, "scenario \"s\" {\n  var x = 1\n  var total = 1\n"+
		"  if x == 1 {\n    step \"a\" {\n      get \"/\"\n      capture total = body.t\n    }\n  }\n"+
		"  step \"b\" {\n    get \"/\"\n    expect total == 1\n  }\n}\n", "total")
	if strings.Join(reports, ",") != "12:12 skip-maybe-written" {
		t.Fatalf("got %v", reports)
	}

	_, reports = pruned(t, "scenario \"s\" {\n  var x = 1\n  var total = 1\n"+
		"  if x == 1 {\n    step \"a\" {\n      get \"/\"\n      capture total = body.t\n    }\n  } else {\n"+
		"    step \"c\" {\n      get \"/\"\n      capture total = body.t\n    }\n  }\n"+
		"  step \"b\" {\n    get \"/\"\n    expect total == 1\n  }\n}\n", "total")
	if len(reports) != 0 {
		t.Fatalf("both branches write total: %v", reports)
	}
}

func TestSkipInBlocks(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"parallel writes it": {
			"  parallel {\n    step \"a\" {\n      get \"/\"\n      capture total = body.t\n    }\n    step \"c\" {\n      get \"/c\"\n    }\n  }\n" +
				"  step \"b\" {\n    get \"/\"\n    expect total == 1\n  }\n",
			"",
		},
		"repeat reads it above the write": {
			"  repeat max 3 {\n    step \"a\" {\n      get \"/\"\n      expect total == 1\n    }\n    step \"c\" {\n      get \"/\"\n      capture total = body.t\n    }\n  } until status == 200\n",
			"6:14 skip-maybe-written",
		},
		"a condition reads it": {
			"  if total == 1 {\n    step \"a\" {\n      get \"/\"\n    }\n  }\n",
			"3:6 skip-read",
		},
		"a switch with no default writes it in every case": {
			"  switch x {\n    case 1 {\n      step \"a\" {\n        get \"/\"\n        capture total = body.t\n      }\n    }\n  }\n" +
				"  step \"b\" {\n    get \"/\"\n    expect total == 1\n  }\n",
			"13:12 skip-maybe-written",
		},
		"another var reads it": {
			"  step \"a\" {\n    get \"/\"\n    capture y = total\n  }\n",
			"5:17 skip-read",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, reports := pruned(t, "scenario \"s\" {\n  var total = 1\n"+tc.body+"}\n", "total")
			if strings.Join(reports, ",") != tc.want {
				t.Fatalf("want %q, got %v", tc.want, reports)
			}
		})
	}
}

func TestSkipIsReserved(t *testing.T) {
	tree, _ := parser.Parse("main.art", "scenario \"s\" {\n  var skip = 1\n  step \"a\" {\n    get \"/\"\n  }\n}\n")
	_, bag := check.Check(tree)
	if ds := only(bag, diag.ReservedWord); len(ds) != 1 || ds[0].Span.Line != 2 {
		t.Fatalf("want skip reserved as a var's name, got %v", bag.All())
	}
}

// The plan's tests as source, for when the parser reads flow and scenario
// files and items take var inputs from the context (Tasks 4-6, 8).

func TestSkipRemovesTheExpectsThatReadItFromSource(t *testing.T) {
	out, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { total = skip }\n",
		"flows/checkout.art": "flow checkout {\n  var total = 100\n  step \"pay\" {\n    post \"/p\"\n    expect status == 200\n    expect body.total == total\n  }\n}\n",
	})
	noDiags(t, diags)
	if !contains(out, "expect status == 200") || contains(out, "body.total") || contains(out, "var total") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestSkipLeavesTheObjectFieldOutFromSource(t *testing.T) {
	out, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { coupon = skip }\n",
		"flows/checkout.art": "flow checkout {\n  var sku = \"S\"\n  var coupon = \"\"\n  step \"pay\" {\n    post \"/p\" { body = {\"sku\": sku, \"coupon\": coupon} }\n  }\n}\n",
	})
	noDiags(t, diags)
	if !contains(out, `body = {"sku": sku}`) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestSkipInAURLIsAnErrorFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { coupon = skip }\n",
		"flows/checkout.art": "flow checkout {\n  var coupon = \"\"\n  step \"pay\" {\n    post \"/p/${coupon}\"\n  }\n}\n",
	})
	if !contains(diags, "flows/checkout.art: skip-read") {
		t.Fatalf("got:\n%s", diags)
	}
}

func TestSkipReachesIntoAnItemFromSource(t *testing.T) {
	out, diags := run(t, map[string]string{
		"main.art":            "flow \"flows/checkout.art\"\nscenario \"s\" { total = skip }\n",
		"flows/checkout.art":  "import \"../collections/api.art\"\n\nflow checkout {\n  var total = 1\n  step \"pay\" = api.pay { }\n}\n",
		"collections/api.art": "collection \"api\" {\n  request pay {\n    var total\n    post \"/p\"\n    expect body.total == total\n  }\n}\n",
	})
	noDiags(t, diags)
	if contains(out, "body.total") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestSkipReadInsideAnItemNamesTheChain(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":             "flow \"flows/checkout.art\"\nscenario \"s\" { token = skip }\n",
		"flows/checkout.art":   "import \"../collections/auth.art\"\n\nflow checkout {\n  var token = \"t\"\n  step \"me\" = auth.me { }\n}\n",
		"collections/auth.art": "collection \"auth\" {\n  request me {\n    var token\n    get \"/me\" { header \"Authorization\" = \"Bearer ${token}\" }\n  }\n}\n",
	})
	if !contains(diags, "collections/auth.art: skip-read") {
		t.Fatalf("got:\n%s", diags)
	}
}

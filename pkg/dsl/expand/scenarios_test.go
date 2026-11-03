package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// checkout is a flow file.
const checkout = "flow checkout {\n  var quantity = 1\n  var total = 100\n" +
	"  step \"pay\" {\n    post \"/pay\" { body = {\"q\": quantity} }\n    expect body.total == total\n  }\n}\n"

func TestScenarioFileExpandsOneScenarioPerLine(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"full\" { }\nscenario \"coupon\" {\n  quantity = 3\n  total = 270\n}\n",
		map[string]string{"flows/checkout.art": checkout})
	noDiags(t, got.diags)
	full, coupon, ok := strings.Cut(got.out, "scenario \"coupon\"")
	if !ok || !contains(full, "scenario \"full\"") {
		t.Fatalf("want a scenario full, then a scenario coupon:\n%s", got.out)
	}
	for _, want := range []string{"var quantity = 1", "var total = 100", "step \"pay\"", "expect body.total == total"} {
		if !contains(full, want) {
			t.Errorf("scenario full has no %q:\n%s", want, full)
		}
	}
	for _, want := range []string{"var quantity = 3", "var total = 270", "step \"pay\""} {
		if !contains(coupon, want) {
			t.Errorf("scenario coupon has no %q:\n%s", want, coupon)
		}
	}
	for _, d := range got.res.File.Scenarios {
		if s, ok := d.(*ast.Scenario); !ok || s.Flow != "checkout" {
			t.Errorf("every expanded scenario runs flow checkout, got %#v", d)
		}
	}
}

func TestAStepInTheFlowChainsToItsScenario(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"full\" { }\nscenario \"coupon\" { quantity = 3 }\n",
		map[string]string{"flows/checkout.art": checkout})
	noDiags(t, got.diags)
	coupon := got.res.File.Scenarios[1].(*ast.Scenario)
	var step *ast.StepDecl
	for _, d := range coupon.Body {
		if s, ok := d.(*ast.StepDecl); ok {
			step = s
		}
	}
	chain := got.res.Chain(step.Name.Span)
	if step.Name.Span.File != "flows/checkout.art" || len(chain) != 1 || chain[0].File != "main.art" || chain[0].Line != 3 {
		t.Fatalf("the step is the flow's, and chains to scenario coupon on main.art:3; got %v via %v", step.Name.Span, chain)
	}
}

const authLogin = "collection \"auth\" {\n  request login(user) {\n    post \"/login\" { body = {\"user\": user} }\n    capture token = body.token\n  }\n}\n"

func TestFlowImportsResolveFromTheFlowFile(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		map[string]string{
			"flows/checkout.art":   "import \"../collections/auth.art\"\n\nflow checkout {\n  step \"log in\" = auth.login { user = \"a\" }\n}\n",
			"collections/auth.art": authLogin,
		})
	noDiags(t, got.diags)
	if !contains(got.out, `post "/login"`) || !contains(got.out, "capture token = body.token") {
		t.Fatalf("the flow's use was not expanded:\n%s", got.out)
	}
}

func TestStandaloneFlowsItemFaultChainsThroughTheStep(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		map[string]string{
			"flows/checkout.art":   "import \"../collections/auth.art\"\n\nflow checkout {\n  step \"log in\" = auth.login { }\n}\n",
			"collections/auth.art": authLogin,
		})
	ds := only(got.bag, diag.MissingArgument)
	if len(ds) != 1 || ds[0].Span.File != "flows/checkout.art" {
		t.Fatalf("want one missing-argument at the flow's use, got:\n%s", got.diags)
	}
	if len(ds[0].UsedFrom) != 1 || ds[0].UsedFrom[0].File != "main.art" || ds[0].UsedFrom[0].Line != 1 {
		t.Fatalf("the flow's fault chains to the scenario file's header, got %v", ds[0].UsedFrom)
	}
}

func TestScenarioCannotSetAnUnknownOrPrivateVar(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" {\n  quantty = 3\n  url = \"x\"\n}\n",
		map[string]string{"flows/checkout.art": "flow checkout {\n  var quantity = 1\n  private var url = \"http://h\"\n" +
			"  step \"pay\" {\n    post \"${url}/pay\" { body = {\"q\": quantity} }\n  }\n}\n"})
	unknown := only(got.bag, diag.UnknownParam)
	if len(unknown) != 1 || unknown[0].Span.Line != 3 || !contains(unknown[0].Hint, `did you mean "quantity"?`) {
		t.Fatalf("want unknown-param at quantty with a did-you-mean, got:\n%s", got.diags)
	}
	priv := only(got.bag, diag.PrivateParam)
	if len(priv) != 1 || priv[0].Span.Line != 4 || priv[0].Message != "url is private to flow checkout" {
		t.Fatalf("want private-param at url, got:\n%s", got.diags)
	}
	if contains(got.out, `var url = "x"`) || contains(got.out, "private") {
		t.Fatalf("the private var keeps its default, and the scenario holds it as a plain var:\n%s", got.out)
	}
}

func TestRequiredFlowVarMustBeSet(t *testing.T) {
	flow := map[string]string{"flows/checkout.art": "flow checkout {\n  var password\n" +
		"  step \"pay\" {\n    post \"/pay\" { body = {\"p\": password} }\n  }\n}\n"}
	got := expandFlows(t, "flow \"flows/checkout.art\"\nscenario \"s\" { }\n", flow)
	ds := only(got.bag, diag.MissingParam)
	if len(ds) != 1 || ds[0].Span.File != "main.art" || ds[0].Span.Line != 2 ||
		!contains(ds[0].Message, "password") || !contains(ds[0].Message, "checkout") {
		t.Fatalf("want missing-param naming password and checkout at scenario s, got:\n%s", got.diags)
	}

	got = expandFlows(t, "flow \"flows/checkout.art\"\nscenario \"s\" { password = env(\"PW\") }\n", flow)
	noDiags(t, got.diags)
	if !contains(got.out, `var password = env("PW")`) {
		t.Fatalf("the scenario's value is the var's:\n%s", got.out)
	}
}

func TestScenarioValueCannotReadAName(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" { total = \"${quantity}0\" }\n",
		map[string]string{"flows/checkout.art": checkout})
	ds := only(got.bag, diag.ScenarioValueReadsName)
	if len(ds) != 1 || ds[0].Span.File != "main.art" || ds[0].Span.Line != 2 || ds[0].Span.Col != 27 {
		t.Fatalf("want scenario-value-reads-name at quantity, got:\n%s", got.diags)
	}
	if contains(got.out, "${quantity}0") {
		t.Fatalf("a value that reads a name must never reach the flow, where it would read the flow's var:\n%s", got.out)
	}

	got = expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" {\n  quantity = env(\"Q\")\n  total = {\"n\": [1, \"${env(\"T\")}\"]}\n}\n",
		map[string]string{"flows/checkout.art": checkout})
	noDiags(t, got.diags)
}

func TestVarAfterTheFirstStepIsAnError(t *testing.T) {
	got := expandFlows(t, "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		map[string]string{"flows/checkout.art": "flow checkout {\n  var a = 1\n" +
			"  step \"pay\" {\n    get \"/pay\"\n  }\n  var b = 2\n}\n"})
	ds := only(got.bag, diag.VarAfterStep)
	if len(ds) != 1 || ds[0].Span.File != "flows/checkout.art" || ds[0].Span.Line != 6 {
		t.Fatalf("want var-after-step at var b, got:\n%s", got.diags)
	}
	if contains(got.out, "scenario") {
		t.Fatalf("a broken flow makes no scenario:\n%s", got.out)
	}
}

func TestFlowCannotDeclareTags(t *testing.T) {
	got := expandFlows(t, "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		map[string]string{"flows/checkout.art": "flow checkout {\n  var tags = 1\n" +
			"  step \"pay\" {\n    get \"/pay\"\n  }\n}\n"})
	if ds := only(got.bag, diag.ReservedTags); len(ds) != 1 || ds[0].Span.Line != 2 {
		t.Fatalf("want reserved-tags at var tags, got:\n%s", got.diags)
	}
}

func TestScenarioTagsAreCarried(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"s\" {\n  tags = [\"smoke\"]\n  total = 1\n}\nscenario \"t\" { }\n",
		map[string]string{"flows/checkout.art": checkout})
	noDiags(t, got.diags)
	s, rest, _ := strings.Cut(got.out, "scenario \"t\"")
	if !contains(s, `tags = ["smoke"]`) || contains(rest, "tags") {
		t.Fatalf("scenario s carries its tags, and t has none:\n%s", got.out)
	}
}

func TestFlowFaultIsReportedOnceAtTheFlow(t *testing.T) {
	got := expandFlows(t,
		"flow \"flows/checkout.art\"\nscenario \"a\" { }\nscenario \"b\" { }\n",
		map[string]string{"flows/checkout.art": "flow checkout {\n  step \"pay\" {\n    get \"/${nope}\"\n  }\n}\n"})
	ds := only(got.bag, diag.UnknownIdentifier)
	if len(ds) != 1 || ds[0].Span.File != "flows/checkout.art" || ds[0].Span.Line != 3 {
		t.Fatalf("want one unknown-identifier at the flow's line, got:\n%s", got.diags)
	}
	if len(ds[0].UsedFrom) != 1 || ds[0].UsedFrom[0].File != "main.art" {
		t.Fatalf("the flow's fault chains to the scenario file, got %v", ds[0].UsedFrom)
	}
}

func TestFlowHeaderNamesAFlowFile(t *testing.T) {
	got := expandFlows(t, "flow \"flows/nope.art\"\nscenario \"s\" { }\n", map[string]string{})
	if ds := only(got.bag, diag.FlowNotFound); len(ds) != 1 || ds[0].Span.File != "main.art" {
		t.Fatalf("want flow-not-found at the header, got:\n%s", got.diags)
	}
	got = expandFlows(t, "flow \"collections/auth.art\"\nscenario \"s\" { }\n",
		map[string]string{"collections/auth.art": authLogin})
	ds := only(got.bag, diag.NotAFlowFile)
	if len(ds) != 1 || ds[0].Message != "collections/auth.art is a collection file" {
		t.Fatalf("want not-a-flow-file naming a collection file, got:\n%s", got.diags)
	}
}

// The plan's tests as source, for when the parser reads flow and scenario
// files (Task 6).

func TestScenarioFileExpandsOneScenarioPerLineFromSource(t *testing.T) {
	files := map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"full\" { }\nscenario \"coupon\" { quantity = 3  total = 270 }\n",
		"flows/checkout.art": "flow checkout {\n  var quantity = 1\n  var total = 100\n  step \"pay\" {\n    post \"/pay\" { body = {\"q\": quantity} }\n    expect body.total == total\n  }\n}\n",
	}
	out, diags := run(t, files)
	noDiags(t, diags)
	full, coupon, _ := strings.Cut(out, "scenario \"coupon\"")
	for _, want := range []string{"scenario \"full\"", "var quantity = 1", "var total = 100", "step \"pay\""} {
		if !contains(full, want) {
			t.Errorf("scenario full has no %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"var quantity = 3", "var total = 270", "step \"pay\""} {
		if !contains(coupon, want) {
			t.Errorf("scenario coupon has no %q:\n%s", want, out)
		}
	}
}

func TestFlowImportsResolveFromTheFlowFileFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":             "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		"flows/checkout.art":   "import \"../collections/auth.art\"\n\nflow checkout {\n  step \"log in\" = auth.login { user = \"a\" }\n}\n",
		"collections/auth.art": authLogin,
	})
	noDiags(t, diags)
}

func TestScenarioCannotSetAnUnknownOrPrivateVarFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" {\n  quantty = 3\n  url = \"x\"\n}\n",
		"flows/checkout.art": "flow checkout {\n  var quantity = 1\n  private var url = \"http://h\"\n  step \"pay\" {\n    post \"${url}/pay\" { body = {\"q\": quantity} }\n  }\n}\n",
	})
	if !contains(diags, "main.art: unknown-param") || !contains(diags, "main.art: private-param") {
		t.Fatalf("got:\n%s", diags)
	}
}

func TestRequiredFlowVarMustBeSetFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		"flows/checkout.art": "flow checkout {\n  var password\n  step \"pay\" {\n    post \"/pay\" { body = {\"p\": password} }\n  }\n}\n",
	})
	if !contains(diags, "main.art: missing-param") || !contains(diags, "password") || !contains(diags, "checkout") {
		t.Fatalf("got:\n%s", diags)
	}
}

func TestScenarioValueCannotReadANameFromSource(t *testing.T) {
	files := map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { total = \"${quantity}0\" }\n",
		"flows/checkout.art": "flow checkout {\n  var quantity = 1\n  var total = 100\n  step \"pay\" {\n    post \"/pay\" { body = {\"q\": quantity} }\n    expect body.total == total\n  }\n}\n",
	}
	_, diags := run(t, files)
	if !contains(diags, "main.art: scenario-value-reads-name") {
		t.Fatalf("got:\n%s", diags)
	}
	files["main.art"] = "flow \"flows/checkout.art\"\nscenario \"s\" { quantity = env(\"X\")  total = json(\"d.json\") }\n"
	_, diags = run(t, files)
	noDiags(t, diags)
}

func TestVarAfterTheFirstStepIsAnErrorFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		"flows/checkout.art": "flow checkout {\n  step \"pay\" {\n    get \"/pay\"\n  }\n  var b = 2\n}\n",
	})
	if !contains(diags, "flows/checkout.art: var-after-step") {
		t.Fatalf("got:\n%s", diags)
	}
}

func TestFlowCannotDeclareTagsFromSource(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" { }\n",
		"flows/checkout.art": "flow checkout {\n  var tags = 1\n  step \"pay\" {\n    get \"/pay\"\n  }\n}\n",
	})
	if !contains(diags, "flows/checkout.art: reserved-tags") {
		t.Fatalf("got:\n%s", diags)
	}
}

func TestScenarioTagsAreCarriedFromSource(t *testing.T) {
	out, diags := run(t, map[string]string{
		"main.art":           "flow \"flows/checkout.art\"\nscenario \"s\" {\n  tags = [\"smoke\"]\n}\n",
		"flows/checkout.art": "flow checkout {\n  step \"pay\" {\n    get \"/pay\"\n  }\n}\n",
	})
	noDiags(t, diags)
	if !contains(out, `tags = ["smoke"]`) {
		t.Fatalf("got:\n%s", out)
	}
}

// A scenario file the parser already faulted does not load its flow: a
// missing flow would only add noise to the parse error.
func TestAScenarioFileThatDidNotParseLoadsNoFlow(t *testing.T) {
	tree, _ := parserParse("flow \"flows/missing.art\"\n\nscenario \"s\" {\n  step \"x\" {\n    get \"/\"\n  }\n}\n")
	_, bag := ExpandParsed(tree, MapLoader{}, false)
	if bag.HasErrors() {
		t.Fatalf("want no expansion diagnostics, got %v", bag.All())
	}
}

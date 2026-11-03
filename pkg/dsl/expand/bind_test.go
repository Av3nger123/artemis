package expand

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// loginItem is auth.login with user defaulted and password required; tries
// is made private by varInputs("tries").
const loginItem = "collection \"auth\" {\n  request login(user = \"admin\", password, tries = 1) {\n" +
	"    post \"/l\" { body = {\"u\": user, \"p\": password, \"n\": tries} }\n  }\n}\n"

// --- Task 7: an item whose inputs are vars binds like one with parameters.
// The parser does not read `var` lines above an item's action yet, so these
// build the item in Go: varInputs turns its parameters into Inputs.

func TestInputsUnifiesParamsAndVars(t *testing.T) {
	f, bag := parser.Parse("c.art", "collection \"c\" {\n  request r(a, secret b = 1) {\n    get \"/\"\n  }\n}\n")
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	r := f.Scenarios[0].(*ast.Collection).Items[0].(*ast.RequestDecl)
	check := func(got []input) {
		t.Helper()
		if len(got) != 2 || got[0].Name.Value != "a" || !isNil(got[0].Default) || got[0].Private ||
			got[1].Name.Value != "b" || isNil(got[1].Default) || got[1].Secret.Text != "secret" {
			t.Fatalf("got %+v", got)
		}
	}
	check(inputs(r))

	varInputs("b")(r)
	if r.Params != nil || len(r.Inputs) != 2 || !r.Inputs[0].Required() {
		t.Fatalf("varInputs left %+v / %+v", r.Params, r.Inputs)
	}
	got := inputs(r)
	if !got[1].Private || got[0].Private {
		t.Fatalf("private is not carried: %+v", got)
	}
	got[1].Private = false
	check(got)
}

func TestItemVarsBindLikeParamsInATree(t *testing.T) {
	got, diags := runEdited(t, map[string]string{
		"auth.art": loginItem,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { password = \"pw\" }\n}\n",
	}, varInputs("tries"))
	noDiags(t, diags)
	if !contains(got, `body = {"u": "admin", "p": "pw", "n": 1}`) {
		t.Fatalf("the defaults and the argument were not bound:\n%s", got)
	}
}

func TestRequiredItemVarIsMissingArgumentInATree(t *testing.T) {
	_, diags := runEdited(t, map[string]string{
		"auth.art": loginItem,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { }\n}\n",
	}, varInputs("tries"))
	if diags != "main.art: missing-argument: auth.login needs password" {
		t.Fatalf("got\n%s", diags)
	}
}

func TestPassingAPrivateItemVarIsAnErrorInATree(t *testing.T) {
	_, diags := runEdited(t, map[string]string{
		"auth.art": loginItem,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { password = \"pw\", tries = 2 }\n}\n",
	}, varInputs("tries"))
	if diags != "main.art: private-input: tries is private to auth.login" {
		t.Fatalf("got\n%s", diags)
	}
}

func TestPrivateInputHintSaysRemoveIt(t *testing.T) {
	files := map[string]string{
		"auth.art": loginItem,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { password = \"pw\", tries = 2 }\n}\n",
	}
	_, bag := expandEdited(t, files, varInputs("tries"))
	var d *diag.Diagnostic
	for _, x := range bag.All() {
		if x.Code == diag.PrivateInput {
			d = &x
		}
	}
	if d == nil {
		t.Fatalf("want private-input, got %v", bag.All())
	}
	if d.Span.File != "main.art" || d.Span.Line != 4 || d.Span.Col != 37 {
		t.Fatalf("want it at the argument's name, main.art:4:37, got %+v", d.Span)
	}
	if d.Hint != "remove it; a private var takes only its own default" {
		t.Fatalf("hint is %q", d.Hint)
	}
}

func TestAPrivateInputIgnoresTheContext(t *testing.T) {
	got, diags := runEdited(t, map[string]string{
		"auth.art": loginItem,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  var tries = 5\n  use auth.login { password = \"pw\" }\n}\n",
	}, varInputs("tries"))
	noDiags(t, diags)
	if !contains(got, `"n": 1}`) {
		t.Fatalf("a private input took the context's name:\n%s", got)
	}
}

func TestCommandItemExpandsInATree(t *testing.T) {
	got, diags := runEdited(t, map[string]string{
		"ops.art":  "collection \"ops\" {\n  request migrate(target = \"migrate\") {\n    run \"make\" { args = [target] }\n  }\n}\n",
		"main.art": "import \"ops.art\"\n\nscenario \"s\" {\n  use ops.migrate { }\n}\n",
	}, func(r *ast.RequestDecl) {
		varInputs()(r)
		r.Keyword.Text, r.Keyword.Value = "command", "command"
	})
	noDiags(t, diags)
	if !contains(got, `run "make"`) || !contains(got, `args = ["migrate"]`) {
		t.Fatalf("got\n%s", got)
	}
}

// The source-level forms of the tests above. They need item `var` inputs
// (Task 4), `private var` and `var x` (Task 2) and `step "n" = c.i { }`
// (Task 5) from the parser.

func TestItemVarsBindLikeParams(t *testing.T) {
	t.Skip("integration: needs Tasks 2,4,5")
	got, diags := run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login {\n    var user = \"admin\"\n    var password\n" +
			"    post \"/l\" { body = {\"u\": user, \"p\": password} }\n  }\n}\n",
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  step \"l\" = auth.login { password = \"pw\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `{"u": "admin", "p": "pw"}`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestRequiredItemVarIsMissingArgument(t *testing.T) {
	t.Skip("integration: needs Tasks 2,4,5")
	_, diags := run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login {\n    var user = \"admin\"\n    var password\n" +
			"    post \"/l\" { body = {\"u\": user, \"p\": password} }\n  }\n}\n",
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  step \"l\" = auth.login { }\n}\n",
	})
	if !contains(diags, "missing-argument") || !contains(diags, "password") {
		t.Fatalf("got\n%s", diags)
	}
}

func TestPassingAPrivateItemVarIsAnError(t *testing.T) {
	t.Skip("integration: needs Tasks 2,4,5")
	_, diags := run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login {\n    private var tries = 1\n    post \"/l\" { body = {\"n\": tries} }\n  }\n}\n",
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  step \"l\" = auth.login { tries = 2 }\n}\n",
	})
	if diags != "main.art: private-input: tries is private to auth.login" {
		t.Fatalf("got\n%s", diags)
	}
}

func TestCommandItemExpands(t *testing.T) {
	t.Skip("integration: needs Tasks 2,4,5")
	got, diags := run(t, map[string]string{
		"ops.art":  "collection \"ops\" {\n  command migrate {\n    var target = \"migrate\"\n    run \"make\" { args = [target] }\n  }\n}\n",
		"main.art": "import \"ops.art\"\n\nscenario \"s\" {\n  step \"m\" = ops.migrate { }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `run "make"`) || !contains(got, `args = ["migrate"]`) {
		t.Fatalf("got\n%s", got)
	}
}

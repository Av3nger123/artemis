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
	f, bag := parser.Parse("c.art", "collection \"c\" {\n  request r(a, b = 1) {\n    get \"/\"\n  }\n}\n")
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	r := f.Scenarios[0].(*ast.Collection).Items[0].(*ast.RequestDecl)
	check := func(got []input) {
		t.Helper()
		if len(got) != 2 || got[0].Name.Value != "a" || !isNil(got[0].Default) || got[0].Private ||
			got[1].Name.Value != "b" || isNil(got[1].Default) {
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
	got, diags := run(t, map[string]string{
		"ops.art":  "collection \"ops\" {\n  command migrate {\n    var target = \"migrate\"\n    run \"make\" { args = [target] }\n  }\n}\n",
		"main.art": "import \"ops.art\"\n\nscenario \"s\" {\n  step \"m\" = ops.migrate { }\n}\n",
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
	_, diags := run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login {\n    private var tries = 1\n    post \"/l\" { body = {\"n\": tries} }\n  }\n}\n",
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  step \"l\" = auth.login { tries = 2 }\n}\n",
	})
	if diags != "main.art: private-input: tries is private to auth.login" {
		t.Fatalf("got\n%s", diags)
	}
}

func TestCommandItemExpands(t *testing.T) {
	got, diags := run(t, map[string]string{
		"ops.art":  "collection \"ops\" {\n  command migrate {\n    var target = \"migrate\"\n    run \"make\" { args = [target] }\n  }\n}\n",
		"main.art": "import \"ops.art\"\n\nscenario \"s\" {\n  step \"m\" = ops.migrate { }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `run "make"`) || !contains(got, `args = ["migrate"]`) {
		t.Fatalf("got\n%s", got)
	}
}

// --- Task 8: an input the use does not pass is read from the context: a var
// of the scenario or a capture above the use, before the item's default.
// These use today's syntax: `request r(params)`, `use`, `capture`.

const contextLogin = "collection \"auth\" {\n  request login(url, user = \"admin\") {\n" +
	"    post \"${url}/l\" { body = {\"u\": user} }\n  }\n}\n"

func TestMissingInputComesFromTheContext(t *testing.T) {
	got, diags := run(t, map[string]string{
		"auth.art": contextLogin,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  var url = \"http://x\"\n  var user = \"bob\"\n  use auth.login { }\n}\n",
	})
	noDiags(t, diags)
	// The context name itself, not a copy of its value; and the scenario's
	// user wins over the item's default.
	if !contains(got, `post "${url}/l" { body = {"u": user} }`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestPassedValueBeatsTheContext(t *testing.T) {
	got, diags := run(t, map[string]string{
		"auth.art": contextLogin,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  var url = \"http://x\"\n  var user = \"bob\"\n  use auth.login { user = \"carol\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `body = {"u": "carol"}`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestTheDefaultIsUsedWhenTheContextLacksTheName(t *testing.T) {
	got, diags := run(t, map[string]string{
		"auth.art": contextLogin,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  var url = \"http://x\"\n  use auth.login { }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `body = {"u": "admin"}`) {
		t.Fatalf("got\n%s", got)
	}
}

const meItem = "collection \"api\" {\n  request me(token) {\n    get \"/me/${token}\"\n  }\n}\n"

func TestEarlierCaptureFeedsALaterItem(t *testing.T) {
	got, diags := run(t, map[string]string{
		"api.art": meItem,
		"main.art": "import \"api.art\"\n\nscenario \"s\" {\n  step \"a\" {\n    get \"/t\"\n    capture token = body.t\n  }\n" +
			"  use api.me { }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `get "/me/${token}"`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestACaptureBelowTheUseIsNotInItsContext(t *testing.T) {
	_, diags := run(t, map[string]string{
		"api.art":  meItem,
		"main.art": "import \"api.art\"\n\nscenario \"s\" {\n  use api.me { }\n  step \"a\" {\n    get \"/t\"\n    capture token = body.t\n  }\n}\n",
	})
	if diags != "main.art: missing-argument: api.me needs token" {
		t.Fatalf("got\n%s", diags)
	}
}

// A capture an earlier use brings in is in the context too, under the name
// as gives it.
func TestAnEarlierUsesCaptureFeedsALaterItem(t *testing.T) {
	got, diags := run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login() {\n    post \"/l\"\n    capture token = body.t\n  }\n}\n",
		"api.art":  meItem,
		"main.art": "import \"auth.art\"\nimport \"api.art\"\n\nscenario \"s\" {\n  use auth.login\n  use api.me\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `get "/me/${token}"`) {
		t.Fatalf("got\n%s", got)
	}
	_, diags = run(t, map[string]string{
		"auth.art": "collection \"auth\" {\n  request login() {\n    post \"/l\"\n    capture token = body.t\n  }\n}\n",
		"api.art":  meItem,
		"main.art": "import \"auth.art\"\nimport \"api.art\"\n\nscenario \"s\" {\n  use auth.login as admin\n  use api.me\n}\n",
	})
	if diags != "main.art: missing-argument: api.me needs token" {
		t.Fatalf("as renames the capture to admin_token, so token is not in the context; got\n%s", diags)
	}
}

// A capture in setup, or in a branch of a block above, is in the context of a
// use after it, and so is one above the block for a use inside it.
func TestCapturesAboveAndAroundABlockAreInTheContext(t *testing.T) {
	for name, body := range map[string]string{
		"setup":     "  setup {\n    step \"a\" {\n      get \"/t\"\n      capture token = body.t\n    }\n  }\n  use api.me\n",
		"if branch": "  var x = true\n  if x {\n    step \"a\" {\n      get \"/t\"\n      capture token = body.t\n    }\n  }\n  use api.me\n",
		"inside a block": "  var x = true\n  step \"a\" {\n    get \"/t\"\n    capture token = body.t\n  }\n" +
			"  if x {\n    use api.me\n  }\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, diags := run(t, map[string]string{
				"api.art":  meItem,
				"main.art": "import \"api.art\"\n\nscenario \"s\" {\n" + body + "}\n",
			})
			noDiags(t, diags)
			if !contains(got, `get "/me/${token}"`) {
				t.Fatalf("got\n%s", got)
			}
		})
	}
}

// The context name sits where the item reads the input, stamped with the
// use: a diagnostic on it points into the item, with the use as its chain.
func TestAContextNameIsPlacedAtTheItemsRead(t *testing.T) {
	tree, _ := parser.Parse("main.art", "import \"api.art\"\n\nscenario \"s\" {\n  var token = \"t\"\n  use api.me\n}\n")
	res, bag := Expand(tree, MapLoader{"api.art": meItem})
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	var id *ast.Ident
	ast.Inspect(res.File, func(n ast.Node) {
		if v, ok := n.(*ast.Ident); ok && v.Tok.Value == "token" && v.Tok.Span.File == "api.art" {
			id = v
		}
	})
	if id == nil {
		t.Fatalf("no token read in api.art:\n%s", ast.Source(res.File))
	}
	if sp := id.Tok.Span; sp.Line != 3 || sp.Col != 16 || sp.Via != 1 {
		t.Fatalf("want the read at api.art:3:16 with Via 1, got %+v", sp)
	}
	if ch := res.Chain(id.Tok.Span); len(ch) != 1 || ch[0].File != "main.art" || ch[0].Line != 5 {
		t.Fatalf("want the chain main.art:5, got %+v", ch)
	}
}

package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

const auth = `collection "auth" {
  request login(user, password = "pw", base = env("API_URL")) {
    post "${base}/token" {
      body = {"username": user, "password": password}
    }
    expect status == 200
    capture token = body.token
  }
}
`

func TestFileWithoutUsesIsUnchanged(t *testing.T) {
	src := "scenario \"s\" {\n  step \"a\" {\n    get \"x\"\n    expect status == 200\n  }\n}\n"
	got, diags := run(t, map[string]string{"main.art": src})
	noDiags(t, diags)
	if got != src {
		t.Fatalf("got\n%s", got)
	}
}

func TestFileWithoutUsesIsTheSameTree(t *testing.T) {
	tree, _ := parserParse("scenario \"s\" {\n  step \"a\" {\n    get \"x\"\n  }\n}\n")
	res, bag := Expand(tree, nil)
	if bag.Len() != 0 || res.File != tree {
		t.Fatalf("a file with no import, collection or use must expand to itself")
	}
}

func TestUseExpandsToAStepWithArgumentsSubstituted(t *testing.T) {
	got, diags := run(t, map[string]string{
		"auth.art": auth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"alice\" }\n}\n",
	})
	noDiags(t, diags)
	// The brief wrote the post block broken over three lines; canonical mode
	// renders a one-field block inline whenever it fits the margin (see
	// print.blockAfter), and auth.art itself prints the same way.
	want := `scenario "s" {
  step "auth.login" {
    post "${env("API_URL")}/token" { body = {"username": "alice", "password": "pw"} }
    expect status == 200
    capture token = body.token
  }
}
`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestDefaultSeesPassedEarlierParam(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(base, url = \"${base}/x\") {\n    get url\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { base = \"http://h\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `get "${"http://h"}/x"`) {
		t.Fatalf("the default did not see the passed base:\n%s", got)
	}
}

func TestBindingErrors(t *testing.T) {
	for name, tc := range map[string]struct{ use, code string }{
		"missing required": {`use auth.login {}`, "missing-argument"},
		"unknown argument": {`use auth.login { user = "a", usr = "b" }`, "unknown-argument"},
		"duplicate":        {`use auth.login { user = "a", user = "b" }`, "duplicate-argument"},
		"unknown item":     {`use auth.logn { user = "a" }`, "unknown-item"},
		"unknown coll":     {`use ath.login { user = "a" }`, "unknown-collection"},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := run(t, map[string]string{
				"auth.art": auth,
				"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  " + tc.use + "\n}\n",
			})
			if !contains(diags, "main.art: "+tc.code) {
				t.Fatalf("want %s at main.art, got:\n%s", tc.code, diags)
			}
		})
	}
}

func TestImportErrors(t *testing.T) {
	_, diags := run(t, map[string]string{"main.art": "import \"nope.art\"\n"})
	if !contains(diags, "import-not-found") {
		t.Fatalf("got %s", diags)
	}
	_, diags = run(t, map[string]string{
		"main.art": "import \"a.art\"\n",
		"a.art":    "import \"b.art\"\n",
		"b.art":    "import \"a.art\"\n",
	})
	if !contains(diags, "import-cycle") || !contains(diags, "a.art -> b.art -> a.art") {
		t.Fatalf("got %s", diags)
	}
	_, diags = run(t, map[string]string{
		"main.art": "import \"a.art\"\nimport \"b.art\"\n",
		"a.art":    "collection \"x\" {}\n",
		"b.art":    "collection \"x\" {}\n",
	})
	if !contains(diags, "duplicate-collection") {
		t.Fatalf("got %s", diags)
	}
}

func TestNilLoaderRejectsImport(t *testing.T) {
	tree, _ := parserParse("import \"a.art\"\n")
	_, bag := Expand(tree, nil)
	if !bag.HasErrors() || bag.All()[0].Code != "import-needs-file" {
		t.Fatalf("got %v", bag.All())
	}
}

// Beyond the brief: the behaviours later tasks lean on.

func TestImportsAreNotTransitive(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art": "import \"a.art\"\n\nscenario \"s\" {\n  use b.r {}\n}\n",
		"a.art":    "import \"b.art\"\n",
		"b.art":    "collection \"b\" {\n  request r() {\n    get \"x\"\n  }\n}\n",
	})
	if !contains(diags, "main.art: unknown-collection") {
		t.Fatalf("got %s", diags)
	}
}

func TestFlowExpandsItsStepsAndNestedUses(t *testing.T) {
	got, diags := run(t, map[string]string{
		"o.art": `collection "orders" {
  request create(sku) {
    post "/orders" {
      body = {"sku": sku}
    }
  }
  flow buy(item) {
    step "look" {
      get "/items/${item}"
    }
    use create { sku = item }
  }
}
`,
		"main.art": "import \"o.art\"\n\nscenario \"s\" {\n  use orders.buy { item = \"A-1\" }\n}\n",
	})
	noDiags(t, diags)
	for _, want := range []string{`step "orders.buy / look" {`, `get "/items/${"A-1"}"`, `step "orders.buy / orders.create" {`, `body = {"sku": "A-1"}`} {
		if !contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
}

func TestUseCycle(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  flow a() {\n    use b {}\n  }\n  flow b() {\n    use a {}\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.a {}\n}\n",
	})
	if !contains(diags, "use-cycle") || !contains(diags, "c.a -> c.b -> c.a") {
		t.Fatalf("got %s", diags)
	}
}

func TestBareRefOutsideCollection(t *testing.T) {
	_, diags := run(t, map[string]string{
		"auth.art": auth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use login { user = \"a\" }\n}\n",
	})
	if !contains(diags, "main.art: unknown-item") {
		t.Fatalf("got %s", diags)
	}
}

func TestOperandArgumentIsParenthesised(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(flag) {\n    get \"x\"\n    expect not flag\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { flag = true and false }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, "expect not (true and false)") {
		t.Fatalf("got\n%s", got)
	}
}

// An argument written above the request in another file, or above it in the
// same file, must not reorder the step: the action still comes first.
func TestArgumentDoesNotReorderTheStep(t *testing.T) {
	got, diags := run(t, map[string]string{
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { code = 201 }\n}\n",
		"c.art":    "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\ncollection \"c\" {\n  request r(code) {\n    get \"x\"\n    expect status == code\n  }\n}\n",
	})
	noDiags(t, diags)
	want := "scenario \"s\" {\n  step \"c.r\" {\n    get \"x\"\n    expect status == 201\n  }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	got, diags = run(t, map[string]string{
		"main.art": "scenario \"s\" {\n  use c.r { code = 201 }\n}\n\ncollection \"c\" {\n  request r(code) {\n    get \"x\"\n    expect status == code\n  }\n}\n",
	})
	noDiags(t, diags)
	if got != want {
		t.Fatalf("same file: got\n%s\nwant\n%s", got, want)
	}
}

func TestCopiesAreStampedAndChained(t *testing.T) {
	tree, _ := parserParse("import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"alice\" }\n}\n")
	res, bag := Expand(tree, MapLoader{"auth.art": auth})
	if bag.Len() != 0 {
		t.Fatalf("%v", bag.All())
	}
	if len(res.Uses) != 1 || res.Uses[0].Ref != "auth.login" || res.Uses[0].Parent != 0 {
		t.Fatalf("uses %+v", res.Uses)
	}
	step := res.File.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	for _, tk := range step.Tokens(nil) {
		if tk.Span.IsZero() {
			continue
		}
		if tk.Span.Line == 0 || tk.Span.Col == 0 {
			t.Fatalf("token %q has no position", tk.Text)
		}
	}
	if step.Keyword.Span.Via != 1 || step.Keyword.Span.Line == 0 {
		t.Fatalf("step keyword span %+v", step.Keyword.Span)
	}
	chain := res.Chain(step.Action.Span())
	if len(chain) != 1 || chain[0].File != "main.art" || chain[0].Line != 4 {
		t.Fatalf("chain %+v", chain)
	}
	if res.Chain(token.Span{}) != nil {
		t.Fatal("a span written in place has no chain")
	}
	if _, ok := res.Sources["auth.art"]; !ok {
		t.Fatal("sources")
	}
}

func TestUseRecordsTheItemItExpanded(t *testing.T) {
	files := map[string]string{
		"auth.art": auth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"u\" }\n}\n",
	}
	tree, _ := parserParse(files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	if len(res.Uses) != 1 {
		t.Fatalf("want one use, got %v", res.Uses)
	}
	it := res.Uses[0].Item
	if it.File != "auth.art" || it.Line != 2 {
		t.Fatalf("Item = %+v, want the request's name at auth.art:2", it)
	}
}

// defaulted is orders.art with a defaulted flow parameter that its nested use
// and its own step both read: line 13 is the flow's parameter list, line 14
// its nested use, line 16 its step.
const defaultedAuth = "collection \"auth\" {\n  request login(user, base = \"h\") {\n    post \"${base}/token\"\n" +
	"    expect status == 200\n    capture token = body.token\n  }\n}\n"

const defaultedOrders = `import "auth.art"

collection "orders" {
  request create(sku, qty = 1, base = env("API_URL")) {
    post "${base}/orders" {
      body = {"sku": sku, "qty": qty}
    }
    expect status == 201
    capture order_id = body.data.id
  }

  # the flow
  flow checkout(user, ref, base = env("API_URL")) {
    use auth.login { user = user, base = base }
    use create { sku = ref, base = base }
    step "pay" {
      post "${base}/orders/${order_id}/pay"
      expect status == 200
    }
  }
}
`

// A param default copied into a nested use line, or into a flow's step, has
// the template's file and Via; it must not pull the use's or the step's span
// back to the parameter list.
func TestADefaultDoesNotMoveANestedUseOrAStep(t *testing.T) {
	files := MapLoader{
		"auth.art":   defaultedAuth,
		"orders.art": defaultedOrders,
		"main.art":   "import \"orders.art\"\n\nscenario \"s\" {\n  use orders.checkout { user = \"a\", ref = \"r\" }\n}\n",
	}
	tree, _ := parserParse(files["main.art"])
	res, bag := Expand(tree, files)
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	body := res.File.Scenarios[0].(*ast.Scenario).Body
	var login, pay *ast.StepDecl
	for _, d := range body {
		if s, ok := d.(*ast.StepDecl); ok {
			switch s.Name.Value {
			case "orders.checkout / auth.login":
				login = s
			case "orders.checkout / pay":
				pay = s
			}
		}
	}
	if login == nil || pay == nil {
		t.Fatalf("steps missing:\n%v", body)
	}
	chain := res.Chain(login.Action.Span())
	if len(chain) != 2 || chain[0].File != "orders.art" || chain[0].Line != 14 || chain[0].Col != 5 ||
		chain[1].File != "main.art" || chain[1].Line != 4 {
		t.Fatalf("chain %+v, want orders.art:14:5 then main.art:4", chain)
	}
	if sp := pay.Span(); sp.File != "orders.art" || sp.Line != 16 {
		t.Fatalf("pay's span %+v, want it to start at orders.art:16", sp)
	}
}

// A request block an override synthesises sits at the override line; in a
// collection declared in the same file as the use, it must not stretch the
// request's span down to that line.
func TestASynthesisedBlockDoesNotStretchTheRequest(t *testing.T) {
	src := "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect status == 200\n  }\n}\n\n" +
		"scenario \"s\" {\n  use c.r { header \"X\" = \"1\" }\n}\n"
	tree, _ := parserParse(src)
	res, bag := Expand(tree, MapLoader{})
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	step := res.File.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	if sp := step.Action.Span(); sp.Line != 3 || sp.EndLine != 3 {
		t.Fatalf("request span %+v, want it on line 3", sp)
	}
	if sp := step.Span(); sp.Line != 2 || sp.EndLine != 5 {
		t.Fatalf("step span %+v, want lines 2-5", sp)
	}
}

// A default substituted into an expect must not move the expect above the
// action: the expanded step prints, and runs, in the order it was written.
func TestADefaultDoesNotReorderTheStep(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(code = 200) {\n    get \"http://x\"\n    expect status == code\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n}\n",
	})
	noDiags(t, diags)
	if g, e := strings.Index(got, "get "), strings.Index(got, "expect "); g < 0 || e < g {
		t.Fatalf("the action must come first:\n%s", got)
	}
}

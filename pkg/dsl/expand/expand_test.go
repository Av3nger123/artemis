package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
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
		"main.art": "import \"a/x.art\"\nimport \"b/x.art\"\n",
		"a/x.art":  "collection \"x\" {}\n",
		"b/x.art":  "collection \"x\" {}\n",
	})
	if !contains(diags, "duplicate-import-name") {
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
		"a.art":    "import \"b.art\"\n\ncollection \"a\" {}\n",
		"b.art":    "collection \"b\" {\n  request r() {\n    get \"x\"\n  }\n}\n",
	})
	if !contains(diags, "main.art: unknown-collection") {
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

// An argument written above the request, in the scenario's file, must not
// reorder the step: the action still comes first.
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
}

// An import names its file's collection, by `as` or by the file's name; the
// collection's own name is a title no use reads.
func TestAnImportNamesItsCollection(t *testing.T) {
	login := strings.Replace(auth, `collection "auth"`, `collection "Backend APIs"`, 1)
	got, diags := run(t, map[string]string{
		"Backend APIs.art": login,
		"main.art":         "import \"Backend APIs.art\" as api\n\nscenario \"s\" {\n  use api.login { user = \"a\", base = \"http://x\" }\n}\n",
	})
	noDiags(t, diags)
	if !contains(got, `step "api.login"`) {
		t.Fatalf("got\n%s", got)
	}
	// With no as, by the file's name, whatever the collection is titled.
	_, diags = run(t, map[string]string{
		"orders.collection.art": login,
		"main.art":              "import \"orders.collection.art\"\n\nscenario \"s\" {\n  use orders.login { user = \"a\", base = \"http://x\" }\n}\n",
	})
	noDiags(t, diags)

	for name, tc := range map[string]struct{ main, code, hint string }{
		"a file name that is not a name": {"import \"my-orders.art\"\n", "import-needs-name", `name it: import "my-orders.art" as my_orders`},
		"a file with no collection":      {"import \"plain.art\"\n", "import-no-collection", "import a collection file: a scenario file is run, never imported"},
		"two of one name":                {"import \"my-orders.art\" as x\nimport \"auth.art\" as x\n", "duplicate-import-name", "the first is on line 1; name one of them another way with as"},
	} {
		t.Run(name, func(t *testing.T) {
			files := MapLoader{"my-orders.art": auth, "auth.art": auth, "plain.art": "scenario \"s\" {}\n", "main.art": tc.main}
			tree, _ := parserParse(tc.main)
			_, bag := Expand(tree, files)
			found := false
			for _, d := range bag.All() {
				if string(d.Code) == tc.code && d.Span.File == "main.art" && d.Hint == tc.hint {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %s with hint %q, got %v", tc.code, tc.hint, bag.All())
			}
		})
	}
}

// A collection file holds one collection and no scenario: an import then
// names exactly one collection, the file's own.
func TestACollectionFileHoldsOneCollection(t *testing.T) {
	for name, tc := range map[string]struct{ file, src, code, hint string }{
		"a second":       {"orders.art", "collection \"orders\" {}\ncollection \"refunds\" {}\n", "second-collection", "move it to a file of its own"},
		"a scenario too": {"orders.art", "collection \"orders\" {}\n\nscenario \"s\" {}\n", "scenario-in-collection-file", "move the scenario to a scenario file, which imports orders.art"},
	} {
		t.Run(name, func(t *testing.T) {
			files := MapLoader{tc.file: tc.src, "main.art": "import \"" + tc.file + "\"\n"}
			tree, _ := parserParse(files["main.art"])
			_, bag := Expand(tree, files)
			found := false
			for _, d := range bag.All() {
				if string(d.Code) == tc.code && d.Span.File == tc.file && d.Hint == tc.hint {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %s at %s with hint %q, got %v", tc.code, tc.file, tc.hint, bag.All())
			}
		})
	}
	// The file on its own, as `artemis parse` or `run` reads it, is held to the same shape.
	tree, _ := parser.Parse("orders.art", "collection \"orders\" {}\n\nscenario \"s\" {}\n")
	if _, bag := Expand(tree, MapLoader{}); !bag.HasErrors() || bag.All()[0].Code != "scenario-in-collection-file" {
		t.Fatalf("got %v", bag.All())
	}
	// One collection, named after its file, and nothing else: no complaint.
	_, diags := run(t, map[string]string{"orders.art": "collection \"orders\" {}\n", "main.art": "import \"orders.art\"\n"})
	noDiags(t, diags)
}

func TestCopiesAreStampedAndChained(t *testing.T) {
	tree, _ := parserParse("import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"alice\" }\n}\n")
	res, bag := Expand(tree, MapLoader{"auth.art": auth})
	if bag.Len() != 0 {
		t.Fatalf("%v", bag.All())
	}
	if len(res.Uses) != 1 || res.Uses[0].Ref != "auth.login" {
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

// A request block an override synthesises sits at the override line, in the
// scenario's file; it must not stretch the request's span down to that line.
func TestASynthesisedBlockDoesNotStretchTheRequest(t *testing.T) {
	files := MapLoader{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\n\n\n\n\n\nscenario \"s\" {\n  use c.r { header \"X\" = \"1\" }\n}\n",
	}
	tree, _ := parserParse(files["main.art"])
	res, bag := Expand(tree, files)
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

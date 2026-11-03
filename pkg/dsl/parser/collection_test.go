package parser

import (
	"os"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, bag := Parse("t.art", src)
	if bag.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", bag.All())
	}
	if got := ast.Source(f); got != src {
		t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
	}
	return f
}

func TestCollectionsFixtureParsesAndRoundTrips(t *testing.T) {
	src, err := os.ReadFile("testdata/collections.art")
	if err != nil {
		t.Fatal(err)
	}
	f := mustParse(t, string(src))
	if len(f.Scenarios) != 3 {
		t.Fatalf("top level = %d decls, want import, collection, scenario", len(f.Scenarios))
	}
	if _, ok := f.Scenarios[0].(*ast.Import); !ok {
		t.Fatalf("first decl is %T", f.Scenarios[0])
	}
	c := f.Scenarios[1].(*ast.Collection)
	req := c.Items[0].(*ast.RequestDecl)
	if req.Name.Value != "create" || len(req.Params.List) != 3 || req.Params.List[1].Default == nil {
		t.Fatalf("request parsed wrong: %+v", req)
	}
	if pay := c.Items[1].(*ast.RequestDecl); pay.Params.List[1].Secret.Text != "secret" {
		t.Fatal("secret parameter lost its modifier")
	}
	sc := f.Scenarios[2].(*ast.Scenario)
	u := sc.Body[0].(*ast.UseDecl)
	if u.Ref() != "orders.create" || u.Alias.Value != "rush" {
		t.Fatalf("use = %q as %q", u.Ref(), u.Alias.Value)
	}
	kinds := []string{}
	for _, l := range u.Lines {
		switch l.(type) {
		case *ast.Field:
			kinds = append(kinds, "field")
		case *ast.BodySet:
			kinds = append(kinds, "bodyset")
		case *ast.Drop:
			kinds = append(kinds, "drop")
		case *ast.Expect:
			kinds = append(kinds, "expect")
		}
	}
	want := "field field bodyset drop drop expect"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("use lines = %q, want %q", got, want)
	}
	if a, b := u.Lines[3].(*ast.Drop).What.Value, u.Lines[4].(*ast.Drop).What.Value; a != "expects" || b != "captures" {
		t.Fatalf("drop lines = %q, %q", a, b)
	}
}

func TestBrokenUseStillRoundTrips(t *testing.T) {
	for _, src := range []string{
		"scenario \"s\" {\n  use\n}\n",
		"scenario \"s\" {\n  use a. { x = }\n}\n",
		"collection \"c\" {\n  request r( { get \"x\" }\n}\n",
		"collection \"c\" {\n  bogus\n}\n",
		"import\n",
	} {
		f, bag := Parse("t.art", src)
		if !bag.HasErrors() {
			t.Errorf("%q: expected a diagnostic", src)
		}
		if got := ast.Source(f); got != src {
			t.Errorf("%q: round trip gave %q", src, got)
		}
	}
}

func TestImportAfterScenarioIsAnError(t *testing.T) {
	_, bag := Parse("t.art", "scenario \"s\" {}\nimport \"a.art\"\n")
	if !bag.HasErrors() {
		t.Fatal("an import below a scenario must be reported")
	}
}

func TestParamNamedLikeAUseLineIsAnError(t *testing.T) {
	_, bag := Parse("t.art", "collection \"c\" {\n  request r(body) { get \"x\" }\n}\n")
	if !bag.HasErrors() {
		t.Fatal("a parameter named body cannot be passed and must be reported")
	}
}

func TestDropTakesExpectsOrCaptures(t *testing.T) {
	_, bag := Parse("t.art", "scenario \"s\" {\n  use c.r { drop capture }\n}\n")
	all := bag.All()
	if len(all) != 1 || !strings.Contains(all[0].Message, "\"expects\", \"captures\" or \"vars\"") {
		t.Fatalf("diagnostics = %v", all)
	}
	if !strings.Contains(all[0].Hint, "captures") {
		t.Errorf("hint = %q, want a did you mean captures", all[0].Hint)
	}
}

// A collection's name is a title: a use reads it by the name it is imported as.
func TestACollectionNameIsATitle(t *testing.T) {
	for _, ok := range []string{"auth", "Backend APIs", "my-coll"} {
		if _, bag := Parse("t.art", "collection \""+ok+"\" {}\n"); bag.HasErrors() {
			t.Errorf("%q: %v", ok, bag.All())
		}
	}
	for name, want := range map[string]string{"9lives": "_9lives", "a b": "a_b", "": "_"} {
		if got := identLike(name); got != want {
			t.Errorf("identLike(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestImportTakesAName(t *testing.T) {
	tree, bag := Parse("t.art", "import \"Backend APIs.art\" as api\nimport \"auth.art\"\n")
	if bag.HasErrors() {
		t.Fatalf("diagnostics = %v", bag.All())
	}
	if got := tree.Scenarios[0].(*ast.Import).Name.Value; got != "api" {
		t.Errorf("name = %q, want api", got)
	}
	if got := tree.Scenarios[1].(*ast.Import).Name.Text; got != "" {
		t.Errorf("name = %q, want none", got)
	}
	if _, bag := Parse("t.art", "import \"auth.art\" as\n"); !bag.HasErrors() {
		t.Error("an as with no name parsed")
	}
	for path, want := range map[string]struct {
		name string
		ok   bool
	}{"collections/orders.art": {"orders", true}, "orders.collection.art": {"orders", true}, "Backend APIs.art": {"Backend APIs", false}} {
		if name, ok := ImportName(path); name != want.name || ok != want.ok {
			t.Errorf("ImportName(%q) = %q, %v", path, name, ok)
		}
	}
}

// A comma is legal between a use block's lines, so the hint for two lines
// run together offers it -- not "there are no semicolons".
func TestMissingSeparatorInAUseBlockOffersAComma(t *testing.T) {
	_, bag := Parse("t.art", "scenario \"s\" {\n  use c.r { sku = \"a\" qty = 2 }\n}\n")
	all := bag.All()
	if len(all) != 1 || all[0].Code != diag.MissingSeparator {
		t.Fatalf("want one missing-separator, got %v", all)
	}
	if all[0].Hint != "separate with a comma or a newline" {
		t.Errorf("hint = %q", all[0].Hint)
	}
}

// The plan's full example: a required var, a private var and an output var
// after the action. Those three forms are Tasks 2 and 3; the typed items and
// their input vars are this test's own, and TestTypedItemsWithValuedInputs
// holds them on their own until the forms land.
func TestTypedItemsWithInputs(t *testing.T) {
	t.Skip("integration: needs Task 2 (private var, var with no value) and Task 3 (var after the action)")
	src := "collection \"auth\" {\n" +
		"  request login {\n    var user = \"admin\"\n    var password\n    private var tries = 1\n" +
		"    post \"/login\" { body = {\"user\": user} }\n    var token = body.token\n  }\n" +
		"  command migrate {\n    run \"make\" { args = [\"migrate\"] }\n  }\n" +
		"  query seed {\n    var db_url\n    db db_url { sql = \"select 1\" }\n  }\n}\n"
	f := mustParse(t, src)
	c := f.Scenarios[0].(*ast.Collection)
	login := c.Items[0].(*ast.RequestDecl)
	if len(login.Inputs) != 3 || login.Params != nil {
		t.Fatalf("login inputs = %d, params = %v", len(login.Inputs), login.Params)
	}
	if login.Inputs[2].Private.Text != "private" || !login.Inputs[1].Required() {
		t.Errorf("inputs = %+v", login.Inputs)
	}
	if got := c.Items[1].(*ast.RequestDecl).Type(); got != "terminal" {
		t.Errorf("migrate type = %q", got)
	}
	if got := c.Items[2].(*ast.RequestDecl).Type(); got != "db" {
		t.Errorf("seed type = %q", got)
	}
}

func TestTypedItemsWithValuedInputs(t *testing.T) {
	src := "collection \"auth\" {\n" +
		"  request login {\n    var user = \"admin\"\n    var base = env(\"U\")\n" +
		"    post \"${base}/login\" { body = {\"user\": user} }\n    capture token = body.token\n  }\n" +
		"  command migrate {\n    run \"make\" { args = [\"migrate\"] }\n  }\n" +
		"  query seed {\n    var db_url = env(\"DB\")\n    db db_url { sql = \"select 1\" }\n  }\n}\n"
	f := mustParse(t, src)
	c := f.Scenarios[0].(*ast.Collection)
	login := c.Items[0].(*ast.RequestDecl)
	if login.Params != nil || len(login.Inputs) != 2 || login.Inputs[1].Name.Value != "base" {
		t.Fatalf("login = params %v, inputs %+v", login.Params, login.Inputs)
	}
	if _, ok := login.Action.(*ast.Request); !ok || len(login.Body) != 1 {
		t.Fatalf("login action = %T, body = %d", login.Action, len(login.Body))
	}
	for i, want := range []string{"api", "terminal", "db"} {
		if got := c.Items[i].(*ast.RequestDecl).Type(); got != want {
			t.Errorf("item %d type = %q, want %q", i, got, want)
		}
	}
	if seed := c.Items[2].(*ast.RequestDecl); len(seed.Inputs) != 1 {
		t.Errorf("seed inputs = %d", len(seed.Inputs))
	}
}

func TestItemActionMustMatchItsType(t *testing.T) {
	for src, want := range map[string]struct{ at, msg, hint string }{
		"collection \"c\" {\n  request r {\n    run \"make\"\n  }\n}\n": {
			"run", "a request holds an api action, and this one is run", "make it a command, or use get, post, put, patch or delete"},
		"collection \"c\" {\n  command c {\n    get \"/\"\n  }\n}\n": {
			"get", "a command holds a terminal action, and this one is get", "make it a request, or use run"},
		"collection \"c\" {\n  query q {\n    run \"psql\"\n  }\n}\n": {
			"run", "a query holds a db action, and this one is run", "make it a command, or use db"},
	} {
		tree, bag := Parse("t.art", src)
		all := bag.All()
		if len(all) != 1 || all[0].Code != diag.ItemActionMismatch {
			t.Errorf("%q: diagnostics = %v", src, all)
			continue
		}
		d := all[0]
		if d.Message != want.msg || d.Hint != want.hint {
			t.Errorf("%q: got %q / %q", src, d.Message, d.Hint)
		}
		if got := src[d.Span.Offset : d.Span.Offset+len(want.at)]; got != want.at {
			t.Errorf("%q: span at %q, want %q", src, got, want.at)
		}
		if ast.Source(tree) != src {
			t.Errorf("%q: round trip lost bytes", src)
		}
	}
}

// The older form keeps its parameters and reads no vars above the action.
func TestItemWithParamsReadsNoInputs(t *testing.T) {
	f := mustParse(t, "collection \"c\" {\n  command m(target = \"x\") {\n    run \"make\" { args = [target] }\n  }\n}\n")
	m := f.Scenarios[0].(*ast.Collection).Items[0].(*ast.RequestDecl)
	if m.Params == nil || len(m.Params.List) != 1 || len(m.Inputs) != 0 {
		t.Fatalf("params = %v, inputs = %d", m.Params, len(m.Inputs))
	}
}

func TestInputNamedLikeAUseLineIsAnError(t *testing.T) {
	_, bag := Parse("t.art", "collection \"c\" {\n  request r {\n    var body = 1\n    get \"x\"\n  }\n}\n")
	all := bag.All()
	if len(all) != 1 || all[0].Code != diag.ReservedParam {
		t.Fatalf("diagnostics = %v", all)
	}
}

func TestStepCallsAnItem(t *testing.T) {
	src := "import \"auth.art\"\n\nscenario \"s\" {\n  step \"log in\" = auth.login as admin {\n    user = \"bob\"\n    drop vars\n  }\n}\n"
	f := mustParse(t, src)
	u, ok := f.Scenarios[1].(*ast.Scenario).Body[0].(*ast.UseDecl)
	if !ok {
		t.Fatalf("body[0] = %T", f.Scenarios[1].(*ast.Scenario).Body[0])
	}
	if u.Name.Value != "log in" || u.Name.Text != `"log in"` || u.Alias.Value != "admin" || u.Keyword.Text != "" {
		t.Fatalf("use = name %q alias %q keyword %q", u.Name.Text, u.Alias.Value, u.Keyword.Text)
	}
	if u.StepName() != "log in" || u.Ref() != "auth.login" {
		t.Errorf("StepName = %q, Ref = %q", u.StepName(), u.Ref())
	}
	if d, ok := u.Lines[1].(*ast.Drop); !ok || d.What.Value != "vars" {
		t.Errorf("lines[1] = %#v", u.Lines[1])
	}
}

// The step form is accepted wherever a step is: in setup, teardown and every
// block, and a bare sibling ref works as it does for a use.
func TestStepCallIsAStepEverywhere(t *testing.T) {
	src := "scenario \"s\" {\n  setup {\n    step \"a\" = auth.login { }\n  }\n" +
		"  parallel {\n    step \"b\" = auth.me { }\n    step \"c\" = me { }\n  }\n" +
		"  if x {\n    step \"d\" = auth.login {}\n  }\n  teardown {\n    step \"e\" = auth.logout { }\n  }\n}\n"
	f := mustParse(t, src)
	n := 0
	ast.Inspect(f, func(x ast.Node) {
		if u, ok := x.(*ast.UseDecl); ok && u.Step.Text == "step" {
			n++
		}
	})
	if n != 5 {
		t.Fatalf("found %d step calls, want 5", n)
	}
}

func TestStepCallNeedsABlock(t *testing.T) {
	src := "scenario \"s\" {\n  step \"a\" = auth.login\n  step \"b\" {\n    get \"/\"\n  }\n}\n"
	tree, bag := Parse("t.art", src)
	all := bag.All()
	if len(all) != 1 || !strings.Contains(all[0].Message, "expected \"{\" after auth.login") {
		t.Fatalf("diagnostics = %v", all)
	}
	if got := ast.Source(tree); got != src {
		t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
	}
	if _, ok := tree.Scenarios[0].(*ast.Scenario).Body[1].(*ast.StepDecl); !ok {
		t.Errorf("the step after it was lost: %T", tree.Scenarios[0].(*ast.Scenario).Body[1])
	}
}

func TestBrokenStepCallStillRoundTrips(t *testing.T) {
	for _, src := range []string{
		"scenario \"s\" {\n  step \"a\" = { }\n}\n",
		"scenario \"s\" {\n  step \"a\" = auth. { }\n}\n",
		"scenario \"s\" {\n  step \"a\" = auth.login as { }\n}\n",
		"scenario \"s\" {\n  step \"a\" = auth.login { 1 }\n}\n",
		"scenario \"s\" {\n  step \"a\" = auth.login {\n",
	} {
		tree, bag := Parse("t.art", src)
		if !bag.HasErrors() {
			t.Errorf("%q parsed clean", src)
		}
		if got := ast.Source(tree); got != src {
			t.Errorf("round trip lost bytes:\n got %q\nwant %q", got, src)
		}
	}
}

// A var that does not parse ends the inputs and keeps its bytes.
func TestBrokenInputStillRoundTrips(t *testing.T) {
	src := "collection \"c\" {\n  request r {\n    var = 1\n    var u = 2\n    get \"x\"\n  }\n}\n"
	tree, bag := Parse("t.art", src)
	if !bag.HasErrors() {
		t.Fatal("a var with no name parsed")
	}
	if got := ast.Source(tree); got != src {
		t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
	}
}

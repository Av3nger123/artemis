package front_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

// Compile must report the checker's findings and not only the parser's. The
// fixture parses and then fails the checker, so a Compile that forgot to merge
// the checker's bag would return no diagnostics at all.
func TestCompileMergesTheCheckersBag(t *testing.T) {
	path := filepath.Join("..", "testdata", "invalid", "action_not_first.art")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	tree, info, bag := front.Compile(path, string(src))

	if tree == nil {
		t.Fatal("Compile returned no tree; the fixture is meant to parse")
	}
	if info == nil {
		t.Fatal("Compile returned no checker Info")
	}
	if !bag.HasErrors() {
		t.Fatalf("Compile found no errors in %s, which exists to have one", path)
	}
}

func TestIsArtFileIgnoresCase(t *testing.T) {
	for _, path := range []string{"x.art", "x.ART", "dir/x.Art"} {
		if !front.IsArtFile(path) {
			t.Errorf("IsArtFile(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"x.yaml", "x", "x.artemis"} {
		if front.IsArtFile(path) {
			t.Errorf("IsArtFile(%q) = true, want false", path)
		}
	}
}

func TestCompileWithExpandsAndChecksTheExpandedTree(t *testing.T) {
	files := expand.MapLoader{
		"c.art":    "collection \"c\" {\n  request r(u) {\n    get u\n    expect status == 200\n    capture tok = body.t\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { u = \"x\" }\n  step \"next\" {\n    get \"${tok}\"\n    expect status == 200\n  }\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
	if u.Tree == u.Expanded {
		t.Fatal("a file with a use must expand to a new tree")
	}
	if u.Info.Steps() != 2 {
		t.Fatalf("Info is about %d steps; want the 2 expanded ones", u.Info.Steps())
	}
	if _, ok := u.Sources["c.art"]; !ok {
		t.Fatal("Sources must hold the collection, to render its diagnostics")
	}
	if _, ok := u.Sources["main.art"]; !ok {
		t.Fatal("Sources must hold the root file too")
	}
	if len(u.Uses) != 1 || u.Uses[0].Ref != "c.r" || u.Uses[0].Item.File != "c.art" {
		t.Fatalf("Uses must be the expansion's use table, got %+v", u.Uses)
	}
}

func TestCompileWithAttachesTheUseChainToCheckerDiagnostics(t *testing.T) {
	files := expand.MapLoader{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n    capture tok = body.t\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  step \"a\" {\n    get \"y\"\n    capture tok = body.t\n  }\n  use c.r\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	var found bool
	for _, d := range u.Bag.All() {
		if d.Span.File == "c.art" && len(d.UsedFrom) == 1 && d.UsedFrom[0].Line == 8 {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the capture clash in c.art used from main.art:8, got %v", u.Bag.All())
	}
}

// An argument naming a scenario capture that a flow also captures: the flow's
// capture is the second binding of the name, and the checker -- running on the
// expanded tree -- says so, names the use line, and points at as.
func TestArgumentCannotShadowFlowCapture(t *testing.T) {
	auth := "collection \"auth\" {\n  request login(user, password = \"pw\", base = env(\"API_URL\")) {\n" +
		"    post \"${base}/token\" {\n      body = {\"username\": user, \"password\": password}\n    }\n" +
		"    expect status == 200\n    capture token = body.token\n  }\n}\n"
	files := expand.MapLoader{
		"auth.art": auth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  step \"t\" {\n    get \"x\"\n    capture token = body.t\n  }\n  use auth.login { user = token }\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	var got []diag.Diagnostic
	for _, d := range u.Bag.All() {
		if d.Code == diag.DuplicateBinding {
			got = append(got, d)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one duplicate-binding, got %v", u.Bag.All())
	}
	d := got[0]
	if d.Span.File != "auth.art" || d.Span.Line != 7 || len(d.UsedFrom) != 1 || d.UsedFrom[0].File != "main.art" || d.UsedFrom[0].Line != 8 {
		t.Fatalf("want the clash at auth.art:7 used from main.art:8, got %+v", d)
	}
	if d.Hint != "the capture at line 6 of main.art binds it first\nuse ... as <name> to keep both" {
		t.Fatalf("hint is %q", d.Hint)
	}
}

func TestCompileWithoutImportsIsCompile(t *testing.T) {
	src := "scenario \"s\" {\n  step \"a\" {\n    get \"x\"\n    expect status == 200\n  }\n}\n"
	tree, info, bag := front.Compile("a.art", src)
	u := front.CompileWith("a.art", src, nil)
	if u.Tree != u.Expanded || ast.Source(u.Tree) != ast.Source(tree) || bag.Len() != u.Bag.Len() || info.Steps() != u.Info.Steps() {
		t.Fatal("CompileWith must equal Compile for a file with no imports")
	}
}

// secretAuth is a collection whose request takes a secret parameter, so a use
// of it with a literal argument hoists `secret var login_password`.
const secretAuth = "collection \"auth\" {\n  request login(user, secret password) {\n" +
	"    post \"http://h/token\" {\n      body = {\"username\": user, \"password\": password}\n    }\n" +
	"    expect status == 200\n    secret capture token = body.token\n  }\n}\n"

// dupBindings is every duplicate-binding of name in u.
func dupBindings(u *front.Unit, name string) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range u.Bag.All() {
		if d.Code == diag.DuplicateBinding && d.Message == `"`+name+`" is already bound` {
			out = append(out, d)
		}
	}
	return out
}

// Two uses of one request with a secret argument, without as, would each
// hoist `secret var login_password`; the second var silently overwrote the
// first, so both requests sent the second password. It is duplicate-binding.
func TestTwoUsesHoistingOneSecretVarAreDuplicateBinding(t *testing.T) {
	files := expand.MapLoader{
		"auth.art": secretAuth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"a\", password = \"first\" }\n" +
			"  use auth.login { user = \"a\", password = \"second\", drop captures }\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	got := dupBindings(u, "login_password")
	if len(got) != 1 {
		t.Fatalf("want one duplicate-binding of login_password, got %v", u.Bag.All())
	}
	d := got[0]
	if d.Span.File != "auth.art" || d.Span.Line != 2 || len(d.UsedFrom) != 1 || d.UsedFrom[0].File != "main.art" || d.UsedFrom[0].Line != 5 {
		t.Fatalf("want the clash at auth.art:2 used from main.art:5, got %+v", d)
	}
	if !strings.Contains(d.Hint, "use ... as <name> to keep both") {
		t.Fatalf("hint is %q", d.Hint)
	}
}

// A scenario's own var of the name a use hoists is a clash too, above or below.
func TestAScenarioVarClashesWithAHoistedVar(t *testing.T) {
	for _, main := range []string{
		"import \"auth.art\"\n\nscenario \"s\" {\n  var login_password = \"x\"\n  use auth.login { user = \"a\", password = \"p\" }\n}\n",
		"import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"a\", password = \"p\" }\n  var login_password = \"x\"\n}\n",
	} {
		files := expand.MapLoader{"auth.art": secretAuth, "main.art": main}
		u := front.CompileWith("main.art", main, files)
		if got := dupBindings(u, "login_password"); len(got) != 1 {
			t.Errorf("want one duplicate-binding of login_password, got %v\n%s", u.Bag.All(), main)
		}
	}
}

// With as, each use hoists its own var and the file compiles.
func TestTwoUsesWithAsHoistTwoVars(t *testing.T) {
	files := expand.MapLoader{
		"auth.art": secretAuth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login as a { user = \"a\", password = \"first\" }\n" +
			"  use auth.login as b { user = \"a\", password = \"second\" }\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
}

// Two uses of one request that captures: the first binding is the first
// use's copy of the capture, on the very line the caret is on, so "the capture
// at line 7 binds it first" points at itself. The hint names the use instead.
func TestDuplicateBindingHintNamesTheFirstUse(t *testing.T) {
	files := expand.MapLoader{
		"auth.art": "collection \"auth\" {\n  request login(user) {\n    post \"http://h/token\"\n    expect status == 200\n" +
			"    capture token = body.token\n  }\n}\n",
		"twice.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"a\" }\n  use auth.login { user = \"b\" }\n}\n",
	}
	u := front.CompileWith("twice.art", files["twice.art"], files)
	got := dupBindings(u, "token")
	if len(got) != 1 {
		t.Fatalf("want one duplicate-binding of token, got %v", u.Bag.All())
	}
	if want := "the use at twice.art:4 binds it first\nuse ... as <name> to keep both"; got[0].Hint != want {
		t.Fatalf("hint is %q, want %q", got[0].Hint, want)
	}
}

package front_test

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

// Compile must report the checker's findings and not only the parser's. The
// flow parses and then fails the checker, so a front end that forgot to merge
// the checker's bag would return no diagnostics at all.
func TestCompileMergesTheCheckersBag(t *testing.T) {
	files := expand.MapLoader{
		"f.art": "flow f {\n  step orders {\n    get \"/orders\"\n    expect statu == 200\n  }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	if u.Tree == nil || u.Info == nil {
		t.Fatal("CompileWith returned no tree or no checker Info")
	}
	var found bool
	for _, d := range u.Bag.All() {
		found = found || d.Code == diag.UnknownField && d.Span.File == "f.art" && d.Span.Line == 4
	}
	if !found {
		t.Fatalf("want the checker's unknown-field at f.art:4, got %v", u.Bag.All())
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
		"c.art": "collection c {\n  request r {\n    var u\n    get u\n    expect status == 200\n    var tok = body.t\n  }\n}\n",
		"f.art": "import \"c.art\"\n\nflow s {\n  step call = c.r { u = \"x\" }\n  step next {\n    get \"${tok}\"\n    expect status == 200\n  }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
	if u.Tree == u.Expanded {
		t.Fatal("a scenario file must expand to a new tree")
	}
	if u.Info.Steps() != 2 {
		t.Fatalf("Info is about %d steps; want the 2 expanded ones", u.Info.Steps())
	}
	for _, name := range []string{"main.art", "f.art", "c.art"} {
		if _, ok := u.Sources[name]; !ok {
			t.Fatalf("Sources must hold %s, to render its diagnostics", name)
		}
	}
	var call bool
	for _, use := range u.Uses {
		call = call || use.Ref == "c.r" && use.Item.File == "c.art"
	}
	if !call {
		t.Fatalf("Uses must be the expansion's use table, with the call of c.r, got %+v", u.Uses)
	}
}

// An argument naming a name the flow wrote that the item writes too: the item
// reads the old value and its var overwrites it, as any later write does. The
// last write wins.
func TestAnItemMayOverwriteTheNameItsArgumentReads(t *testing.T) {
	files := expand.MapLoader{
		"auth.art": "collection auth {\n  request login {\n    var user\n    var password = \"pw\"\n" +
			"    post \"http://h/token\" {\n      body = {\"username\": user, \"password\": password}\n    }\n" +
			"    expect status == 200\n    var token = body.token\n  }\n}\n",
		"f.art": "import \"auth.art\"\n\nflow s {\n  step t {\n    get \"http://h/x\"\n    var token = body.t\n  }\n" +
			"  step log_in as \"log in\" = auth.login { user = token }\n  step after {\n    get \"http://h/${token}\"\n  }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
}

// Compile is CompileWith with no disk to read from.
func TestCompileIsCompileWithNoLoader(t *testing.T) {
	src := "flow s {\n  step a {\n    get \"x\"\n    expect status == 200\n  }\n}\n"
	tree, info, bag := front.Compile("a.art", src)
	u := front.CompileWith("a.art", src, nil)
	if ast.Source(u.Tree) != ast.Source(tree) || bag.Len() != u.Bag.Len() || info.Steps() != u.Info.Steps() {
		t.Fatal("CompileWith with no loader must equal Compile")
	}
}

// Review Focus 4: an item input that the call does not pass is read from the
// context, and a name that only one if branch creates is not bound on every
// path. That is capture-maybe-unbound at compile time -- where the item reads
// the input, with the call and then the scenario as the chain -- not
// missing-argument, and not a runtime error.
func TestContextInputFromOneBranchIsMaybeUnbound(t *testing.T) {
	files := expand.MapLoader{
		"api.art": "collection api {\n  request me {\n    var token\n    get \"http://h/me/${token}\"\n  }\n}\n",
		"f.art": "import \"api.art\"\n\nflow s {\n  var x = true\n  if x {\n    step a {\n      get \"http://h/\"\n" +
			"      var token = body.t\n    }\n  }\n  step me = api.me { }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	var got []diag.Diagnostic
	for _, d := range u.Bag.All() {
		if d.Severity == diag.Error {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Code != diag.CaptureMaybeUnbound {
		t.Fatalf("want one capture-maybe-unbound, got %v", u.Bag.All())
	}
	d := got[0]
	if d.Span.File != "api.art" || d.Span.Line != 4 || len(d.UsedFrom) != 2 ||
		d.UsedFrom[0].File != "f.art" || d.UsedFrom[0].Line != 11 || d.UsedFrom[1].File != "main.art" {
		t.Fatalf("want it at api.art:4 used from the call at f.art:11, then main.art, got %+v", d)
	}
}

// Two calls of one item that writes a var, without as: the second call's var
// overwrites the first's, and a step after both reads the second.
func TestTwoCallsOfAWritingItemOverwriteTheVar(t *testing.T) {
	files := expand.MapLoader{
		"auth.art": "collection auth {\n  request login {\n    var user\n    post \"http://h/token\"\n    expect status == 200\n" +
			"    var token = body.token\n  }\n}\n",
		"f.art": "import \"auth.art\"\n\nflow s {\n  step a = auth.login { user = \"a\" }\n  step b = auth.login { user = \"b\" }\n" +
			"  step after {\n    get \"http://h/${token}\"\n  }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
}

// Review Focus 1: one flow, two scenario files that name it. A mistake in the
// flow is reported by each file's unit once, at the flow's line, chained to
// that file.
func TestFlowErrorReportedPerScenarioFile(t *testing.T) {
	files := expand.MapLoader{
		"flows/checkout.art": "flow checkout {\n  step pay {\n    get \"/${nope}\"\n  }\n}\n",
		"a.art":              "flow \"flows/checkout.art\"\nscenario a1 { }\nscenario a2 { }\n",
		"b.art":              "flow \"flows/checkout.art\"\nscenario b1 { }\n",
	}
	for _, name := range []string{"a.art", "b.art"} {
		u := front.CompileWith(name, files[name], files)
		var got []diag.Diagnostic
		for _, d := range u.Bag.All() {
			if d.Code == diag.UnknownIdentifier {
				got = append(got, d)
			}
		}
		if len(got) != 1 || got[0].Span.File != "flows/checkout.art" || got[0].Span.Line != 3 {
			t.Fatalf("%s: want one unknown-identifier at flows/checkout.art:3, got %v", name, u.Bag.All())
		}
		if ch := got[0].UsedFrom; len(ch) != 1 || ch[0].File != name {
			t.Fatalf("%s: the fault chains to %s, got %v", name, name, ch)
		}
	}
}

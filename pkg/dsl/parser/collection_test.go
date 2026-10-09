package parser

import (
	"os"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
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
	flow := c.Items[1].(*ast.FlowDecl)
	if flow.Params.List[1].Secret.Text != "secret" {
		t.Fatal("secret parameter lost its modifier")
	}
	if u := flow.Body[1].(*ast.UseDecl); u.Ref() != "create" {
		t.Fatalf("sibling ref = %q", u.Ref())
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
	in := sc.Body[1].(*ast.UseDecl).Lines[2].(*ast.In)
	if in.Step.Value != "pay" || len(in.Lines) != 1 {
		t.Fatalf("in block = %+v", in)
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
	if len(all) != 1 || !strings.Contains(all[0].Message, "\"expects\" or \"captures\"") {
		t.Fatalf("diagnostics = %v", all)
	}
	if !strings.Contains(all[0].Hint, "captures") {
		t.Errorf("hint = %q, want a did you mean captures", all[0].Hint)
	}
}

func TestCollectionNameMustBeAnIdentifier(t *testing.T) {
	_, bag := Parse("t.art", "collection \"my-coll\" {\n  request r() { get \"x\" }\n}\n")
	all := bag.All()
	if len(all) != 1 || all[0].Code != "bad-collection-name" {
		t.Fatalf("diagnostics = %v", all)
	}
	if !strings.Contains(all[0].Hint, "my_coll") {
		t.Errorf("hint = %q, want it to suggest my_coll", all[0].Hint)
	}
	for _, ok := range []string{"auth", "_x", "orders2"} {
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

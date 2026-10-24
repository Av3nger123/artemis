package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

func TestFreeNameInARequestIsAnErrorInTheCollection(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"${url}/x\"\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  var url = \"h\"\n  use c.r\n}\n",
	})
	if !contains(diags, "c.art: ") || !contains(diags, `"url"`) {
		t.Fatalf("want the free name reported against c.art, got\n%s", diags)
	}
}

func TestBrokenCollectionReportedOnceAndNotExpanded(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n  use c.r as again\n}\n",
	})
	if n := strings.Count(diags, "statu"); n != 1 {
		t.Fatalf("reported %d times:\n%s", n, diags)
	}
	if contains(got, "statu") {
		t.Fatalf("a broken collection must not be expanded:\n%s", got)
	}
}

func TestParamsAreInScopeAndDefaultsMayNameEarlierParams(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(a, b = \"${a}/x\") {\n    get b\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { a = \"h\" }\n}\n",
	})
	noDiags(t, diags)
}

func TestACollectionFileOnItsOwnIsCheckedToo(t *testing.T) {
	tree, _ := parser.Parse("c.art", "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n")
	_, bag := Expand(tree, MapLoader{})
	if len(bag.All()) != 1 || !contains(bag.All()[0].Message, "statu") {
		t.Fatalf("got %v", bag.All())
	}
}

func TestStandaloneDiagnosticsHaveRealSpans(t *testing.T) {
	tree, _ := parser.Parse("c.art", "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n")
	_, bag := Expand(tree, MapLoader{})
	if !bag.HasErrors() {
		t.Fatal("want the broken collection reported")
	}
	for _, d := range bag.All() {
		if d.Span.Line == 0 || d.Span.File != "c.art" {
			t.Fatalf("diagnostic with no position: %+v", d)
		}
	}
}

// The standalone check builds its step against a scratch use table: the real
// Result.Uses holds only the use the output came from, and the check's
// diagnostics carry no Via, pointing at the collection as written.
func TestStandaloneErrorsHaveNoVia(t *testing.T) {
	files := map[string]string{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"${nope}\"\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n}\n",
	}
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if !bag.HasErrors() {
		t.Fatal("want the free name reported")
	}
	for _, d := range bag.All() {
		if d.Span.Via != 0 {
			t.Fatalf("a standalone diagnostic carries Via %d: %+v", d.Span.Via, d)
		}
	}
	if len(res.Uses) != 1 {
		t.Fatalf("the scratch uses leaked into the use table: %+v", res.Uses)
	}
}

func TestAFailedImportHidesTheUnknownCollection(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art": "import \"gone.art\"\n\nscenario \"s\" {\n  use c.r\n}\n",
	})
	if !contains(diags, "import-not-found") || contains(diags, "unknown-collection") {
		t.Fatalf("got\n%s", diags)
	}
}

// A template capture named after an observation root -- body here -- would
// be renamed by as (z_body) along with a later step's read of the root
// itself, so expect body.y would read the capture. It is refused in the
// collection, at the capture's name.
func TestATemplateCaptureMayNotTakeAnObservationRootsName(t *testing.T) {
	weird := "collection \"weird\" {\n  request r() {\n    get \"http://h/x\"\n    capture body = body.x\n  }\n}\n"
	tree, _ := parser.Parse("main.art", "import \"weird.art\"\n\nscenario \"s\" {\n  use weird.r as z\n  step \"next\" {\n    get \"http://h/y\"\n    expect body.y == 1\n  }\n}\n")
	_, bag := Expand(tree, MapLoader{"weird.art": weird, "main.art": ""})
	all := bag.All()
	if len(all) != 1 {
		t.Fatalf("want one diagnostic, got %v", all)
	}
	d := all[0]
	if d.Code != diag.RootCapture || d.Span.File != "weird.art" || d.Span.Line != 4 || d.Span.Col != 13 {
		t.Fatalf("want root-capture at weird.art:4:13, got %+v", d)
	}
	if d.Hint != "rename it, e.g. body_value; a later step's `body` would be ambiguous" {
		t.Fatalf("hint is %q", d.Hint)
	}
	for _, root := range []string{"status", "raw", "headers", "exit_code", "stdout", "stderr", "page"} {
		c := "collection \"c\" {\n  request r() {\n    get \"http://h/x\"\n    capture " + root + " = body.x\n  }\n}\n"
		tree, _ := parser.Parse("main.art", "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n}\n")
		_, bag := Expand(tree, MapLoader{"c.art": c})
		if all := bag.All(); len(all) != 1 || all[0].Code != diag.RootCapture {
			t.Errorf("capture %s: got %v", root, all)
		}
	}
}

// A free name in a template is something a scenario would have to supply, and
// a collection takes everything from outside as a parameter: the hint says so.
func TestAFreeNameHintSaysAddAParameter(t *testing.T) {
	tree, _ := parser.Parse("main.art", "import \"c.art\"\n\nscenario \"s\" {\n  var url = \"h\"\n  use c.r { base = \"b\" }\n}\n")
	_, bag := Expand(tree, MapLoader{"c.art": "collection \"c\" {\n  request r(base) {\n    get \"${url}/x\"\n    expect status == 200\n  }\n}\n"})
	all := bag.All()
	if len(all) != 1 || all[0].Code != diag.UnknownIdentifier {
		t.Fatalf("want one unknown-identifier, got %v", all)
	}
	if !strings.HasPrefix(all[0].Hint, "add it as a parameter of c.r\n") {
		t.Fatalf("hint is %q", all[0].Hint)
	}
}

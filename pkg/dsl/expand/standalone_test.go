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

func TestFlowSeesItsOwnEarlierCaptures(t *testing.T) {
	_, diags := run(t, ordersFiles("  use orders.checkout { user = \"a\" }\n"))
	noDiags(t, diags)
}

func TestParamsAreInScopeAndDefaultsMayNameEarlierParams(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(a, b = \"${a}/x\") {\n    get b\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { a = \"h\" }\n}\n",
	})
	noDiags(t, diags)
}

func TestCollectionInTheSameFileIsCheckedToo(t *testing.T) {
	_, diags := run(t, map[string]string{
		"main.art": "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n\nscenario \"s\" {\n  use c.r\n}\n",
	})
	if !contains(diags, "main.art: ") || !contains(diags, "statu") {
		t.Fatalf("got %s", diags)
	}
}

func TestStandaloneDiagnosticsHaveRealSpans(t *testing.T) {
	tree, _ := parser.Parse("main.art", "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n")
	_, bag := Expand(tree, MapLoader{})
	if !bag.HasErrors() {
		t.Fatal("want the broken collection reported")
	}
	for _, d := range bag.All() {
		if d.Span.Line == 0 || d.Span.File != "main.art" {
			t.Fatalf("diagnostic with no position: %+v", d)
		}
	}
}

// The standalone check of a flow expands its nested uses into a scratch use
// table: the real Result.Uses holds only the uses the output came from, and no
// diagnostic points into the scratch table.
func TestStandaloneCheckLeavesTheUseTableAlone(t *testing.T) {
	files := ordersFiles("  use orders.checkout { user = \"a\" }\n")
	tree, _ := parser.Parse("main.art", files["main.art"])
	res, bag := Expand(tree, MapLoader(files))
	if bag.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %v", bag.All())
	}
	// checkout, and the two uses it writes.
	if len(res.Uses) != 3 {
		t.Fatalf("want 3 uses, got %d: %+v", len(res.Uses), res.Uses)
	}
	for _, tok := range res.File.Tokens(nil) {
		if tok.Span.Via > len(res.Uses) {
			t.Fatalf("token %q has Via %d past the use table", tok.Text, tok.Span.Via)
		}
	}
}

func TestStandaloneErrorsInANestedFlowHaveNoVia(t *testing.T) {
	files := map[string]string{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect status == 200\n  }\n  flow f() {\n    use r as one\n    step \"t\" {\n      get \"${nope}\"\n      expect status == 200\n    }\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f\n}\n",
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

// A secret parameter of a nested use hoists into the synthetic scenario only:
// nothing leaks into the real output.
func TestStandaloneHoistingDoesNotLeak(t *testing.T) {
	got, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(secret pw) {\n    post \"x\" { body = {\"p\": pw} }\n    expect status == 200\n  }\n  flow f() {\n    use r { pw = \"k\" }\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  step \"a\" {\n    get \"x\"\n    expect status == 200\n  }\n}\n",
	})
	noDiags(t, diags)
	if contains(got, "secret var") {
		t.Fatalf("a standalone hoist leaked:\n%s", got)
	}
}

func TestAFlowUsingABrokenCollectionIsBrokenToo(t *testing.T) {
	got, diags := run(t, map[string]string{
		"b.art":    "collection \"b\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n    capture id = body.id\n  }\n}\n",
		"c.art":    "import \"b.art\"\n\ncollection \"c\" {\n  flow f() {\n    use b.r\n    step \"t\" {\n      get \"${id}\"\n      expect status == 200\n    }\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f\n}\n",
	})
	if n := strings.Count(diags, "\n") + 1; n != 1 {
		t.Fatalf("want only the one root error, got\n%s", diags)
	}
	if contains(got, "step") {
		t.Fatalf("a flow over a broken collection must not expand:\n%s", got)
	}
}

func TestSiblingCollectionDeclaredLaterIsCheckedFirst(t *testing.T) {
	got, diags := run(t, map[string]string{
		"main.art": "collection \"a\" {\n  flow f() {\n    use b.r\n  }\n}\n\ncollection \"b\" {\n  request r() {\n    get \"x\"\n    expect statu == 200\n  }\n}\n\nscenario \"s\" {\n  use a.f\n}\n",
	})
	if n := strings.Count(diags, "statu"); n != 1 {
		t.Fatalf("reported %d times:\n%s", n, diags)
	}
	if contains(got, "step") {
		t.Fatalf("want nothing expanded:\n%s", got)
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
	weird := "collection \"weird\" {\n  request r() {\n    get \"http://h/x\"\n    capture body = body.x\n  }\n" +
		"  flow f() {\n    use r\n    step \"next\" {\n      get \"http://h/y\"\n      expect body.y == 1\n    }\n  }\n}\n"
	tree, _ := parser.Parse("main.art", "import \"weird.art\"\n\nscenario \"s\" {\n  use weird.f as z\n}\n")
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

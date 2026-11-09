package front_test

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

// Each old form is its own code, at the word that makes it old, with a hint
// that names the new form and ends by naming artemis migrate on this file.
func TestOldFormsAreErrors(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		code            diag.Code
		line, col       int
		hint            string
	}{
		{"a scenario with steps", "a.art",
			"scenario \"s\" {\n  step \"a\" {\n    get \"/\"\n  }\n}\n",
			diag.OldScenario, 1, 1, "flow <name> { ... }"},
		{"a scenario with no step and no header", "a.art",
			"scenario \"s\" {\n  var x = 1\n}\n",
			diag.OldScenario, 1, 1, `flow "<path to the flow>"`},
		{"capture", "f.art",
			"flow f {\n  step \"a\" {\n    get \"/\"\n    capture id = body.id\n  }\n}\n",
			diag.OldCapture, 4, 5, "var id = ... after the step's action"},
		{"use", "f.art",
			"import \"c.art\"\n\nflow f {\n  use c.r { }\n}\n",
			diag.OldUse, 4, 3, `step <id> = c.r { ... }`},
		{"params", "c.art",
			"collection \"c\" {\n  request r(user, password = \"pw\") {\n    get \"/\"\n  }\n}\n",
			diag.OldParams, 2, 12, "var user"},
		{"secret var", "f.art",
			"flow f {\n  secret var pw = env(\"PW\")\n  step \"a\" {\n    get \"/\"\n  }\n}\n",
			diag.OldSecret, 2, 3, "remove the word"},
		{"secret capture in an item", "c.art",
			"collection \"c\" {\n  request r {\n    get \"/\"\n    secret capture id = body.id\n  }\n}\n",
			diag.OldSecret, 4, 5, "remove the word"},
		{"a suite block", "x/suite.art",
			"suite {\n  var url = \"u\"\n}\n",
			diag.OldSuite, 1, 1, "into each flow under the folder"},
		{"a file named suite.art", "x/suite.art",
			"# nothing\n",
			diag.OldSuite, 2, 1, "into each flow under the folder"},
		{"a suite block elsewhere", "a.art",
			"suite {\n  var url = \"u\"\n}\n",
			diag.OldSuite, 1, 1, "its tags onto each scenario"},
		{"for rows", "a.art",
			"scenario \"s ${n}\" for n in [1, 2] {\n  step \"a\" {\n    get \"/\"\n  }\n}\n",
			diag.OldFor, 1, 19, "a scenario that sets n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := front.CompileWith(tc.file, tc.src, expand.MapLoader{})
			var got []diag.Diagnostic
			for _, d := range u.Bag.All() {
				if d.Code == tc.code {
					got = append(got, d)
				}
			}
			if len(got) != 1 {
				t.Fatalf("want one %s, got %v", tc.code, u.Bag.All())
			}
			d := got[0]
			if d.Span.File != tc.file || d.Span.Line != tc.line || d.Span.Col != tc.col {
				t.Errorf("at %s:%d:%d, want %s:%d:%d", d.Span.File, d.Span.Line, d.Span.Col, tc.file, tc.line, tc.col)
			}
			if !strings.Contains(d.Hint, tc.hint) {
				t.Errorf("hint %q does not say %q", d.Hint, tc.hint)
			}
			if want := " -- artemis migrate -f " + tc.file + " -o <dir> rewrites it"; !strings.HasSuffix(d.Hint, want) {
				t.Errorf("hint %q does not end %q", d.Hint, want)
			}
		})
	}
}

// An old file says it is old and nothing more: what expansion and the checker
// would add is about a file artemis migrate rewrites. A capture and a use in
// an old scenario are the scenario's, and are not reported again.
func TestAnOldFileIsReportedAsOneAndGoesNoFurther(t *testing.T) {
	files := expand.MapLoader{"c.art": "collection \"c\" {\n  request r(u) {\n    get u\n  }\n}\n"}
	src := "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { u = \"/\" }\n  step \"a\" {\n    get \"/${nope}\"\n" +
		"    capture id = body.id\n  }\n}\n"
	u := front.CompileWith("a.art", src, files)
	ds := u.Bag.All()
	if len(ds) != 1 || ds[0].Code != diag.OldScenario {
		t.Fatalf("want old-scenario alone, got %v", ds)
	}
	if u.Expanded != u.Tree || u.Info == nil {
		t.Fatal("an old file is not expanded, and still has an Info")
	}
}

// A flow file in the new form that imports a collection in the old one is
// reported where the old form is: the collection's file.
func TestAnOldFormInAFileReadIsReported(t *testing.T) {
	files := expand.MapLoader{
		"c.art": "collection \"c\" {\n  request r(u) {\n    get u\n    capture id = body.id\n  }\n}\n",
		"f.art": "import \"c.art\"\n\nflow f {\n  step call = c.r { u = \"/\" }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { }\n", files)
	codes := map[diag.Code]string{}
	for _, d := range u.Bag.All() {
		codes[d.Code] = d.Span.File
	}
	if codes[diag.OldParams] != "c.art" || codes[diag.OldCapture] != "c.art" {
		t.Fatalf("want old-params and old-capture in c.art, got %v", u.Bag.All())
	}
	for _, d := range u.Bag.All() {
		if d.Code == diag.OldParams && !strings.HasSuffix(d.Hint, "artemis migrate -f c.art -o <dir> rewrites it") {
			t.Errorf("the hint names the file the old form is in: %q", d.Hint)
		}
	}
}

// The new forms are not old: a flow, a scenario file, an item with var
// inputs and a step that calls it compile clean.
func TestTheNewFormsAreNotOld(t *testing.T) {
	files := expand.MapLoader{
		"c.art": "collection c {\n  request r {\n    var u\n    get u\n    var id = body.id\n  }\n}\n",
		"f.art": "import \"c.art\"\n\nflow f {\n  var path = \"/\"\n  step call = c.r { u = path }\n" +
			"  step next {\n    get \"/${id}\"\n    var other = body.x\n  }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario s { path = \"/x\" }\n", files)
	if len(u.Bag.All()) != 0 {
		t.Fatalf("want no diagnostics, got %v", u.Bag.All())
	}
}

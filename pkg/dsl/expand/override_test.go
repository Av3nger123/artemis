package expand

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

func useLogin(lines string) map[string]string {
	return map[string]string{
		"auth.art": auth,
		"main.art": "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login {\n    user = \"a\"\n" + lines + "  }\n}\n",
	}
}

func TestHeaderAndQueryOverridesReplaceOrAdd(t *testing.T) {
	got, diags := run(t, useLogin("    header \"X-T\" = \"1\"\n    query \"dry\" = true\n"))
	noDiags(t, diags)
	for _, want := range []string{`header "X-T" = "1"`, `query "dry" = true`} {
		if !contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestBodyFieldOverrideSetsOneField(t *testing.T) {
	got, diags := run(t, useLogin("    body.password = \"x\"\n    body.extra.deep = 1\n"))
	noDiags(t, diags)
	if !contains(got, `body = {"username": "a", "password": "x", "extra": {"deep": 1}}`) {
		t.Fatalf("got\n%s", got)
	}
}

func TestBodyFieldOverrideOnNonLiteralBodyIsAnError(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(p) {\n    post \"x\" { body = p }\n    expect status == 200\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { p = {}, body.a = 1 }\n}\n",
	})
	if !contains(diags, "bad-override") || !contains(diags, "not an object literal") {
		t.Fatalf("got %s", diags)
	}
}

func TestDropExpectsThenExpectReplacesAssertions(t *testing.T) {
	got, diags := run(t, useLogin("    drop expects\n    expect status == 401\n"))
	noDiags(t, diags)
	if contains(got, "status == 200") || !contains(got, "status == 401") {
		t.Fatalf("got\n%s", got)
	}
}

func TestExpectWithoutDropIsAppended(t *testing.T) {
	got, _ := run(t, useLogin("    expect body.token exists\n"))
	if !contains(got, "status == 200") || !contains(got, "body.token exists") {
		t.Fatalf("got\n%s", got)
	}
}

func TestDropAfterExpectIsAnError(t *testing.T) {
	_, diags := run(t, useLogin("    expect status == 401\n    drop expects\n"))
	if !contains(diags, "drop-after-expect") {
		t.Fatalf("got %s", diags)
	}
}

func TestOverrideShapeErrors(t *testing.T) {
	c := "collection \"c\" {\n  request r() {\n    run \"true\"\n    expect exit_code == 0\n  }\n}\n"
	_, diags := run(t, map[string]string{"c.art": c, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { header \"X\" = \"1\" }\n}\n"})
	if !contains(diags, "main.art: bad-override") {
		t.Fatalf("got %s", diags)
	}
}

// Beyond the brief.

func TestHeaderOverrideReplacesCaseInsensitivelyAndQueryExactly(t *testing.T) {
	c := "collection \"c\" {\n  request r() {\n    get \"x\" {\n      header \"X-Token\" = \"old\"\n      query \"Page\" = 1\n    }\n  }\n}\n"
	got, diags := run(t, map[string]string{
		"c.art":    c,
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r {\n    header \"x-token\" = \"new\"\n    query \"page\" = 2\n    query \"Page\" = 3\n  }\n}\n",
	})
	noDiags(t, diags)
	want := "scenario \"s\" {\n  step \"c.r\" {\n    get \"x\" {\n      header \"X-Token\" = \"new\"\n      query \"Page\" = 3\n      query \"page\" = 2\n    }\n  }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestHeaderOverrideOnABlocklessRequestMakesABlock(t *testing.T) {
	c := "collection \"c\" {\n  request r() {\n    get \"x\"\n  }\n}\n"
	tree, _ := parserParse("import \"c.art\"\n\nscenario \"s\" {\n  use c.r { header \"X\" = \"1\" }\n}\n")
	res, bag := Expand(tree, MapLoader{"c.art": c})
	if bag.Len() != 0 {
		t.Fatalf("%v", bag.All())
	}
	step := res.File.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	b := step.Action.(*ast.Request).Block
	if b == nil || len(b.Fields) != 1 {
		t.Fatalf("block %+v", b)
	}
	// The braces sit at the end of the request, in the collection, so the
	// request's span stays the request's; the header line keeps the span it
	// was written with, at the use.
	if b.LBrace.Span.File != "c.art" || b.LBrace.Span.Line != 3 || b.LBrace.Span.Via != 1 || b.RBrace.Span != b.LBrace.Span {
		t.Fatalf("braces %+v %+v", b.LBrace.Span, b.RBrace.Span)
	}
	if f := b.Fields[0].Span(); f.File != "main.art" || f.Line != 4 {
		t.Fatalf("header line span %+v", f)
	}
	if r := step.Action.Span(); r.File != "c.art" || r.Line != 3 || r.EndLine != 3 {
		t.Fatalf("request span %+v", r)
	}
	if v := b.Fields[0].(*ast.Field).Value.Span(); v.Via != 0 || v.File != "main.art" {
		t.Fatalf("an override value is written at the use and is not stamped: %+v", v)
	}
}

func TestBodyOverrideReplacesTheWholeBody(t *testing.T) {
	got, diags := run(t, useLogin("    body = {\"k\": 1}\n"))
	noDiags(t, diags)
	if !contains(got, `body = {"k": 1}`) || contains(got, "username") {
		t.Fatalf("got\n%s", got)
	}
}

func TestBodyFieldThroughANonObjectIsAnError(t *testing.T) {
	_, diags := run(t, useLogin("    body.username.first = \"x\"\n"))
	if !contains(diags, "main.art: bad-override") || !contains(diags, "body.username is not an object") {
		t.Fatalf("got %s", diags)
	}
}

func TestBodyFieldOnARequestWithNoBodyIsAnError(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { body.a = 1 }\n}\n",
	})
	if !contains(diags, "main.art: bad-override") || !contains(diags, "not an object literal") {
		t.Fatalf("got %s", diags)
	}
}

func TestBodyFieldOnADefaultedObjectBodyIsStillNotALiteral(t *testing.T) {
	_, diags := run(t, map[string]string{
		"c.art":    "collection \"c\" {\n  request r(p = {}) {\n    post \"x\" { body = p }\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { body.a = 1 }\n}\n",
	})
	if !contains(diags, "not an object literal") {
		t.Fatalf("got %s", diags)
	}
}

// An override line written at the use lives in another file than the
// template; its offset says nothing about where it goes. Appended
// lines print after the template's statements, and a header lands in the
// request block, whichever file is longer.
func TestOverridesPrintAfterTheTemplate(t *testing.T) {
	c := "collection \"c\" {\n  request r() {\n    get \"x\" { header \"A\" = \"1\" }\n    expect status == 200\n    capture id = body.id\n  }\n}\n"
	pad := "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n"
	want := "scenario \"s\" {\n  step \"c.r\" {\n    get \"x\" {\n      header \"A\" = \"1\"\n      header \"B\" = \"2\"\n    }\n    expect status == 200\n    capture id = body.id\n    expect body.id exists\n  }\n}\n"
	for name, files := range map[string]map[string]string{
		"use file first": {"c.art": pad + c, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r {\n    header \"B\" = \"2\"\n    expect body.id exists\n  }\n}\n"},
		"use file later": {"c.art": c, "main.art": "import \"c.art\"\n" + pad + "scenario \"s\" {\n  use c.r {\n    header \"B\" = \"2\"\n    expect body.id exists\n  }\n}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			got, diags := run(t, files)
			noDiags(t, diags)
			if got != want {
				t.Fatalf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestDropCapturesRemovesTheTemplatesCaptures(t *testing.T) {
	// The negative test drop captures exists for: a 401 has no token to
	// capture, so the capture has to go as well as the expect. Its order
	// against the use's own expect does not matter.
	for _, lines := range []string{
		"    drop expects\n    drop captures\n    expect status == 401\n",
		"    drop expects\n    expect status == 401\n    drop captures\n",
	} {
		got, diags := run(t, useLogin(lines))
		noDiags(t, diags)
		if contains(got, "capture") || contains(got, "status == 200") || !contains(got, "status == 401") {
			t.Fatalf("got\n%s", got)
		}
	}
}

func TestDropCapturesKeepsTheExpects(t *testing.T) {
	got, diags := run(t, useLogin("    drop captures\n"))
	noDiags(t, diags)
	if contains(got, "capture") || !contains(got, "status == 200") {
		t.Fatalf("got\n%s", got)
	}
}

// An override on a terminal or browser step is refused, and the hint says
// what to do instead: pass the value as a parameter.
func TestOverrideOnNonAPIStepHintsAParameter(t *testing.T) {
	for name, tc := range map[string]struct{ item, kind string }{
		"terminal": {"run \"true\"\n    expect exit_code == 0", "terminal step"},
		"browser":  {"browser {\n      goto \"/x\"\n    }\n    expect page.url exists", "browser step"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				"c.art":    "collection \"c\" {\n  request r() {\n    " + tc.item + "\n  }\n}\n",
				"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r { header \"X\" = \"1\" }\n}\n",
			}
			tree, pb := parserParse(files["main.art"])
			if pb.HasErrors() {
				t.Fatalf("main.art does not parse: %v", pb.All())
			}
			_, bag := Expand(tree, MapLoader(files))
			for _, d := range bag.All() {
				if d.Code != diag.BadOverride {
					continue
				}
				if !contains(d.Message, tc.kind) {
					t.Errorf("message %q does not name %q", d.Message, tc.kind)
				}
				if !contains(d.Hint, "parameter") {
					t.Errorf("hint %q does not point at a parameter", d.Hint)
				}
				return
			}
			t.Fatalf("no bad-override diagnostic in %v", bag.All())
		})
	}
}

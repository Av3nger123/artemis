package expand

import (
	"regexp"
	"testing"

	"artemis/pkg/dsl/ast"
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
	flow := "collection \"c\" {\n  request r() {\n    run \"true\"\n    expect exit_code == 0\n  }\n  flow f() {\n    use r\n    step \"two\" {\n      get \"x\"\n      expect status == 200\n    }\n  }\n}\n"
	for name, tc := range map[string]struct{ use, want string }{
		"header on terminal": {`use c.r { header "X" = "1" }`, "bad-override"},
		"header on flow":     {`use c.f { header "X" = "1" }`, "bad-override"},
		"in on request":      {`use c.r { in "x" { expect exit_code == 1 } }`, "bad-override"},
		"unknown flow step":  {`use c.f { in "tow" { expect status == 201 } }`, "unknown-flow-step"},
		// Beyond the brief.
		"api step via in":      {`use c.f { in "two" { header "X" = "1" } }`, ""},
		"expect on flow":       {`use c.f { expect status == 201 }`, "bad-override"},
		"argument inside in":   {`use c.f { in "two" { x = 1 } }`, "bad-override"},
		"in inside in":         {`use c.f { in "two" { in "two" { expect status == 1 } } }`, "bad-override"},
		"drop after expect in": {`use c.f { in "two" { expect status == 1, drop expects } }`, "drop-after-expect"},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := run(t, map[string]string{"c.art": flow, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  " + tc.use + "\n}\n"})
			if tc.want == "" {
				noDiags(t, diags)
				return
			}
			if !contains(diags, "main.art: "+tc.want) {
				t.Fatalf("got %s", diags)
			}
		})
	}
}

// The brief cut the output at `"c.f / two"`, the flow-prefixed step name that
// Task 8 introduces; until then the step is called "two". The pattern accepts
// both so the test outlives the rename.
func TestInTargetsOneFlowStep(t *testing.T) {
	flow := "collection \"c\" {\n  flow f() {\n    step \"one\" {\n      get \"a\"\n      expect status == 200\n    }\n    step \"two\" {\n      get \"b\"\n      expect status == 200\n    }\n  }\n}\n"
	got, diags := run(t, map[string]string{"c.art": flow, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f { in \"two\" { header \"X\" = \"1\" } }\n}\n"})
	noDiags(t, diags)
	at := regexp.MustCompile(`step "(c\.f / )?two"`).FindStringIndex(got)
	if at == nil {
		t.Fatalf("no step two in\n%s", got)
	}
	one := got[:at[0]]
	if contains(one, `"X"`) || !contains(got[at[0]:], `header "X" = "1"`) {
		t.Fatalf("override landed on the wrong step:\n%s", got)
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
	for _, sp := range []struct {
		name string
		at   ast.Node
	}{{"block", b}} {
		s := sp.at.Span()
		if s.File != "main.art" || s.Line != 4 || s.Via != 1 {
			t.Fatalf("%s span %+v", sp.name, s)
		}
	}
	if b.LBrace.Span.Via != 1 || b.RBrace.Span.Via != 1 || b.LBrace.Span.Line != 4 {
		t.Fatalf("braces %+v %+v", b.LBrace.Span, b.RBrace.Span)
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

func TestInDropsAndAddsExpectsOnOneStep(t *testing.T) {
	flow := "collection \"c\" {\n  flow f() {\n    step \"one\" {\n      get \"a\"\n      expect status == 200\n    }\n    step \"two\" {\n      get \"b\"\n      expect status == 200\n    }\n  }\n}\n"
	got, diags := run(t, map[string]string{"c.art": flow, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f { in \"two\" { drop expects, expect status == 404 } }\n}\n"})
	noDiags(t, diags)
	want := "scenario \"s\" {\n  step \"c.f / one\" {\n    get \"a\"\n    expect status == 200\n  }\n\n  step \"c.f / two\" {\n    get \"b\"\n    expect status == 404\n  }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// An override line written at the use lives in another file, or another copy,
// than the template; its offset says nothing about where it goes. Appended
// lines print after the template's statements, and a header lands in the
// request block, whichever file is longer.
func TestOverridesPrintAfterTheTemplate(t *testing.T) {
	c := "collection \"c\" {\n  request r() {\n    get \"x\" { header \"A\" = \"1\" }\n    expect status == 200\n    capture id = body.id\n  }\n}\n"
	pad := "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n"
	want := "scenario \"s\" {\n  step \"c.r\" {\n    get \"x\" {\n      header \"A\" = \"1\"\n      header \"B\" = \"2\"\n    }\n    expect status == 200\n    capture id = body.id\n    expect body.id exists\n  }\n}\n"
	for name, files := range map[string]map[string]string{
		"use file first": {"c.art": pad + c, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r {\n    header \"B\" = \"2\"\n    expect body.id exists\n  }\n}\n"},
		"use file later": {"c.art": c, "main.art": "import \"c.art\"\n" + pad + "scenario \"s\" {\n  use c.r {\n    header \"B\" = \"2\"\n    expect body.id exists\n  }\n}\n"},
		"same file":      {"main.art": "scenario \"s\" {\n  use c.r {\n    header \"B\" = \"2\"\n    expect body.id exists\n  }\n}\n\n" + c},
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

// A nested use in a flow can override too; its lines were copied out of the
// collection with the flow.
func TestNestedUseInAFlowOverrides(t *testing.T) {
	c := "collection \"c\" {\n  request r(code) {\n    get \"x\"\n    expect status == code\n  }\n  flow f(n) {\n    use r { code = n, drop expects, expect status != n }\n  }\n}\n"
	got, diags := run(t, map[string]string{"c.art": c, "main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.f { n = 200 }\n}\n"})
	noDiags(t, diags)
	if contains(got, "status == 200\n") || !contains(got, "expect status != 200") {
		t.Fatalf("got\n%s", got)
	}
}

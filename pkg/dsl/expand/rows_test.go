package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

func expandSrc(t *testing.T, src string, files MapLoader) (*Result, *diag.Bag) {
	t.Helper()
	tree, bag := parser.Parse("suite/s.art", src)
	if bag.HasErrors() {
		t.Fatalf("parse: %v", bag.All())
	}
	return Expand(tree, files)
}

// A for clause is one scenario per row: each named for its row, each opening
// with `var <name> = <row>`, each knowing which row it is.
func TestRowsAreScenarios(t *testing.T) {
	files := MapLoader{"suite/users.json": `[{"role": "admin", "id": 1}, {"role": "viewer", "id": 2}]`}
	res, bag := expandSrc(t, `scenario "login as ${user.role}" for user in json("users.json") {
  step "s" { run "true" }
}
`, files)
	if bag.HasErrors() {
		t.Fatalf("expand: %v", bag.All())
	}
	if len(res.File.Scenarios) != 2 {
		t.Fatalf("%d scenarios, want 2", len(res.File.Scenarios))
	}
	for i, want := range []string{"login as admin", "login as viewer"} {
		sc := res.File.Scenarios[i].(*ast.Scenario)
		if sc.Name.Value != want {
			t.Errorf("scenario %d is named %q, want %q", i, sc.Name.Value, want)
		}
		if sc.For != nil || sc.Row == nil || sc.Row.Index != i || sc.Row.Source != "users.json" {
			t.Errorf("scenario %d: For = %v, Row = %+v", i, sc.For, sc.Row)
		}
		v, ok := sc.Body[0].(*ast.VarDecl)
		if !ok || v.Name.Value != "user" {
			t.Fatalf("scenario %d does not open with its row: %T", i, sc.Body[0])
		}
	}
	got := print.Canonical(res.File)
	if !strings.Contains(got, `var user = {"id": 1, "role": "admin"}`) {
		t.Errorf("the first row prints as:\n%s", got)
	}
}

// A name with no interpolation is numbered; a literal array is rows too; and
// a row's values print back as the literal that makes them -- a quote, a
// dollar and a newline included.
func TestPlainNamesAreNumbered(t *testing.T) {
	res, bag := expandSrc(t, `scenario "edge" for s in ["a\"b", "\${x}", "line\nbreak", -2] {
  step "s" { run "true" }
}
`, nil)
	if bag.HasErrors() {
		t.Fatalf("expand: %v", bag.All())
	}
	got := print.Canonical(res.File)
	for _, want := range []string{`scenario "edge [1]"`, `var s = "a\"b"`, `var s = "\${x}"`, `var s = "line\nbreak"`, `var s = -2`, `scenario "edge [4]"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	// What printed is a file the parser reads back with no error.
	if _, bag := parser.Parse("x.art", got); bag.HasErrors() {
		t.Errorf("the expansion does not parse: %v\n%s", bag.All(), got)
	}
}

// `for secret` makes each row's var a secret one.
func TestSecretRows(t *testing.T) {
	res, _ := expandSrc(t, `scenario "s" for secret cred in [{"pw": "x"}] {
  step "s" { run "true" }
}
`, nil)
	v := res.File.Scenarios[0].(*ast.Scenario).Body[0].(*ast.VarDecl)
	if v.Secret.Text != "secret" {
		t.Error("a secret row's var is not secret")
	}
}

// Every way a for clause cannot make rows is a compile error with its own code.
func TestRowFaults(t *testing.T) {
	files := MapLoader{"suite/obj.json": `{"a": 1}`, "suite/none.json": `[]`, "suite/bad.json": `[`}
	cases := []struct {
		src  string
		code diag.Code
	}{
		{`scenario "s" for x in env("ROWS") { step "s" { run "true" } }`, diag.ForSourceNotStatic},
		{`scenario "s" for x in json("obj.json") { step "s" { run "true" } }`, diag.ForSourceNotArray},
		{`scenario "s" for x in json("none.json") { step "s" { run "true" } }`, diag.ForNoRows},
		{`scenario "s" for x in json("bad.json") { step "s" { run "true" } }`, diag.DataNotJSON},
		{`scenario "s" for x in json("gone.json") { step "s" { run "true" } }`, diag.DataFileMissing},
		{`scenario "${x.a}" for x in [{"a": 1}, {"a": 1}] { step "s" { run "true" } }`, diag.DuplicateRowName},
		{`scenario "${y}" for x in [1] { step "s" { run "true" } }`, diag.BadRowName},
		{`scenario "${env("X")}" for x in [1] { step "s" { run "true" } }`, diag.BadRowName},
	}
	for _, c := range cases {
		res, bag := expandSrc(t, c.src+"\n", files)
		found := false
		for _, d := range bag.All() {
			found = found || d.Code == c.code
		}
		if !found {
			t.Errorf("%s: want %s, got %v", c.src, c.code, bag.All())
		}
		if len(res.File.Scenarios) != 0 {
			t.Errorf("%s: a scenario whose rows could not be made was kept", c.src)
		}
	}
}

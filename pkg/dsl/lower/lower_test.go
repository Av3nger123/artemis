package lower

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
)

// lowerSrc is the whole front end in one call: parse, check, lower. Every test
// in this package starts from real .art source rather than from a tree built in
// Go, because a tree built in Go cannot catch a lowerer that reads the wrong
// field of a node the parser fills differently.
func lowerSrc(t *testing.T, src string) []*Scenario {
	t.Helper()

	tree, bag := parser.Parse("t.art", src)
	if bag.HasErrors() {
		t.Fatalf("source does not parse, so this test proves nothing:\n%s", render(src, bag))
	}
	info, checked := check.Check(tree)
	if checked.HasErrors() {
		t.Fatalf("source does not check, so this test proves nothing:\n%s", render(src, checked))
	}
	scenarios, err := File(tree, info)
	if err != nil {
		t.Fatalf("File() = %v, want nil", err)
	}
	return scenarios
}

// one is the single scenario a test's source declares.
func one(t *testing.T, src string) *Scenario {
	t.Helper()
	got := lowerSrc(t, src)
	if len(got) != 1 {
		t.Fatalf("lowered %d scenarios, want 1", len(got))
	}
	return got[0]
}

// step is the nth step of the single scenario a test's source declares.
func stepN(t *testing.T, src string, n int) *Step {
	t.Helper()
	sc := one(t, src)
	if len(sc.Steps) <= n {
		t.Fatalf("lowered %d steps, want more than %d", len(sc.Steps), n)
	}
	return sc.Steps[n]
}

// envOf is the run-time environment a step's action is rendered against: the
// scenario's scope and nothing else. A step's own roots are not on it, because a
// URL cannot read the status of the request it is part of.
func envOf(scope executor.Scope) *eval.Env {
	return &eval.Env{Vars: scope.Vars(), Getenv: func(string) string { return "" }}
}

// The design document's worked example is the file this package has to lower, so
// it is lowered whole: four steps, one of each shape the language has, in the
// order they were written.
func TestTheWorkedExampleLowers(t *testing.T) {
	sc := one(t, readFixture(t, "checkout.art"))

	if sc.Name != "checkout" {
		t.Errorf("scenario name = %q, want %q", sc.Name, "checkout")
	}
	if sc.Line != 1 {
		t.Errorf("scenario line = %d, want 1", sc.Line)
	}
	if got, want := len(sc.Vars), 3; got != want {
		t.Fatalf("lowered %d vars, want %d", got, want)
	}
	for i, want := range []string{"url", "app", "pw"} {
		if sc.Vars[i].Name != want {
			t.Errorf("var %d = %q, want %q -- vars are lowered in source order", i, sc.Vars[i].Name, want)
		}
	}
	if sc.Browser == nil {
		t.Fatal("scenario has no browser config, want the one it wrote")
	}

	wantSteps := []struct {
		name string
		typ  string
	}{
		{"seed the database", "exec"},
		{"login", "api"},
		{"orders", "api"},
		{"upgrade in the app", "browser"},
	}
	if len(sc.Steps) != len(wantSteps) {
		t.Fatalf("lowered %d steps, want %d", len(sc.Steps), len(wantSteps))
	}
	for i, want := range wantSteps {
		got := sc.Steps[i]
		if got.Name != want.name {
			t.Errorf("step %d name = %q, want %q", i, got.Name, want.name)
		}
		if got.Type != want.typ {
			t.Errorf("step %q type = %q, want %q", got.Name, got.Type, want.typ)
		}
		if got.Line == 0 {
			t.Errorf("step %q has no line; a step that cannot run points at it", got.Name)
		}
	}
}

// A step's type is the key its executor is registered under, which is the whole
// of what the registry dispatches on.
func TestTheTypeIsTheRegistryKey(t *testing.T) {
	cases := []struct {
		name string
		typ  check.StepType
		want string
		ok   bool
	}{
		{"api", check.API, "api", true},
		{"terminal registers as exec", check.Terminal, "exec", true},
		{"browser", check.Browser, "browser", true},
		{"a step with no action has no key", check.Unknown, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TypeKey(c.typ)
			if got != c.want || ok != c.ok {
				t.Errorf("TypeKey(%v) = %q, %v; want %q, %v", c.typ, got, ok, c.want, c.ok)
			}
		})
	}
}

// Vars are bound in source order against the vars already bound, which is the
// checker's ordering rule at run time.
func TestBindEvaluatesVarsInOrder(t *testing.T) {
	sc := one(t, `scenario "s" {
  var a = 1
  var b = a
  var c = "n=${b}"
  step "x" { get "http://example.test" }
}`)

	scope := executor.NewScope()
	if err := sc.Bind(scope); err != nil {
		t.Fatalf("Bind() = %v, want nil", err)
	}
	if got, _ := scope.Get("a"); got != 1.0 {
		t.Errorf("scope[a] = %#v, want 1 as a number", got)
	}
	if got, _ := scope.Get("b"); got != 1.0 {
		t.Errorf("scope[b] = %#v, want the value of a", got)
	}
	if got, _ := scope.Get("c"); got != "n=1" {
		t.Errorf("scope[c] = %#v, want \"n=1\" -- a number renders as written", got)
	}
}

// `config browser` is carried and resolved, with the defaults a scenario that
// says nothing gets.
func TestBrowserConfigResolves(t *testing.T) {
	sc := one(t, `scenario "s" {
  config browser { headless = false, viewport = "1280x720" }
  step "x" { browser { goto "/" } }
}`)

	got, err := sc.Browser.Resolve(envOf(executor.NewScope()))
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if got.Headless {
		t.Error("Headless = true, want the false the scenario wrote")
	}
	if got.Viewport != "1280x720" {
		t.Errorf("Viewport = %q, want 1280x720", got.Viewport)
	}

	// A scenario with no config at all: headless, no viewport.
	none := (*BrowserConfig)(nil)
	def, err := none.Resolve(envOf(executor.NewScope()))
	if err != nil {
		t.Fatalf("Resolve() on no config = %v, want nil", err)
	}
	if !def.Headless || def.Viewport != "" {
		t.Errorf("default config = %#v, want headless with no viewport", def)
	}
}

// Nothing here panics on a tree the parser recovered from. Each of these files
// has diagnostics; lowering one is what a `--json` client or a UI does anyway,
// and a panic would take the whole run down over one bad line.
func TestNothingPanicsOnABrokenTree(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"a step with no action", `scenario "s" { step "x" { expect status == 200 } }`},
		{"a statement that did not parse", `scenario "s" { step "x" { get "u" ???? } }`},
		{"a declaration that did not parse", `scenario "s" { ???? }`},
		{"a field with no value", `scenario "s" { step "x" { get "u" { body = } } }`},
		{"an unterminated scenario", `scenario "s" { step "x" { get "u"`},
		{"a bad browser action", `scenario "s" { step "x" { browser { nope "a" } } }`},
		{"nothing at all", ``},
		{"a var with no value", `scenario "s" { var a = }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree, _ := parser.Parse("t.art", c.src)
			info, _ := check.Check(tree)
			// An error is a fine answer -- a step with no action cannot be run.
			// A panic is not.
			if _, err := File(tree, info); err != nil {
				t.Logf("File() = %v, which is an answer and not a crash", err)
			}
		})
	}
}

// A nil tree and a nil Info are what a caller that built its own tree hands
// over. Both answer rather than panicking, and a nil Info gives the conservative
// class.
func TestNilInputs(t *testing.T) {
	if got, err := File(nil, nil); got != nil || err != nil {
		t.Errorf("File(nil, nil) = %#v, %v; want nil, nil", got, err)
	}

	src := "scenario \"s\" {\n  step \"x\" {\n    get \"u\"\n    expect status == 200\n  }\n}\n"
	tree, bag := parser.Parse("t.art", src)
	if bag.HasErrors() {
		t.Fatalf("source does not parse:\n%s", render(src, bag))
	}
	got, err := File(tree, nil)
	if err != nil {
		t.Fatalf("File(tree, nil) = %v, want nil", err)
	}
	if len(got) != 1 || len(got[0].Steps) != 1 {
		t.Fatalf("File(tree, nil) = %#v, want one scenario with one step", got)
	}
	if cls := got[0].Steps[0].Expects[0].Class; cls != check.Complex {
		t.Errorf("class with no Info = %v, want the conservative %v", cls, check.Complex)
	}
}

// A step whose action did not parse is an error naming the step, not a step with
// no type that would be dispatched on "" at run time.
func TestAStepWithNoActionIsAnError(t *testing.T) {
	tree, _ := parser.Parse("t.art", `scenario "s" { step "nameless thing" { expect status == 200 } }`)
	info, _ := check.Check(tree)

	_, err := File(tree, info)
	if err == nil {
		t.Fatal("File() = nil, want an error for a step with no action")
	}
	if want := "nameless thing"; !strings.Contains(err.Error(), want) {
		t.Errorf("File() = %q, want it to name the step %q", err, want)
	}
}

// A second `config browser` replaces the first: the checker has no rule against
// writing two, and the last one written is the one a reader expects to apply.
func TestTheLastConfigWins(t *testing.T) {
	sc := one(t, `scenario "s" {
  config browser { headless = true }
  config browser { headless = false }
  step "x" { browser { goto "/" } }
}`)
	got, err := sc.Browser.Resolve(envOf(executor.NewScope()))
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if got.Headless {
		t.Error("Headless = true, want the second config's false")
	}
}

// A statement written above the action block is lowered where it was written,
// not where the struct holds it.
//
// Action-not-first is a diagnostic, so this file does not parse clean and is
// lowered from the recovered tree on purpose: what is being pinned is that the
// lowerer walks ast.Children -- the items in source order -- rather than the
// action and then the body, which would put the second expect first.
func TestStatementsKeepSourceOrder(t *testing.T) {
	src := `scenario "s" {
  step "x" {
    expect status == 200
    get "http://example.test"
    expect status == 201
  }
}
`
	tree, _ := parser.Parse("t.art", src)
	info, _ := check.Check(tree)
	got, err := File(tree, info)
	if err != nil {
		t.Fatalf("File() = %v, want nil", err)
	}
	st := got[0].Steps[0]

	if len(st.Expects) != 2 {
		t.Fatalf("lowered %d expects, want 2", len(st.Expects))
	}
	if st.Expects[0].Line != 3 || st.Expects[1].Line != 5 {
		t.Errorf("expect lines = %d, %d; want 3, 5 -- reading order", st.Expects[0].Line, st.Expects[1].Line)
	}
}

// ast.Source of a lowered expression is still the file's bytes: the lowerer
// holds the nodes rather than copying their text, which is what lets ART-43's
// encoder and ART-34's printer read the same tree.
func TestLoweringHoldsTheTreeItself(t *testing.T) {
	src := `scenario "s" {
  step "x" {
    get "http://example.test"
    expect status == 200
  }
}
`
	st := stepN(t, src, 0)
	if got, want := strings.TrimSpace(ast.Source(st.Expects[0].Value)), "status == 200"; got != want {
		t.Errorf("expect expression source = %q, want %q", got, want)
	}
	if st.Request.URL == nil {
		t.Fatal("the request has no URL expression")
	}
	if kind := st.Request.URL.(*ast.Literal).Kind(); kind != token.String {
		t.Errorf("URL literal kind = %v, want a string", kind)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "parser", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// render is what the checker or the parser actually said, for a failure message
// that shows the diagnostic rather than the count.
func render(src string, bag *diag.Bag) string {
	files := diag.NewFiles()
	files.Add("t.art", src)
	return diag.TerminalString(files, bag.All())
}

// A var that cannot be evaluated stops the scenario: a scenario whose variables
// do not resolve has nothing worth running, and the message names the var.
func TestBindStopsAtAVarThatWillNotEvaluate(t *testing.T) {
	sc := one(t, `scenario "s" {
  var a = "x"
  var b = -a
  var c = 1
  step "x" { get "http://example.test" }
}`)

	scope := executor.NewScope()
	err := sc.Bind(scope)
	if err == nil {
		t.Fatal("Bind() = nil, want an error for a var that will not evaluate")
	}
	if !strings.Contains(err.Error(), "var b") {
		t.Errorf("Bind() = %q, want it to name the var", err)
	}
	if _, ok := scope.Get("c"); ok {
		t.Error("Bind() carried on past the var it could not evaluate")
	}
	if got, _ := scope.Get("a"); got != "x" {
		t.Errorf("scope[a] = %#v, want the vars above the failure to be bound", got)
	}
}

// A config setting that came out of an expression and is the wrong kind of value
// is an error naming the setting. The checker catches `headless = 3`; this is the
// form only the run could have known.
func TestABadBrowserConfigFromAnExpressionIsAnError(t *testing.T) {
	sc := one(t, `scenario "s" {
  var h = "yes"
  config browser { headless = h }
  step "x" { browser { goto "/" } }
}`)

	scope := executor.NewScope()
	if err := sc.Bind(scope); err != nil {
		t.Fatalf("Bind() = %v, want nil", err)
	}
	if _, err := sc.Browser.Resolve(envOf(scope)); err == nil {
		t.Fatal("Resolve() = nil, want an error for a headless that is not a boolean")
	}

	// A viewport is rendered rather than type-checked: any value has a text
	// form, and what a viewport string means belongs to whatever opens the
	// window.
	vp := one(t, `scenario "s" {
  config browser { viewport = "1280x720" }
  step "x" { browser { goto "/" } }
}`)
	got, err := vp.Browser.Resolve(envOf(executor.NewScope()))
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if got.Viewport != "1280x720" {
		t.Errorf("Viewport = %q, want 1280x720", got.Viewport)
	}
}

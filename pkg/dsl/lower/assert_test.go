package lower

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/dsl/check"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/result"
)

// observed is the environment a step's assertions are evaluated against: what
// the step saw, by the root names its type binds, plus the scenario's scope.
// Where the observation comes from at run time is ART-38's; what is pinned here
// is that a lowered step can assert against one.
func observed(roots map[string]any, scope executor.Scope) *eval.Env {
	return &eval.Env{
		Roots:  roots,
		Vars:   scope.Vars(),
		Getenv: func(string) string { return "" },
	}
}

// apiRoots is an api step's observation: the four roots the type binds.
func apiRoots(status float64, body map[string]any) map[string]any {
	return map[string]any{
		"status":  status,
		"body":    body,
		"raw":     `{"data":{"count":2}}`,
		"headers": eval.Headers{"Content-Type": "application/json"},
	}
}

// assertOf lowers one scenario, binds it, and returns the assertions its first
// step makes against roots.
func assertOf(t *testing.T, src string, roots map[string]any) []result.AssertionResult {
	t.Helper()
	sc, scope := bound(t, src)
	return sc.Steps[0].Assert(observed(roots, scope))
}

// One `expect` is one assertion, and the rule holds for an `and` chain: source
// line to reported assertion stays one to one, so two assertions means two
// expect lines.
func TestOneExpectIsOneAssertion(t *testing.T) {
	got := assertOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    expect status == 200 and body.data.count > 0 and body.data.count < 10
    expect status < 400
  }
}`, apiRoots(200, map[string]any{"data": map[string]any{"count": 2.0}}))

	if len(got) != 2 {
		t.Fatalf("got %d assertions, want 2 -- one per expect line, whatever the expression does", len(got))
	}
	for i, a := range got {
		if a.Status != result.StatusPass {
			t.Errorf("assertion %d = %v (%s), want a pass", i, a.Status, a.Describe())
		}
		if a.Kind != KindExpect {
			t.Errorf("assertion %d kind = %q, want %q", i, a.Kind, KindExpect)
		}
		if a.Step != "orders" {
			t.Errorf("assertion %d step = %q, want orders", i, a.Step)
		}
	}
	if got[0].Line != 4 || got[1].Line != 5 {
		t.Errorf("lines = %d, %d; want 4, 5 -- the lines the expects were written on", got[0].Line, got[1].Line)
	}
}

// An `and` chain that fails is attributed to the half that broke, which is why
// one assertion is enough: the reader still gets the operand that decided it.
func TestAnAndChainReportsTheHalfThatBroke(t *testing.T) {
	got := assertOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    expect status == 200 and body.data.count > 5
  }
}`, apiRoots(200, map[string]any{"data": map[string]any{"count": 2.0}}))

	if len(got) != 1 {
		t.Fatalf("got %d assertions, want 1", len(got))
	}
	if got[0].Status != result.StatusFail {
		t.Fatalf("assertion = %v, want a fail", got[0].Status)
	}
	if got[0].Path != "body.data.count" {
		t.Errorf("Path = %q, want the operand that decided it", got[0].Path)
	}
	if got[0].Actual != 2.0 {
		t.Errorf("Actual = %#v, want the value that was there", got[0].Actual)
	}
}

// A check that could not be made at all is errored, not failed: an absent path
// has no honest false, which is the taxonomy pkg/shared/assert established and
// pkg/eval keeps.
func TestAnUnaskableAssertionIsErrored(t *testing.T) {
	got := assertOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    expect body.data.missing > 0
  }
}`, apiRoots(200, map[string]any{"data": map[string]any{"count": 2.0}}))

	if len(got) != 1 {
		t.Fatalf("got %d assertions, want 1", len(got))
	}
	if got[0].Status != result.StatusError {
		t.Errorf("status = %v, want error -- a question that could not be asked", got[0].Status)
	}
	if got[0].Error == "" {
		t.Error("the errored assertion carries no reason")
	}
}

// Each of the design's assertion forms produces one assertion with the operator
// the author wrote, so a failure reads as the line in the file.
func TestEveryAssertionFormLowers(t *testing.T) {
	roots := apiRoots(200, map[string]any{"data": map[string]any{
		"count": 2.0,
		"email": "ada@example.com",
		"roles": []any{"admin", "user"},
		"token": "abc",
	}})

	cases := []struct {
		expect   string
		path     string
		operator string
	}{
		{`expect status == 200`, "status", "=="},
		{`expect body.data.count > 0`, "body.data.count", ">"},
		{`expect body.data.email matches /.+@.+/`, "body.data.email", "matches"},
		{`expect body.data.roles contains "admin"`, "body.data.roles", "contains"},
		{`expect body.data.token exists`, "body.data.token", "exists"},
		{`expect body.data.count is number`, "body.data.count", "is"},
		{`expect not body.data.count == 9`, "body.data.count", "not =="},
		{`expect headers["content-type"] contains "json"`, `headers["content-type"]`, "contains"},
	}
	for _, c := range cases {
		t.Run(c.expect, func(t *testing.T) {
			got := assertOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    `+c.expect+`
  }
}`, roots)
			if len(got) != 1 {
				t.Fatalf("got %d assertions, want 1", len(got))
			}
			if got[0].Status != result.StatusPass {
				t.Errorf("assertion = %v (%s), want a pass", got[0].Status, got[0].Describe())
			}
			if got[0].Path != c.path {
				t.Errorf("Path = %q, want %q", got[0].Path, c.path)
			}
			if got[0].Operator != c.operator {
				t.Errorf("Operator = %q, want %q", got[0].Operator, c.operator)
			}
		})
	}
}

// A terminal step asserts against its own roots, which is the per-type scope at
// run time.
func TestATerminalStepAssertsAgainstItsOwnRoots(t *testing.T) {
	got := assertOf(t, `scenario "s" {
  step "seed" {
    run "psql"
    expect exit_code == 0
    expect stdout contains "COPY 42"
  }
}`, map[string]any{"exit_code": 0.0, "stdout": "COPY 42\n", "stderr": ""})

	if len(got) != 2 {
		t.Fatalf("got %d assertions, want 2", len(got))
	}
	for i, a := range got {
		if a.Status != result.StatusPass {
			t.Errorf("assertion %d = %v (%s), want a pass", i, a.Status, a.Describe())
		}
	}
}

// A step with no expects asserts nothing, rather than asserting a default
// nobody wrote. In a .art file there is no implicit status check: `expect status
// == 200` is a line in the file or it is not there at all.
func TestAStepWithNoExpectsAssertsNothing(t *testing.T) {
	got := assertOf(t, `scenario "s" {
  step "fire and forget" {
    post "https://api.test/events"
  }
}`, apiRoots(500, nil))

	if got != nil {
		t.Errorf("got %#v, want no assertions", got)
	}
}

// A capture keeps the type the evaluator gave it, and writes into the scope the
// later steps read.
func TestACapturePreservesItsType(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "login" {
    get "https://api.test/me"
    capture token = body.data.token
    capture count = body.data.count
    capture flag = body.data.flag
    capture obj = body.data
  }
}`)
	data := map[string]any{
		"token": "abc",
		"count": 2.0,
		"flag":  true,
	}
	errs := sc.Steps[0].Apply(observed(apiRoots(200, map[string]any{"data": data}), scope), scope)
	if errs != nil {
		t.Fatalf("Apply() = %#v, want no errored assertions", errs)
	}

	for _, c := range []struct {
		key  string
		want any
	}{{"token", "abc"}, {"count", 2.0}, {"flag", true}} {
		got, ok := scope.Get(c.key)
		if !ok {
			t.Errorf("scope has no %s", c.key)
			continue
		}
		if got != c.want {
			t.Errorf("scope[%s] = %#v (%T), want %#v (%T) -- the type is preserved",
				c.key, got, got, c.want, c.want)
		}
	}
	// An object stays an object rather than becoming its text, which is what
	// makes `body = obj` safe.
	if got, _ := scope.Get("obj"); got == nil {
		t.Error("scope[obj] is nil, want the object")
	} else if _, ok := got.(map[string]any); !ok {
		t.Errorf("scope[obj] = %T, want a map", got)
	}
}

// A capture that succeeds records no assertion: a capture is plumbing, not a
// check, and a run that printed a line per captured token would bury the checks
// that matter.
func TestASuccessfulCaptureRecordsNothing(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "login" {
    get "https://api.test/me"
    capture token = body.data.token
  }
}`)
	got := sc.Steps[0].Apply(observed(apiRoots(200, map[string]any{
		"data": map[string]any{"token": "abc"},
	}), scope), scope)

	if got != nil {
		t.Errorf("Apply() = %#v, want nothing recorded for a capture that worked", got)
	}
}

// A capture that cannot be read is one errored assertion naming the key, writes
// nothing, and does not stop the captures after it -- two mistyped paths should
// take one run to find.
func TestAFailedCaptureIsOneErroredAssertion(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "login" {
    get "https://api.test/me"
    capture first = body.data.nope
    capture second = body.data.token
    capture third = body.data.alsoNope
  }
}`)
	got := sc.Steps[0].Apply(observed(apiRoots(200, map[string]any{
		"data": map[string]any{"token": "abc"},
	}), scope), scope)

	if len(got) != 2 {
		t.Fatalf("got %d errored assertions, want 2 -- one per failed capture", len(got))
	}
	for i, a := range got {
		if a.Status != result.StatusError {
			t.Errorf("assertion %d = %v, want error", i, a.Status)
		}
		if a.Kind != KindCapture {
			t.Errorf("assertion %d kind = %q, want %q", i, a.Kind, KindCapture)
		}
		if a.Error == "" {
			t.Errorf("assertion %d carries no reason", i)
		}
		if a.Expected != nil || a.Actual != nil {
			t.Errorf("assertion %d compared %#v to %#v; a capture compares nothing", i, a.Expected, a.Actual)
		}
	}
	// The key is the Path, because the name is what a scenario goes and fixes,
	// and they come out in source order.
	if got[0].Path != "first" || got[1].Path != "third" {
		t.Errorf("paths = %q, %q; want first, third in source order", got[0].Path, got[1].Path)
	}
	if got[0].Operator != "body.data.nope" {
		t.Errorf("Operator = %q, want the expression the value was read with", got[0].Operator)
	}
	if got[0].Line != 4 {
		t.Errorf("Line = %d, want 4 -- the line the name sits on", got[0].Line)
	}

	// A failed capture writes nothing, so a later step fails on an unknown name
	// rather than on a value that is quietly wrong.
	if _, ok := scope.Get("first"); ok {
		t.Error("a failed capture wrote into the scope")
	}
	// The capture between the two failures still ran.
	if got, ok := scope.Get("second"); !ok || got != "abc" {
		t.Errorf("scope[second] = %v, %v; want the capture after a failure to have run", got, ok)
	}
}

// `within` is a per-assertion budget, defaulting to BrowserWithin in a browser
// step and to "evaluate once" in an api or terminal one.
func TestWithinDefaultsByStepType(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "browse" {
    browser {
      goto "/billing"
    }
    expect page.url contains "/billing/confirmed" within "10s"
    expect page.title contains "Billing"
  }
  step "orders" {
    get "https://api.test/o"
    expect status == 200
  }
}`)
	env := observed(nil, scope)

	written, err := sc.Steps[0].Expects[0].Within(env)
	if err != nil {
		t.Fatalf("Within() = %v, want nil", err)
	}
	if written != 10*time.Second {
		t.Errorf("Within() = %v, want the 10s the scenario wrote", written)
	}

	def, err := sc.Steps[0].Expects[1].Within(env)
	if err != nil {
		t.Fatalf("Within() = %v, want nil", err)
	}
	if def != BrowserWithin {
		t.Errorf("Within() = %v, want the browser default %v", def, BrowserWithin)
	}

	api, err := sc.Steps[1].Expects[0].Within(env)
	if err != nil {
		t.Fatalf("Within() = %v, want nil", err)
	}
	if api != 0 {
		t.Errorf("Within() = %v, want zero: an api step's observation is complete when it returns", api)
	}
}

// An interpolated budget is evaluated at run time, and one that is not a
// duration is an error naming the clause.
func TestAnInterpolatedWithinIsEvaluated(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  var budget = "250ms"
  var bad = "soon"
  step "browse" {
    browser {
      goto "/"
    }
    expect page.title contains "x" within budget
    expect page.title contains "y" within bad
  }
}`)
	env := observed(nil, scope)

	got, err := sc.Steps[0].Expects[0].Within(env)
	if err != nil {
		t.Fatalf("Within() = %v, want nil", err)
	}
	if got != 250*time.Millisecond {
		t.Errorf("Within() = %v, want 250ms", got)
	}

	_, err = sc.Steps[0].Expects[1].Within(env)
	if err == nil {
		t.Fatal("Within() = nil, want an error for a budget that is not a duration")
	}
	if !strings.Contains(err.Error(), "within") {
		t.Errorf("Within() = %q, want it to name the clause", err)
	}
}

// The checker's simple/complex label is carried, not re-derived: a second client
// deciding it again is a second client that can disagree.
func TestTheExpectClassIsCarried(t *testing.T) {
	sc, _ := bound(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    expect status == 200
    expect status == 200 and status < 400
  }
}`)

	if got := sc.Steps[0].Expects[0].Class; got != check.Simple {
		t.Errorf("class of a comparison = %v, want %v", got, check.Simple)
	}
	if got := sc.Steps[0].Expects[1].Class; got != check.Complex {
		t.Errorf("class of an and chain = %v, want %v", got, check.Complex)
	}
}

// readLocalFixture reads a .art file from this package's own testdata.
//
// The package's other fixtures live in pkg/dsl/parser/testdata, because they
// are the parser's and this package borrows them. regex_capture.art is this
// issue's own: it is the DSL translation of pkg/cli/testdata/regex_capture.yaml,
// and it exists so that ART-38 has a file to run.
func readLocalFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// textRoots is an api step that answered with a body that is not JSON: `raw`
// is the text that arrived and `body` is null, which is the only shape a
// regex capture is for.
func textRoots(raw string) map[string]any {
	return map[string]any{
		"status":  float64(200),
		"body":    nil,
		"raw":     raw,
		"headers": eval.Headers{"Content-Type": "text/plain"},
	}
}

// TestRegexCaptureFeedsTheNextStep is regex_capture.yaml's behaviour, in the
// DSL, end to end: a value read off a body with no JSON in it, by group 1 of
// a regex, and templated into the next step's URL.
//
// It is the behaviour ART-38 has to reproduce byte for byte against
// regex_capture.golden, and it is asserted here rather than there because
// nothing in this package runs an HTTP request: the observation is handed in,
// as every test in this package hands one in.
func TestRegexCaptureFeedsTheNextStep(t *testing.T) {
	t.Setenv("SERVER", "https://api.test")
	sc, scope := bound(t, readLocalFixture(t, "regex_capture.art"))

	got := sc.Steps[0].Apply(observed(textRoots("moved to /items/42"), scope), scope)
	if len(got) != 0 {
		t.Fatalf("a capture that succeeded recorded %d assertion(s), want none: %+v", len(got), got)
	}

	// A string, not the number 42: match() reads the text that matched rather
	// than guessing at what it meant.
	if v, ok := scope.Vars()["itemId"]; !ok || v != "42" {
		t.Fatalf("itemId = %#v, want the string \"42\"", v)
	}

	model, err := sc.Steps[1].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v, want nil", err)
	}
	if want := "https://api.test/items/42"; model.Request.URL != want {
		t.Errorf("the next step's url = %q, want %q -- which is what proves the "+
			"capture landed in the scenario's scope", model.Request.URL, want)
	}
}

// A pattern that matches nothing is one errored assertion naming the capture,
// and nothing is written. That is ART-18's adopted rule -- one errored
// assertion per failed capture -- reached through the path every capture
// already takes, so match() needed no change in the lowerer.
func TestARegexThatMatchesNothingIsOneErroredAssertion(t *testing.T) {
	t.Setenv("SERVER", "https://api.test")
	sc, scope := bound(t, readLocalFixture(t, "regex_capture.art"))

	got := sc.Steps[0].Apply(observed(textRoots("nothing moved"), scope), scope)
	if len(got) != 1 {
		t.Fatalf("got %d assertions, want exactly 1: %+v", len(got), got)
	}
	a := got[0]
	if a.Status != result.StatusError {
		t.Errorf("status = %s, want errored -- a capture that could not be read "+
			"asked no question, so it is not a failure", a.Status)
	}
	if a.Kind != KindCapture || a.Path != "itemId" {
		t.Errorf("kind/path = %q/%q, want %q/%q", a.Kind, a.Path, KindCapture, "itemId")
	}
	if want := `match(raw, /\/items\/([0-9]+)/)`; a.Operator != want {
		t.Errorf("operator = %q, want %q -- the expression the value was read with", a.Operator, want)
	}
	if !strings.Contains(a.Error, "matched nothing") {
		t.Errorf("message = %q, want it to say the pattern matched nothing", a.Error)
	}

	// Nothing written, so a later step naming it fails for the honest reason
	// that the value was never captured.
	if v, ok := scope.Vars()["itemId"]; ok {
		t.Errorf("itemId = %#v, want it unset after a failed capture", v)
	}
}

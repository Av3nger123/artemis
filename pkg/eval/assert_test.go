package eval

import (
	"testing"

	"artemis/pkg/dsl/parser"
	"artemis/pkg/result"
)

// assertSrc parses src as an expression and asserts it.
func assertSrc(t *testing.T, src string, env *Env) Outcome {
	t.Helper()
	x, bag := parser.ParseExpr("t.art", src)
	if bag.HasErrors() {
		t.Fatalf("ParseExpr(%q) reported %d diagnostics: %v", src, bag.Len(), bag.All())
	}
	return Assert(x, env)
}

// describe is the line a reader of a failed run gets: the Outcome recorded into
// the result tree and rendered by the one Describe that exists.
func describe(t *testing.T, src string, env *Env) string {
	t.Helper()
	return assertSrc(t, src, env).Record("orders", "expect", 12).Describe()
}

// The sentence the issue is about. A boolean verdict cannot produce it, which
// is why Assert returns the operands it compared.
func TestTheFailingCountReportsItsOperands(t *testing.T) {
	env := typesEnv()

	o := assertSrc(t, "body.data.count > 0", env)
	if o.Passed {
		t.Error("Passed = true, want false: the count is 0")
	}
	if o.Err != nil {
		t.Fatalf("Err = %v, want nil: the comparison could be made", o.Err)
	}
	if o.Path != "body.data.count" || o.Operator != ">" {
		t.Errorf("Path, Operator = %q, %q, want %q, %q", o.Path, o.Operator, "body.data.count", ">")
	}
	if o.Expected != float64(0) || o.Actual != float64(0) {
		t.Errorf("Expected, Actual = %v, %v, want 0, 0", o.Expected, o.Actual)
	}

	if got, want := describe(t, "body.data.count > 0", env), "body.data.count > 0, got 0"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

// Message generation, per operator and per shape. These are the lines a failed
// run prints, so they are asserted on rather than the verdict alone.
func TestMessageGeneration(t *testing.T) {
	env := typesEnv()
	cases := []struct {
		src  string
		want string
	}{
		{"status == 200", "status == 200, got 200"},
		{`status == 201`, "status == 201, got 200"},
		{`str == "owner"`, "str == owner, got admin team"},
		{"body.data.count > 0", "body.data.count > 0, got 0"},
		{"body.data.count >= 1", "body.data.count >= 1, got 0"},
		{"num < 2", "num < 2, got 3"},
		{`str contains "owner"`, "str contains owner, got admin team"},
		{`arr contains 2`, "arr contains 2, got [1 a <nil>]"},
		{`str matches /owner/`, "str matches /owner/, got admin team"},
		{"body.data.count exists", "body.data.count exists true, got 0"},
		{"body.nope exists", "body.nope exists true, got <nil>"},
		{"not body.nope exists", "body.nope exists false, got <nil>"},
		{"not body.data.count exists", "body.data.count exists false, got 0"},
		{"num is string", "num is string, got number"},
		{"obj is array", "obj is array, got object"},
		{"not status == 200", "status not == 200, got 200"},
		{`not str contains "admin"`, "str not contains admin, got admin team"},
		{"yes", "yes is true, got true"},
		{"no", "no is true, got false"},

		// An and chain is one assertion, reported as the half that decided it.
		{"status == 200 and body.data.count > 0", "body.data.count > 0, got 0"},
		{"body.data.count > 0 and status == 200", "body.data.count > 0, got 0"},
		{"no and yes", "no is true, got false"},

		// An or chain with one true half reports that half; with none, itself.
		{"status == 201 or body.data.count == 0", "body.data.count == 0, got 0"},
		{"status == 201 or body.data.count == 1", "status == 201 or body.data.count == 1 is true, got false"},

		// Parentheses say nothing about the verdict.
		{"(status == 201)", "status == 201, got 200"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if got := describe(t, c.src, env); got != c.want {
				t.Errorf("Describe() of %q = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

// An errored assertion's line is its reason, not a comparison, because there
// was nothing to compare. This is pkg/shared/assert's shape: "that check could
// not be made" and "that check gave the wrong answer" read differently.
func TestAnErroredAssertionDescribesItsReason(t *testing.T) {
	env := typesEnv()
	cases := []struct {
		src  string
		want string
	}{
		{"body.nope > 0", "body.nope >: body.nope did not resolve"},
		{"body > 0", "body >: > needs a number at body, got object"},
		{`num contains "x"`, "num contains: contains needs a string, array or object at num, got number"},
		{`obj matches /x/`, "obj matches: matches needs a string at obj, got object"},
		{"body.nope is number", "body.nope is: body.nope did not resolve"},
		{"str", "str is: an expect needs a boolean at str, got string"},
		{"num.field exists", "num.field exists: cannot read field field of a number at num"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			o := assertSrc(t, c.src, env)
			if o.Err == nil {
				t.Fatalf("Assert(%q) = %+v, want a reason", c.src, o)
			}
			if got := describe(t, c.src, env); got != c.want {
				t.Errorf("Describe() of %q = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

// Record is the only place this package touches the result tree, and the status
// it chooses is the three-way one ART-1 defined.
func TestRecordChoosesPassFailOrError(t *testing.T) {
	env := typesEnv()
	cases := []struct {
		src  string
		want result.Status
	}{
		{"status == 200", result.StatusPass},
		{"status == 201", result.StatusFail},
		{"body.nope > 0", result.StatusError},
	}
	for _, c := range cases {
		got := assertSrc(t, c.src, env).Record("orders", "expect", 12)
		if got.Status != c.want {
			t.Errorf("Record(%q).Status = %q, want %q", c.src, got.Status, c.want)
		}
		if got.Step != "orders" || got.Kind != "expect" || got.Line != 12 {
			t.Errorf("Record(%q) = %+v, want the step, kind and line it was given", c.src, got)
		}
	}
}

// An assertion's expected and actual keep the types they had, because
// pkg/report's JSON reports them as JSON: a numeric check must not become a
// string on the way out.
func TestRecordKeepsTheOperandsTypes(t *testing.T) {
	got := assertSrc(t, "body.data.count > 0", typesEnv()).Record("orders", "expect", 12)
	if _, ok := got.Expected.(float64); !ok {
		t.Errorf("Expected is %T, want float64", got.Expected)
	}
	if _, ok := got.Actual.(float64); !ok {
		t.Errorf("Actual is %T, want float64", got.Actual)
	}

	got = assertSrc(t, `obj == {"k": 2}`, typesEnv()).Record("orders", "expect", 12)
	if _, ok := got.Expected.(map[string]any); !ok {
		t.Errorf("Expected is %T, want map[string]any -- an object must survive as one", got.Expected)
	}
}

// Assert is pure, which is what `expect ... within "10s"` needs: the waiting
// loop rebuilds the observation and re-asks, and the evaluator holds nothing
// between attempts.
func TestAssertIsRepeatable(t *testing.T) {
	env := typesEnv()
	first := assertSrc(t, "body.data.count > 0", env)
	second := assertSrc(t, "body.data.count > 0", env)
	if first != second {
		t.Errorf("two asserts of the same expression differ: %+v then %+v", first, second)
	}

	// The same expression against a moved-on observation gives the new answer
	// without the Env it was first asked against being touched.
	env.Roots["body"] = map[string]any{"data": map[string]any{"count": float64(3)}}
	if o := assertSrc(t, "body.data.count > 0", env); !o.Passed {
		t.Errorf("after the count changed, Passed = false, want true")
	}
}

// Element functions in a browser step, through the whole assertion path.
func TestBrowserAssertionsReportTheirOperands(t *testing.T) {
	env := browserEnv()
	cases := []struct {
		src  string
		want string
	}{
		{`text("[role=status]") contains "Free"`, `text("[role=status]") contains Free, got Pro`},
		{`visible(".modal")`, `visible(".modal") is true, got false`},
		{`count(".invoice") > 5`, `count(".invoice") > 5, got 3`},
		{`page.url contains "/cancelled"`, "page.url contains /cancelled, got https://app.example.com/billing/confirmed"},
		{`attr("#link", "href") == "/account"`, `attr("#link", "href") == /account, got /billing`},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if got := describe(t, c.src, env); got != c.want {
				t.Errorf("Describe() of %q = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

// The subject's text is the source, normalised: wherever the author broke the
// line or wrote a comment, the message reads as a path.
func TestExprTextReadsAsSource(t *testing.T) {
	cases := map[string]string{
		"body.data.count > 0":                "body.data.count",
		"body.data[0].count > 0":             "body.data[0].count",
		`headers["content-type"] == "x"`:     `headers["content-type"]`,
		`env("API_URL") == "x"`:              `env("API_URL")`,
		`attr("#link", "href") == "x"`:       `attr("#link", "href")`,
		"body .\n  data . count > 0":         "body.data.count",
		"body.data. # which one\n count > 0": "body.data.count",
		"-num > 0":                           "-num",
		`"${url}/orders" == "x"`:             `"${url}/orders"`,
	}
	for src, want := range cases {
		t.Run(src, func(t *testing.T) {
			x, bag := parser.ParseExpr("t.art", src)
			if bag.HasErrors() {
				t.Fatalf("ParseExpr(%q) reported: %v", src, bag.All())
			}
			o := Assert(x, typesEnv())
			if o.Path != want {
				t.Errorf("Path = %q, want %q", o.Path, want)
			}
		})
	}
}

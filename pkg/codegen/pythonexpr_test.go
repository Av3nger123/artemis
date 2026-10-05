package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
)

// exprOf compiles one `expect` in a step of the given type and returns the Python
// its expression became.
//
// It goes through the real front end and the real lowerer rather than building a
// tree, so what is asserted here is what the target emits for a file somebody
// wrote.
func exprOf(t *testing.T, stepType, expression string) string {
	t.Helper()
	src := scenarioAround(stepType, expression)
	scenarios, err := lower.File(parse(t, "expr.art", src), nil)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(scenarios) != 1 || len(scenarios[0].Steps) != 1 || len(scenarios[0].Steps[0].Expects) != 1 {
		t.Fatalf("the fixture for %q did not lower to one expect", expression)
	}
	f := &pyFile{used: map[string]bool{}, mods: map[string]bool{}, taken: map[string]bool{}}
	f.step = scenarios[0].Steps[0].Type
	got, err := f.expr(scenarios[0].Steps[0].Expects[0].Value, precOr)
	if err != nil {
		t.Fatalf("expr(%q): %v", expression, err)
	}
	return got
}

// scenarioAround wraps an expression in the smallest step of the right type that
// puts it in scope.
func scenarioAround(stepType, expression string) string {
	action := `get "https://example.test/"`
	switch stepType {
	case "terminal":
		action = `run "echo"`
	case "browser":
		action = "browser {\n      wait \"1ms\"\n    }"
	}
	return "scenario \"x\" {\n  step \"s\" {\n    " + action +
		"\n    expect " + expression + "\n  }\n}\n"
}

// Every operator and every literal kind, with the Python each becomes. This table
// is the contract pythonexpr.go documents: Python's own operator where the
// meaning is the same, a helper where Python has none.
func TestExpressionsBecomePython(t *testing.T) {
	for _, c := range []struct{ step, art, want string }{
		// comparisons: Python's own operators
		{"api", `status == 200`, "status == 200"},
		{"api", `status != 500`, "status != 500"},
		{"api", `body.n < 1`, `body["n"] < 1`},
		{"api", `body.n <= 1`, `body["n"] <= 1`},
		{"api", `body.n > 1`, `body["n"] > 1`},
		{"api", `body.n >= 1`, `body["n"] >= 1`},

		// the operators Python has no spelling of
		{"api", `raw contains "x"`, `art_contains(raw, "x")`},
		{"api", `raw matches /a.b/`, `art_matches(raw, r"a.b")`},
		{"api", `body.x exists`, `art_exists(lambda: body["x"])`},
		{"api", `body.x is number`, `art_is(body["x"], "number")`},

		// logical: short-circuiting in both languages, same precedence order
		{"api", `status == 200 and body.ok == true`, "status == 200 and body[\"ok\"] == True"},
		{"api", `status == 200 or status == 201`, "status == 200 or status == 201"},
		{"api", `not body.x exists`, `not art_exists(lambda: body["x"])`},
		{"api", `status == 200 and status == 201 or status == 204`,
			"status == 200 and status == 201 or status == 204"},
		{"api", `status == 200 and (status == 201 or status == 204)`,
			"status == 200 and (status == 201 or status == 204)"},
		{"api", `not status == 200 and body.ok == true`, "not status == 200 and body[\"ok\"] == True"},

		// literals
		{"api", `body.x == "a"`, `body["x"] == "a"`},
		{"api", `body.x == 1.5`, `body["x"] == 1.5`},
		{"api", `body.x == -2`, `body["x"] == -2`},
		{"api", `body.x == true`, `body["x"] == True`},
		{"api", `body.x == false`, `body["x"] == False`},
		{"api", `body.x == null`, `body["x"] == None`},
		{"api", `body.x == {"a": 1}`, `body["x"] == {"a": 1}`},
		{"api", `body.x == ["a", 2]`, `body["x"] == ["a", 2]`},

		// paths: a subscript either way, so python's KeyError is the DSL's
		// absent path
		{"api", `body.a.b == 1`, `body["a"]["b"] == 1`},
		{"api", `body.a[0] == 1`, `body["a"][0] == 1`},
		{"api", `headers["content-type"] == "text/html"`, `headers["content-type"] == "text/html"`},

		// builtins
		{"api", `env("HOME") == "/root"`, `art_env("HOME") == "/root"`},
		{"api", `match(raw, /id=([0-9]+)/) == "7"`, `art_match(raw, r"id=([0-9]+)") == "7"`},

		// interpolation: an f-string when the pieces fit in one, concatenation
		// when they do not
		{"api", `raw == "a${status}b"`, `raw == f"a{art_render(status)}b"`},
		{"api", `raw == "${body.x}"`, `raw == art_render(body["x"])`},
		{"api", `raw == "a${body.x}"`, `raw == "a" + art_render(body["x"])`},

		// the terminal roots
		{"terminal", `exit_code == 0`, "exit_code == 0"},
		{"terminal", `stdout contains "ok"`, `art_contains(stdout, "ok")`},
		{"terminal", `stderr == ""`, `stderr == ""`},

		// the browser roots and element functions
		{"browser", `page.url contains "/a"`, `art_contains(page.url, "/a")`},
		{"browser", `page.title == "A"`, `page.title() == "A"`},
		{"browser", `text(".x") == "a"`, `art_text(page, ".x") == "a"`},
		{"browser", `value("#x") == "a"`, `art_value(page, "#x") == "a"`},
		{"browser", `attr("#x", "href") == "/a"`, `art_attr(page, "#x", "href") == "/a"`},
		{"browser", `count(".x") == 2`, `page.locator(".x").count() == 2`},
		{"browser", `visible(".x")`, `page.is_visible(".x")`},
	} {
		if got := exprOf(t, c.step, c.art); got != c.want {
			t.Errorf("%s step, %s\n got: %s\nwant: %s", c.step, c.art, got, c.want)
		}
	}
}

// An author's parentheses are source, not a hint: folding them away would turn
// `(a or b) and c` into a different expression.
func TestParenthesesSurvive(t *testing.T) {
	got := exprOf(t, "api", `(status == 200 or status == 201) and body.ok == true`)
	if !strings.HasPrefix(got, "(status == 200 or status == 201)") {
		t.Errorf("parentheses lost: %s", got)
	}
}

// Every builtin the language has needs a Python spelling, or `artemis build`
// would emit a call to something that does not exist. The arity table is
// pkg/eval's, duplicated here because it is what decides the emission, and this
// is what keeps the two equal.
func TestEveryBuiltinHasASpelling(t *testing.T) {
	for _, typ := range check.StepTypes() {
		for _, fn := range check.Functions(typ) {
			if _, ok := builtinArity[fn]; !ok {
				t.Errorf("%s() is callable in a %s step and has no python spelling", fn, typ)
			}
		}
	}
	for fn := range builtinArity {
		found := false
		for _, typ := range check.StepTypes() {
			for _, have := range check.Functions(typ) {
				if have == fn {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("builtinArity has %q, which the checker does not make callable anywhere", fn)
		}
	}
}

// A pattern Python's re cannot read is refused with its line, rather than emitted
// as a file that raises on import.
func TestPatternRefusesWhatPythonCannotRead(t *testing.T) {
	for _, p := range []string{`\p{L}+`, `\P{L}+`, `a(?i)b`, `(?U)a+`} {
		if _, err := pattern(p, 7); err == nil {
			t.Errorf("pattern(%q) was accepted; python's re does not read it", p)
		} else if !strings.Contains(err.Error(), "line 7") {
			t.Errorf("pattern(%q) = %q, want it to name the line", p, err)
		}
	}
	for _, p := range []string{`(?i)abc`, `ORD-(\d+)`, `a(?:b)c`, `(?P<n>x)`, `[a-z]{2,}`} {
		if _, err := pattern(p, 7); err != nil {
			t.Errorf("pattern(%q) = %v, want it accepted", p, err)
		}
	}
}

// A raw string where one is possible, because that is how a Python author writes
// a regex, and an escaped one where a raw string cannot hold the pattern.
func TestPatternPrefersARawString(t *testing.T) {
	for p, want := range map[string]string{
		`\d+`:      `r"\d+"`,
		`a"b`:      `"a\"b"`,
		`a\`:       `"a\\"`,
		`[0-9]{2}`: `r"[0-9]{2}"`,
	} {
		got, err := pattern(p, 1)
		if err != nil {
			t.Fatalf("pattern(%q) = %v", p, err)
		}
		if got != want {
			t.Errorf("pattern(%q) = %s, want %s", p, got, want)
		}
	}
}

func TestQuoteEscapes(t *testing.T) {
	for s, want := range map[string]string{
		`a`:      `"a"`,
		`a"b`:    `"a\"b"`,
		`a\b`:    `"a\\b"`,
		"a\nb":   `"a\nb"`,
		"a\tb":   `"a\tb"`,
		"a\x00b": `"a\x00b"`,
		`a{b}c`:  `"a{b}c"`, // a plain literal keeps its braces
		"héllo":  `"héllo"`,
	} {
		if got := quote(s); got != want {
			t.Errorf("quote(%q) = %s, want %s", s, got, want)
		}
	}
	// An f-string's literal text doubles them, because a brace there opens an
	// interpolation.
	if got := escapeF(`a{b}`); got != `a{{b}}` {
		t.Errorf("escapeF = %s, want a{{b}}", got)
	}
}

// A nil expression and a Bad node are both reasons rather than panics: the parser
// recovers and hands on a half-built tree, and a target may be given one.
func TestExprRefusesNilAndBad(t *testing.T) {
	f := &pyFile{used: map[string]bool{}, mods: map[string]bool{}}
	if _, err := f.expr(nil, precOr); err == nil {
		t.Error("expr(nil) was accepted")
	}
	if _, err := f.expr(&ast.Bad{}, precOr); err == nil {
		t.Error("expr(Bad) was accepted")
	}
}

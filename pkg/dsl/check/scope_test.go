package check

import (
	"fmt"
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// TestScopeRules pins the resolution rules themselves, as codes at positions.
//
// The corpus in pkg/dsl/testdata/invalid pins the *text* a person receives;
// this pins the *rule*, because a rule is a short statement about a short file
// and reading twenty of them next to each other is how you see that the set is
// complete. The two are complementary: a golden here would be twenty files to
// open, and a rule there would be invisible among the hints.
//
// `want` is one `code@line:col` per expected diagnostic, in file order. An
// empty want means the file checks clean, which is half the cases: a scope
// rule that rejects correct code is as much a bug as one that accepts wrong
// code, and the clean cases are the ones a regression breaks first.
func TestScopeRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		// --- the three step types bind their own roots -------------------
		{
			name: "api roots resolve",
			src: step("api", `get "/x"`, `expect status == 200`,
				`expect body.data.count > 0`, `expect raw contains "ok"`,
				`expect headers["content-type"] contains "json"`),
		},
		{
			name: "terminal roots resolve",
			src:  step("terminal", `run "echo"`, `expect exit_code == 0`, `expect stdout contains "x"`, `expect stderr == ""`),
		},
		{
			name: "browser roots resolve",
			src: step("browser", `browser { goto "/" }`, `expect page.url contains "/x"`,
				`expect page.title == "T"`, `expect text(".x") contains "y"`,
				`expect value("#e") == "a"`, `expect attr("#l", "href") contains "/p"`,
				`expect count(".i") == 1`, `expect visible(".m")`),
		},

		// --- and only their own ------------------------------------------
		{
			name: "an api root in a terminal step",
			src:  step("terminal", `run "echo"`, `expect status == 200`),
			want: []string{"not-in-scope@4:12"},
		},
		{
			name: "a terminal root in an api step",
			src:  step("api", `get "/x"`, `expect stdout contains "y"`),
			want: []string{"not-in-scope@4:12"},
		},
		{
			name: "a browser root in a terminal step",
			src:  step("terminal", `run "echo"`, `expect page.url contains "/x"`),
			want: []string{"not-in-scope@4:12"},
		},
		{
			name: "an element function in a terminal step",
			src:  step("terminal", `run "echo"`, `expect text(".x") contains "y"`),
			want: []string{"not-in-scope@4:12"},
		},

		// --- vars ---------------------------------------------------------
		{
			name: "a var sees the vars above it",
			src:  "scenario \"s\" {\n  var a = 1\n  var b = a\n}\n",
		},
		{
			name: "a var does not see the vars below it",
			src:  "scenario \"s\" {\n  var b = a\n  var a = 1\n}\n",
			want: []string{"unknown-identifier@2:11"},
		},
		{
			name: "a var does not see itself",
			src:  "scenario \"s\" {\n  var a = a\n}\n",
			want: []string{"unknown-identifier@2:11"},
		},
		{
			name: "a var's value binds no step roots",
			src:  "scenario \"s\" {\n  var a = status\n}\n",
			want: []string{"not-in-scope@2:11"},
		},
		{
			name: "env() resolves in a var's value",
			src:  "scenario \"s\" {\n  var a = env(\"API_URL\")\n}\n",
		},
		{
			name: "a var shadows a root it collides with",
			src:  "scenario \"s\" {\n  var status = 1\n  step \"t\" {\n    browser { goto \"/\" }\n    expect status == 1\n  }\n}\n",
		},

		// --- captures -----------------------------------------------------
		{
			name: "a capture is in scope in a later step",
			src: "scenario \"s\" {\n  step \"one\" {\n    get \"/x\"\n    capture tok = body.tok\n  }\n" +
				"  step \"two\" {\n    get \"/y\" {\n      header \"Authorization\" = \"Bearer ${tok}\"\n    }\n  }\n}\n",
		},
		{
			name: "a capture is not in scope in its own step",
			src:  step("api", `get "/x"`, `capture tok = body.tok`, `expect tok exists`),
			want: []string{"unknown-identifier@5:12"},
		},
		{
			name: "a capture is not in scope in an earlier step",
			src: "scenario \"s\" {\n  step \"one\" {\n    get \"/${tok}\"\n  }\n" +
				"  step \"two\" {\n    get \"/y\"\n    capture tok = body.tok\n  }\n}\n",
			want: []string{"unknown-identifier@3:13"},
		},
		{
			name: "a capture does not leak into the next scenario",
			src: "scenario \"one\" {\n  step \"t\" {\n    get \"/x\"\n    capture tok = body.tok\n  }\n}\n" +
				"scenario \"two\" {\n  step \"t\" {\n    get \"/${tok}\"\n  }\n}\n",
			want: []string{"unknown-identifier@9:13"},
		},

		// --- the near-miss split between the two unknown-name codes -------
		{
			name: "a near miss of a root is an unknown field",
			src:  step("api", `get "/x"`, `expect statu == 200`),
			want: []string{"unknown-field@4:12"},
		},
		{
			name: "a near miss of a var is an unknown name",
			src:  "scenario \"s\" {\n  var base = 1\n  step \"t\" {\n    get \"/x\"\n    expect bse == 1\n  }\n}\n",
			want: []string{"unknown-identifier@5:12"},
		},
		{
			name: "a name near nothing is an unknown name",
			src:  step("api", `get "/x"`, `expect wholly_invented == 1`),
			want: []string{"unknown-identifier@4:12"},
		},

		// --- members ------------------------------------------------------
		{
			name: "a response's shape is never checked",
			src:  step("api", `get "/x"`, `expect body.anything.at.all[3].x exists`),
		},
		{
			name: "page's members are",
			src:  step("browser", `browser { goto "/" }`, `expect page.ur == "x"`),
			want: []string{"unknown-field@4:17"},
		},

		// --- reserved words, in every name position -----------------------
		{
			name: "reserved as a var name",
			src:  "scenario \"s\" {\n  var let = 1\n}\n",
			want: []string{"reserved-word@2:7"},
		},
		{
			name: "reserved as a capture name",
			src:  step("api", `get "/x"`, `capture import = body.x`),
			want: []string{"reserved-word@4:13"},
		},
		{
			name: "reserved as a config subject",
			src:  "scenario \"s\" {\n  config group { headless = true }\n}\n",
			want: []string{"reserved-word@2:10"},
		},
		{
			name: "reserved as a field name",
			src:  "scenario \"s\" {\n  step \"t\" {\n    get \"/x\" {\n      use \"X\" = 1\n    }\n  }\n}\n",
			want: []string{"reserved-word@4:7"},
		},
		{
			name: "reserved as an env setting name",
			src:  "scenario \"s\" {\n  step \"t\" {\n    run \"x\" {\n      env { fn = \"1\" }\n    }\n  }\n}\n",
			want: []string{"reserved-word@4:13"},
		},
		{
			name: "reserved as a referenced name",
			src:  step("api", `get "/x"`, `expect while == 1`),
			want: []string{"reserved-word@4:12"},
		},

		// --- match(), the regex extraction a capture spells --------------
		{
			name: "match() resolves in an api step, against raw",
			src:  step("api", `get "/x"`, `capture id = match(raw, /\/items\/([0-9]+)/)`),
		},
		{
			name: "match() resolves in a terminal step, against stdout",
			src:  step("terminal", `run "git rev-parse HEAD"`, `capture sha = match(stdout, /^([0-9a-f]{40})/)`),
		},
		{
			name: "match() resolves in a browser step",
			src:  step("browser", `browser { goto "/" }`, `capture t = match(page.title, /^([A-Z]\w+)/)`),
		},
		{
			name: "match() resolves in a var's value, like env()",
			src:  "scenario \"s\" {\n  var v = match(env(\"TAG\"), /v([0-9.]+)/)\n}\n",
		},
		{
			name: "match() is an expression, not a capture-only form",
			src:  step("api", `get "/x"`, `expect match(raw, /id=([0-9]+)/) == "42"`),
		},
		{
			name: "a pattern may be a string, as it may be after matches",
			src:  "scenario \"s\" {\n  var re = \"id=([0-9]+)\"\n  step \"t\" {\n    get \"/x\"\n    capture id = match(raw, re)\n  }\n}\n",
		},
		{
			name: "non-capturing groups do not count",
			src:  step("api", `get "/x"`, `capture id = match(raw, /(?:items|orders)\/([0-9]+)/)`),
		},

		// --- the two faults that move to compile time ---------------------
		{
			name: "a regex is compiled wherever it appears",
			src:  "scenario \"s\" {\n  var re = /a(b/\n}\n",
			want: []string{"invalid-regex@2:12"},
		},
		{
			name: "a within budget is a duration",
			src:  step("api", `get "/x"`, `expect status == 200 within "soon"`),
			want: []string{"invalid-duration@4:33"},
		},
		{
			name: "an interpolated budget is not checked",
			src:  "scenario \"s\" {\n  var t = \"1s\"\n  step \"u\" {\n    get \"/x\"\n    expect status == 200 within \"${t}\"\n  }\n}\n",
		},

		// --- env settings take any name ----------------------------------
		{
			name: "an env block's names are arbitrary",
			src:  "scenario \"s\" {\n  step \"t\" {\n    run \"x\" {\n      env { PGPASSWORD = \"s\", ANYTHING_AT_ALL = \"t\" }\n    }\n  }\n}\n",
		},

		// --- a name is bound once ----------------------------------------
		{
			name: "a capture may not reuse a var's name",
			src:  "scenario \"s\" {\n  var token = \"a\"\n  step \"t\" {\n    get \"/x\"\n    capture token = body.t\n  }\n}\n",
			want: []string{"duplicate-binding@5:13"},
		},
		{
			name: "a capture may not reuse a var declared below it",
			src:  "scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    capture token = body.t\n  }\n  var token = \"a\"\n}\n",
			want: []string{"duplicate-binding@4:13"},
		},
		{
			name: "a capture may not reuse an earlier step's capture",
			src:  "scenario \"s\" {\n  step \"a\" {\n    get \"/x\"\n    capture token = body.t\n  }\n  step \"b\" {\n    get \"/y\"\n    capture token = body.u\n  }\n}\n",
			want: []string{"duplicate-binding@8:13"},
		},
		{
			name: "a capture may not reuse a capture in its own step",
			src:  step("api", `get "/x"`, `capture token = body.t`, `capture token = body.u`),
			want: []string{"duplicate-binding@5:13"},
		},
		{
			name: "distinct captures and vars are fine",
			src:  "scenario \"s\" {\n  var a = \"a\"\n  step \"t\" {\n    get \"/x\"\n    capture b = body.t\n    capture c = body.u\n  }\n}\n",
		},

		// --- a Bad node is never reported on twice ------------------------
		{
			name: "a bad object literal draws no name error",
			src:  "scenario \"s\" {\n  step \"t\" {\n    get \"/x\" {\n      body = {\"a\" \"b\"}\n    }\n  }\n}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, _ := parser.Parse("t.art", tc.src)
			_, bag := Check(tree)

			var got []string
			for _, d := range bag.All() {
				got = append(got, fmt.Sprintf("%s@%d:%d", d.Code, d.Span.Line, d.Span.Col))
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("got %v, want %v\nsource:\n%s\nchecker said:\n%s",
					got, tc.want, tc.src, render("t.art", tc.src, bag))
			}
			for _, d := range bag.All() {
				if !diag.Registered(d.Code) {
					t.Errorf("code %q is not registered", d.Code)
				}
				// Every checker diagnostic carries a hint, because every one of
				// them knows what would have worked. The exception is
				// invalid-regex, whose message is Go's own compile error --
				// which already names the paren, and which nothing here can
				// improve on.
				if d.Hint == "" && d.Code != diag.InvalidRegex {
					t.Errorf("%s at %d:%d has no hint; every name fault has something "+
						"useful to say about what would have worked",
						d.Code, d.Span.Line, d.Span.Col)
				}
			}
		})
	}
}

// step builds a one-step scenario whose action and statements are the lines
// given, so a case above reads as the rule it is testing. The step's body
// starts on line 3, so its first line is at 3 and its first statement at 4.
func step(name, action string, stmts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scenario \"s\" {\n  step %q {\n    %s\n", name, action)
	for _, s := range stmts {
		fmt.Fprintf(&b, "    %s\n", s)
	}
	b.WriteString("  }\n}\n")
	return b.String()
}

// TestDuplicateBindingNamesTheFirstBinding pins the hint: it says where the
// name was bound first, by kind and line, because "already bound" alone sends
// an author hunting through the scenario.
func TestDuplicateBindingNamesTheFirstBinding(t *testing.T) {
	cases := []struct{ src, msg, hint string }{
		{
			src:  "scenario \"s\" {\n  var token = \"a\"\n  step \"t\" {\n    get \"/x\"\n    capture token = body.t\n  }\n}\n",
			msg:  `"token" is already bound`,
			hint: "the var at line 2 binds it first",
		},
		{
			src:  "scenario \"s\" {\n  step \"a\" {\n    get \"/x\"\n    capture token = body.t\n  }\n  step \"b\" {\n    get \"/y\"\n    capture token = body.u\n  }\n}\n",
			msg:  `"token" is already bound`,
			hint: "the capture at line 4 binds it first",
		},
	}
	for _, tc := range cases {
		tree, _ := parser.Parse("t.art", tc.src)
		_, bag := Check(tree)
		all := bag.All()
		if len(all) != 1 || all[0].Code != diag.DuplicateBinding {
			t.Fatalf("want one duplicate-binding, got %v", all)
		}
		if all[0].Message != tc.msg || all[0].Hint != tc.hint {
			t.Errorf("got %q / %q, want %q / %q", all[0].Message, all[0].Hint, tc.msg, tc.hint)
		}
	}
}

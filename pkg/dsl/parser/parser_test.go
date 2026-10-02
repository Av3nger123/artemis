package parser

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// TestPrecedence is the heart of the expression tests: the shape the parser
// chooses, for every level of the grammar and for the pairs of levels where
// choosing wrong is plausible.
func TestPrecedence(t *testing.T) {
	cases := []struct{ src, want string }{
		// The precedence detail worth getting right the first time. `not`
		// binds looser than the predicates, so its operand is a whole
		// predicate -- `not (body.x exists)` is the only reading anyone
		// wants -- and tighter than `and`/`or`, so it does not swallow them.
		{"not body.x exists", "(not (exists body.x))"},
		{"not body.x is number", "(not (is number body.x))"},
		{"not a and b", "(and (not a) b)"},
		{"a and not b", "(and a (not b))"},
		{"not not a", "(not (not a))"},
		{"not a == 1", "(not (== a 1))"},

		// `and` binds tighter than `or`, and both are left-associative.
		{"a or b and c", "(or a (and b c))"},
		{"a and b or c", "(or (and a b) c)"},
		{"a or b or c", "(or (or a b) c)"},
		{"a and b and c", "(and (and a b) c)"},

		// Parentheses are kept as nodes, because folding them away would make
		// the printer reproduce a different expression.
		{"(a or b) and c", "(and (paren (or a b)) c)"},

		// Comparison binds tighter than the logical operators and looser than
		// the postfix and unary levels.
		{"a == 1 and b == 2", "(and (== a 1) (== b 2))"},
		{"body.data.count > 0", "(> body.data.count 0)"},
		{"-a < b", "(< (- a) b)"},
		{"a[0].b exists", "(exists a[0].b)"},
		{`count(".x") == 3`, `(== (call count ".x") 3)`},

		// Every BinOp in the grammar.
		{"a != b", "(!= a b)"},
		{"a <= b", "(<= a b)"},
		{"a >= b", "(>= a b)"},
		{"a < b", "(< a b)"},
		{`a contains "x"`, `(contains a "x")`},
		{"a matches /x/", "(matches a /x/)"},

		// Every TypeName.
		{"a is string", "(is string a)"},
		{"a is number", "(is number a)"},
		{"a is boolean", "(is boolean a)"},
		{"a is object", "(is object a)"},
		{"a is array", "(is array a)"},
		{"a is null", "(is null a)"},

		// Unary minus is right-recursive.
		{"--a", "(- (- a))"},
		{"-1", "(- 1)"},
	}

	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			x := parseExprOK(t, c.src)
			if got := shape(x); got != c.want {
				t.Errorf("shape = %s, want %s", got, c.want)
			}
			assertRoundTrip(t, c.src, x)
		})
	}
}

func TestPrimaryForms(t *testing.T) {
	cases := []struct{ src, want string }{
		{"42", "42"},
		{"3.5", "3.5"},
		{"1e9", "1e9"},
		{`"plain"`, `"plain"`},
		{"/.+@.+/", "/.+@.+/"},
		{"true", "true"},
		{"false", "false"},
		{"null", "null"},
		{"status", "status"},

		// Postfix: member, index, and the two combined.
		{"body.data.id", "body.data.id"},
		{"body.items[0]", "body.items[0]"},
		{`headers["content-type"]`, `headers["content-type"]`},
		{"body.items[0].name", "body.items[0].name"},

		// Calls, including the builtins the design names.
		{`env("API_URL")`, `(call env "API_URL")`},
		{`attr("#a", "href")`, `(call attr "#a" "href")`},
		{"env()", "(call env)"},
		{`text(".x")`, `(call text ".x")`},
		{`visible(".x")`, `(call visible ".x")`},
		{`value("#f")`, `(call value "#f")`},

		// Object and array literals, including nesting and trailing commas.
		{`{"a": 1}`, `(object (entry "a" 1))`},
		{`{"a": 1, "b": pw}`, `(object (entry "a" 1) (entry "b" pw))`},
		{"{}", "(object)"},
		{`{"a": 1,}`, `(object (entry "a" 1))`},
		{`["-f", "seed.sql"]`, `(array "-f" "seed.sql")`},
		{"[]", "(array)"},
		{"[1,]", "(array 1)"},
		{`{"a": [1, {"b": 2}]}`, `(object (entry "a" (array 1 (object (entry "b" 2)))))`},

		// Interpolated strings: the segments hold expressions, and a plain
		// string stays a single Literal rather than becoming an Interp.
		{`"${url}/token"`, "(interp url)"},
		{`"${base}/users/${body.data.id}"`, "(interp base body.data.id)"},
		{`"Bearer ${token}"`, "(interp token)"},
		{`"${a}${b}"`, "(interp a b)"},
		{`"${env("X")}"`, `(interp (call env "X"))`},
	}

	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			x := parseExprOK(t, c.src)
			if got := shape(x); got != c.want {
				t.Errorf("shape = %s, want %s", got, c.want)
			}
			assertRoundTrip(t, c.src, x)
		})
	}
}

// TestPlainStringIsLiteralNotInterp pins the split the lexer chose: a string
// with no `${` is one token and so one Literal, which is what lets a client
// ask "is this a plain string" without counting segments.
func TestPlainStringIsLiteralNotInterp(t *testing.T) {
	x := parseExprOK(t, `"no interpolation here"`)
	lit, ok := x.(*ast.Literal)
	if !ok {
		t.Fatalf("got %T, want *ast.Literal", x)
	}
	if lit.Kind() != token.String {
		t.Errorf("Kind = %v, want String", lit.Kind())
	}
	if lit.Tok.Value != "no interpolation here" {
		t.Errorf("Value = %q, want the decoded text without quotes", lit.Tok.Value)
	}
	if lit.Tok.Text != `"no interpolation here"` {
		t.Errorf("Text = %q, want the source with quotes", lit.Tok.Text)
	}
}

func TestScenarioAndDeclarations(t *testing.T) {
	src := `scenario "checkout" {
  config browser { headless = true, viewport = "1280x720" }

  var url = env("API_URL")
  var n = 3

  step "login" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": "alice"}
    }
    expect status == 200
    capture token = body.data.access_token
  }
}
`
	f := parseOK(t, src)

	if len(f.Scenarios) != 1 {
		t.Fatalf("scenarios = %d, want 1", len(f.Scenarios))
	}
	s, ok := f.Scenarios[0].(*ast.Scenario)
	if !ok {
		t.Fatalf("got %T, want *ast.Scenario", f.Scenarios[0])
	}

	// A scenario's name is a token in the file, not an inferred label: the
	// report prints it and a filter matches it.
	if s.Name.Value != "checkout" {
		t.Errorf("scenario name = %q, want checkout", s.Name.Value)
	}
	if len(s.Body) != 4 {
		t.Fatalf("scenario body = %d declarations, want 4", len(s.Body))
	}

	want := []string{
		`(config browser (block (field headless true) (field viewport "1280x720")))`,
		`(var url (call env "API_URL"))`,
		`(var n 3)`,
		`(step "login" (request post (interp url) (block (field header key="Content-Type" "application/json") (field body (object (entry "username" "alice"))))) (expect (== status 200)) (capture token body.data.access_token))`,
	}
	for i, w := range want {
		if got := shape(s.Body[i]); got != w {
			t.Errorf("declaration %d:\n got %s\nwant %s", i, got, w)
		}
	}
}

// TestNamedStepIdentityIsSyntactic is R7. ART-1's result tree prints a line
// per named step, so the name and the boundary have to be in the syntax: the
// lowerer hands this node to the executor, and a tree where the name were
// inferred could not keep that granularity.
func TestNamedStepIdentityIsSyntactic(t *testing.T) {
	f := parseOK(t, `scenario "s" {
  step "seed the database" {
    run "psql"
  }
  step "login" {
    post "/token"
  }
}
`)
	var names []string
	ast.Inspect(f, func(n ast.Node) {
		if step, ok := n.(*ast.StepDecl); ok {
			names = append(names, step.Name.Value)
		}
	})
	if len(names) != 2 || names[0] != "seed the database" || names[1] != "login" {
		t.Fatalf("step names = %q, want [seed the database, login]", names)
	}
}

func TestActionForms(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{
			"http verb with no block",
			`get "${url}/orders"`,
			`(request get (interp url))`,
		},
		{
			"http verb with a block",
			`get "/orders" { query "limit" = 10 }`,
			`(request get "/orders" (block (field query key="limit" 10)))`,
		},
		{
			"every method",
			"head \"/x\"",
			`(request head "/x")`,
		},
		{
			"run with no block",
			`run "ls"`,
			`(run "ls")`,
		},
		{
			"run with every field",
			"run \"psql\" {\n      args = [\"-f\", \"seed.sql\"]\n      cwd = \"db\"\n      stdin = \"x\"\n      env { PGPASSWORD = pw }\n    }",
			`(run "psql" (block (field args (array "-f" "seed.sql")) (field cwd "db") (field stdin "x") (field env (block (field PGPASSWORD pw)))))`,
		},
		{
			"browser with every action",
			"browser {\n      goto \"/billing\"\n      click \"text=Sign in\"\n      fill \"#email\" = \"a@b.c\"\n      select \"#plan\" = \"pro\"\n      press \"Enter\"\n      hover \".menu\"\n      upload \"#file\" = \"a.png\"\n      wait \"1s\"\n    }",
			`(browser (goto "/billing") (click "text=Sign in") (fill "#email" "a@b.c") (select "#plan" "pro") (press "Enter") (hover ".menu") (upload "#file" "a.png") (wait "1s"))`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "scenario \"s\" {\n  step \"t\" {\n    " + c.body + "\n  }\n}\n"
			f := parseOK(t, src)
			step := firstStep(t, f)
			if got := shape(step.Action); got != c.want {
				t.Errorf("action:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// TestEveryHTTPVerbOpensAnApiStep walks token.Methods rather than listing the
// verbs again, so a verb added to the table without a parser change fails
// here.
func TestEveryHTTPVerbOpensAnApiStep(t *testing.T) {
	for _, m := range token.Methods {
		t.Run(m, func(t *testing.T) {
			f := parseOK(t, "scenario \"s\" {\n  step \"t\" {\n    "+m+" \"/x\"\n  }\n}\n")
			req, ok := firstStep(t, f).Action.(*ast.Request)
			if !ok {
				t.Fatalf("action is %T, want *ast.Request", firstStep(t, f).Action)
			}
			if req.Method.Value != m {
				t.Errorf("method = %q, want %q", req.Method.Value, m)
			}
		})
	}
}

// TestEveryBrowserActionParses walks token.BrowserActions for the same reason.
func TestEveryBrowserActionParses(t *testing.T) {
	for _, a := range token.BrowserActions {
		t.Run(a, func(t *testing.T) {
			// Two arguments, which the parser accepts for every action: arity
			// is the checker's table, so that `click "x" = 1` gets an error
			// naming `click` rather than one about an unexpected "=".
			src := "scenario \"s\" {\n  step \"t\" {\n    browser { " + a + " \"#x\" = \"v\" }\n  }\n}\n"
			f := parseOK(t, src)
			b, ok := firstStep(t, f).Action.(*ast.Browser)
			if !ok {
				t.Fatalf("action is %T, want *ast.Browser", firstStep(t, f).Action)
			}
			if len(b.Acts) != 1 {
				t.Fatalf("acts = %d, want one %q", len(b.Acts), a)
			}
			act, ok := b.Acts[0].(*ast.BrowserAct)
			if !ok {
				t.Fatalf("act is %T, want *ast.BrowserAct", b.Acts[0])
			}
			if act.Name.Value != a {
				t.Fatalf("act is %q, want %q", act.Name.Value, a)
			}
			if act.Value == nil {
				t.Errorf("the value after \"=\" was dropped")
			}
		})
	}
}

func TestStepStatements(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"expect", "expect status == 200", "(expect (== status 200))"},
		{"expect with within", `expect page.url contains "/x" within "10s"`, `(expect within="10s" (contains page.url "/x"))`},
		{"capture", "capture token = body.data.access_token", "(capture token body.data.access_token)"},
		{"capture from a call", `capture invoice = text(".total")`, `(capture invoice (call text ".total"))`},
		{"timeout", `timeout = "5s"`, `(field timeout "5s")`},
		{"retry", `retry { times = 3, delay = "2s" }`, `(field retry (block (field times 3) (field delay "2s")))`},
		{"retry across lines", "retry {\n      times = 3\n      delay = \"2s\"\n    }", `(field retry (block (field times 3) (field delay "2s")))`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "scenario \"s\" {\n  step \"t\" {\n    run \"x\"\n    " + c.body + "\n  }\n}\n"
			f := parseOK(t, src)
			step := firstStep(t, f)
			if len(step.Body) != 1 {
				t.Fatalf("step body = %d statements, want 1", len(step.Body))
			}
			if got := shape(step.Body[0]); got != c.want {
				t.Errorf("statement:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// TestTimeoutAndRetryAreFields pins the decision that `timeout = "5s"` is a
// field of the step rather than a statement type of its own, because that is
// the tree contract ART-43 serialises and subsystem D edits.
func TestTimeoutAndRetryAreFields(t *testing.T) {
	f := parseOK(t, "scenario \"s\" {\n  step \"t\" {\n    run \"x\"\n    timeout = \"5s\"\n    retry { times = 3 }\n  }\n}\n")
	step := firstStep(t, f)
	for i, want := range []string{"timeout", "retry"} {
		field, ok := step.Body[i].(*ast.Field)
		if !ok {
			t.Fatalf("statement %d is %T, want *ast.Field", i, step.Body[i])
		}
		if field.Name.Value != want {
			t.Errorf("field %d is %q, want %q", i, field.Name.Value, want)
		}
	}
}

// TestNewlineSeparatesStatements is R4 on the separator rule, both ways: a
// newline ends a statement, and a `,` is accepted where one separates fields.
func TestNewlineSeparatesStatements(t *testing.T) {
	// Three expects on three lines, with no punctuation anywhere.
	f := parseOK(t, `scenario "s" {
  step "t" {
    run "x"
    expect exit_code == 0
    expect stdout contains "ok"
    expect stderr exists
  }
}
`)
	if n := len(firstStep(t, f).Body); n != 3 {
		t.Fatalf("statements = %d, want 3", n)
	}

	// An operator at the start of the next line does not continue the
	// expression above it: that is two statements, and the second is an error
	// rather than a silent join.
	_, bag := Parse("t.art", "scenario \"s\" {\n  step \"t\" {\n    run \"x\"\n    expect a\n    and b\n  }\n}\n")
	if !bag.HasErrors() {
		t.Error("an expression continued across a newline at depth zero")
	}
}

// TestNewlinesInsideBracketsAreNotSeparators is the other half of the rule:
// inside a bracket a newline is ordinary trivia, which is what lets a request
// body be a multi-line object literal.
func TestNewlinesInsideBracketsAreNotSeparators(t *testing.T) {
	f := parseOK(t, `scenario "s" {
  step "t" {
    post "/x" {
      body = {
        "username": "alice",
        "roles": [
          "admin",
          "user",
        ],
      }
    }
  }
}
`)
	req := firstStep(t, f).Action.(*ast.Request)
	want := `(request post "/x" (block (field body (object (entry "username" "alice") (entry "roles" (array "admin" "user"))))))`
	if got := shape(req); got != want {
		t.Errorf("multi-line body:\n got %s\nwant %s", got, want)
	}
}

// TestEmptyFileIsValid: the grammar is `File = { Scenario }`, so nothing is a
// legal file. Whitespace-only and comment-only files are the ones a UI writes
// before the user has added anything.
func TestEmptyFileIsValid(t *testing.T) {
	for _, src := range []string{"", "\n", "   \n\n", "# just a comment\n"} {
		f := parseOK(t, src)
		if len(f.Scenarios) != 0 {
			t.Errorf("%q yielded %d scenarios, want 0", src, len(f.Scenarios))
		}
	}
}

// TestCommentsAndBlankLinesSurvive: trivia is the reason the tree is concrete,
// so the round trip is asserted over a file that is mostly comments.
func TestCommentsAndBlankLinesSurvive(t *testing.T) {
	src := `# a leading comment

scenario "s" {   # trailing on the brace line

  # about the step
  step "t" {
    run "x"   # about the action

  }

}
# a trailing comment
`
	parseOK(t, src)
}

// TestKeywordsAreOrdinaryNames is what token/tables.go's "the lexer classifies
// almost none of these" buys: `capture body = ...` and `var status = ...` are
// files someone will write, and a parser that promoted words to keywords would
// make them unparseable.
func TestKeywordsAreOrdinaryNames(t *testing.T) {
	src := `scenario "s" {
  var status = env("S")
  var contains = 1
  var within = 2
  step "t" {
    run "x"
    capture body = stdout
    capture exists = stderr
    expect contains == 1
  }
}
`
	f := parseOK(t, src)
	var names []string
	ast.Inspect(f, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.VarDecl:
			names = append(names, n.Name.Value)
		case *ast.Capture:
			names = append(names, n.Name.Value)
		}
	})
	want := []string{"status", "contains", "within", "body", "exists"}
	if len(names) != len(want) {
		t.Fatalf("bound names = %q, want %q", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("bound name %d = %q, want %q", i, names[i], want[i])
		}
	}
}

// TestReservedWordsParseAsIdents: rejecting them is the checker's job, because
// the error has to name what the word is reserved for, and that is ART-33's
// vocabulary. The parser must not get there first.
func TestReservedWordsParseAsIdents(t *testing.T) {
	for _, w := range token.Reserved {
		t.Run(w, func(t *testing.T) {
			x, bag := ParseExpr("t.art", w)
			assertNoDiagnostics(t, w, bag)
			id, ok := x.(*ast.Ident)
			if !ok {
				t.Fatalf("%q parsed as %T, want *ast.Ident", w, x)
			}
			if id.Name() != w {
				t.Errorf("name = %q, want %q", id.Name(), w)
			}
		})
	}
}

// TestParseExprRejectsTrailingTokens: a UI validating one field live needs
// `a b` reported, not half-accepted.
func TestParseExprRejectsTrailingTokens(t *testing.T) {
	_, bag := ParseExpr("t.art", "a b")
	if got := codes(bag); len(got) != 1 || got[0] != diag.UnexpectedToken {
		t.Errorf("codes = %v, want [unexpected-token]", got)
	}
}

// firstStep is the first step of the first scenario.
func firstStep(t *testing.T, f *ast.File) *ast.StepDecl {
	t.Helper()
	var found *ast.StepDecl
	ast.Walk(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.StepDecl); ok && found == nil {
			found = s
		}
		return found == nil
	})
	if found == nil {
		t.Fatal("no step in the file")
	}
	return found
}

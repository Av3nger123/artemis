package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// The JavaScript target's own behaviour, as against the expression rules in
// jsexpr_test.go and the whole modules in the goldens.

// `expect` is imported from exactly one module, and which one depends on the
// file: Playwright's expect is a superset of the jest-compatible one, so a module
// with a browser step uses it for the boolean assertions too. Two bindings called
// `expect` would not compile, and importing vitest's alongside a web-first
// matcher would not work.
func TestJSImportsOneExpect(t *testing.T) {
	for _, c := range []struct{ name, src, want, notWant string }{{
		name: "an api scenario takes vitest's",
		src: `scenario "x" {
  step "s" {
    get "https://example.test/"
    expect status == 200
  }
}
`,
		want:    `import { expect, test } from "vitest";`,
		notWant: `from "@playwright/test"`,
	}, {
		name: "a browser scenario takes playwright's",
		src: `scenario "x" {
  step "s" {
    browser {
      goto "https://example.test/"
    }
    expect page.title == "Home"
  }
}
`,
		want:    `import { chromium, expect } from "@playwright/test";`,
		notWant: `import { expect`,
	}, {
		name: "and so does a file with one of each, because the file is what imports",
		src: `scenario "api" {
  step "s" {
    get "https://example.test/"
    expect status == 200
  }
}

scenario "browser" {
  step "s" {
    browser {
      goto "https://example.test/"
    }
    expect page.title == "Home"
  }
}
`,
		want:    `import { chromium, expect } from "@playwright/test";`,
		notWant: `import { expect`,
	}} {
		t.Run(c.name, func(t *testing.T) {
			files, err := JS{}.Generate(parse(t, "x.art", c.src))
			if err != nil {
				t.Fatal(err)
			}
			imports := importsOf(files[0].Content)
			if !strings.Contains(imports, c.want) {
				t.Errorf("the imports are\n%s\nwant %s", imports, c.want)
			}
			if c.notWant != "" && strings.Contains(imports, c.notWant) {
				t.Errorf("the imports are\n%s\nand must not hold %s", imports, c.notWant)
			}
			if strings.Count(imports, "expect") != 1 {
				t.Errorf("the imports name expect %d times:\n%s",
					strings.Count(imports, "expect"), imports)
			}
		})
	}
}

// importsOf is the module's import block.
func importsOf(module string) string {
	var lines []string
	for _, line := range strings.Split(module, "\n") {
		if strings.HasPrefix(line, "import ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// Every binding a test assigns is declared once at the top, which is what makes
// a capture written inside a retry callback visible to the steps below it -- and
// what keeps a name bound in two steps from being a redeclaration.
func TestJSDeclaresEveryBindingOnce(t *testing.T) {
	files, err := JS{}.Generate(parse(t, "x.art", `scenario "x" {
  var base = "https://example.test"

  step "first" {
    get "${base}/a"
    retry { times = 2 }
    expect status == 200
    capture id = raw
  }

  step "second" {
    run "echo" {
      args = [id]
    }
    expect exit_code == 0
    capture id = stdout
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	module := files[0].Content

	// One `let`, naming the var, the capture once however many steps bind it,
	// and the roots and locals of both step types.
	if n := strings.Count(module, "  let "); n != 1 {
		t.Errorf("the test has %d let lines, want 1:\n%s", n, module)
	}
	for _, want := range []string{
		"  let base, id, status, body, raw, headers, resp, exit_code, stdout, stderr,\n    proc;\n",
		// The capture is assigned, not declared, inside the retry callback.
		"    id = raw;",
		"  id = stdout;",
	} {
		if !strings.Contains(module, want) {
			t.Errorf("the module does not hold %q:\n%s", want, module)
		}
	}
	// And `page` is not among them: a browser step's root is bound once as a
	// const, before the first step.
	if strings.Contains(module, "page") {
		t.Errorf("a scenario with no browser step names page:\n%s", module)
	}
}

// A retried step is a callback, and the step's action, its assertions and its
// captures are all inside it -- so they retry together, which is what `retry`
// means under the interpreter.
func TestJSRetryWrapsTheWholeStep(t *testing.T) {
	files, err := JS{}.Generate(parse(t, "x.art", `scenario "x" {
  step "s" {
    get "https://example.test/"
    retry { times = 3, delay = "250ms" }
    expect status == 200
    capture id = raw
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	module := files[0].Content
	if want := "await art_retry({ times: 3, delay: 0.25 }, async () => {"; !strings.Contains(module, want) {
		t.Errorf("the module does not open the retry with %q:\n%s", want, module)
	}
	for _, want := range []string{
		"    resp = await fetch(",
		"    expect(status === 200, 'status == 200').toBe(true);",
		"    id = raw;",
		"  });",
	} {
		if !strings.Contains(module, want) {
			t.Errorf("the module does not hold %q inside the callback:\n%s", want, module)
		}
	}
}

// A scenario's page is opened with only the settings the scenario wrote, so
// art_browser's own defaults apply and "the scenario said nothing" stays
// different from "the scenario said false".
func TestJSOpensThePageWithWhatTheScenarioWrote(t *testing.T) {
	for _, c := range []struct{ config, want string }{
		{"", "const page = await art_browser();"},
		{"config browser {\n    headless = false\n  }\n",
			"const page = await art_browser({ headless: false });"},
		{"config browser {\n    viewport = \"800x600\"\n  }\n",
			"const page = await art_browser({ viewport: \"800x600\" });"},
		{"config browser {\n    headless = true\n    viewport = \"800x600\"\n  }\n",
			"const page = await art_browser({ headless: true, viewport: \"800x600\" });"},
	} {
		files, err := JS{}.Generate(parse(t, "x.art", `scenario "x" {
  `+c.config+`
  step "s" {
    browser {
      goto "https://example.test/"
    }
    expect page.title == "Home"
  }
}
`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(files[0].Content, c.want) {
			t.Errorf("the module does not hold %q:\n%s", c.want, files[0].Content)
		}
	}
}

// A query is a list of pairs and not an object, because source order is kept and
// a repeated `query` keeps both values -- which an object would collapse.
func TestJSKeepsRepeatedQueryParameters(t *testing.T) {
	files, err := JS{}.Generate(parse(t, "x.art", `scenario "x" {
  step "s" {
    get "https://example.test/orders" {
      query "fields" = "id"
      query "fields" = "sku"
    }
    expect status == 200
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	want := `art_url("https://example.test/orders", [["fields", "id"], ["fields", "sku"]])`
	if !strings.Contains(files[0].Content, want) {
		t.Errorf("the module does not hold %s:\n%s", want, files[0].Content)
	}
}

// An `env { ... }` setting's name is an identifier in the .art file and not
// necessarily one in JavaScript, so it is quoted -- and a step's env is layered
// over the process environment rather than replacing it, which is what the
// interpreter does.
func TestJSLayersAStepsEnvironment(t *testing.T) {
	files, err := JS{}.Generate(parse(t, "x.art", `scenario "x" {
  step "s" {
    run "printenv" {
      env {
        PGPASSWORD = "secret"
      }
    }
    expect exit_code == 0
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `env: { ...process.env, "PGPASSWORD": "secret" }`; !strings.Contains(files[0].Content, want) {
		t.Errorf("the module does not hold %s:\n%s", want, files[0].Content)
	}
}

// A `.art` construct the target cannot export is an error naming the step and
// the line, never emitted code that will not parse. The two shapes that can reach
// a target unchecked -- `artemis ast --from-json` decodes a tree nothing checked
// -- are a step with no action and a Bad node in a value position.
func TestJSRefusesAStepWithNoAction(t *testing.T) {
	tree := &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
		Name: token.Token{Kind: token.String, Value: "x"},
		Body: []ast.Decl{&ast.StepDecl{Name: token.Token{Kind: token.String, Value: "nothing to do"}}},
	}}}
	_, err := JS{}.Generate(tree)
	if err == nil {
		t.Fatal("Generate accepted a step with no action")
	}
	if !strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("Generate = %q, want it to name the step", err)
	}
}

func TestJSRefusesABadExpression(t *testing.T) {
	tree := &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
		Name: token.Token{Kind: token.String, Value: "x"},
		Body: []ast.Decl{&ast.StepDecl{
			Name: token.Token{Kind: token.String, Value: "broken"},
			Action: &ast.Request{
				Method: token.Token{Kind: token.Ident, Value: "get"},
				URL:    &ast.Bad{Toks: []token.Token{{Kind: token.Ident, Value: "?"}}},
			},
		}},
	}}}
	if _, err := (JS{}).Generate(tree); err == nil {
		t.Fatal("Generate accepted a Bad node in a value position")
	}
}

// --lang=javascript is what a person types, and answering it with a
// did-you-mean against the names that do exist would be unkind for no reason.
// It is not in Names(), so --lang's help lists one name per target.
func TestLookupAcceptsTheJavascriptAlias(t *testing.T) {
	for _, name := range []string{"javascript", "JavaScript", " javascript "} {
		target, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q) = %v, want the js target", name, err)
		}
		if target.Name() != "js" {
			t.Errorf("Lookup(%q).Name() = %q, want js", name, target.Name())
		}
	}
	for _, name := range Names() {
		if name == "javascript" {
			t.Error("javascript is in Names(); it is an alias, and --lang's help lists one name per target")
		}
	}
}

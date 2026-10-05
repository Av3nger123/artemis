package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/token"
)

// emitJS is one expression through the whole front end and out as JavaScript, in
// the position an `expect` puts it: the shortest route from a line of .art to the
// line of JavaScript a reader would see.
func emitJS(t *testing.T, expr string) string {
	t.Helper()
	tree := parse(t, "x.art", `scenario "x" {
  step "s" {
    get "https://example.test/"
    expect `+expr+`
  }
}
`)
	files, err := JS{}.Generate(tree)
	if err != nil {
		t.Fatalf("%s: Generate: %v", expr, err)
	}
	for _, line := range strings.Split(files[0].Content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "expect(") {
			return line
		}
	}
	t.Fatalf("%s: the module has no assertion:\n%s", expr, files[0].Content)
	return ""
}

// assertion is the emitted expression without the assertion wrapped around it,
// so a table of expression shapes reads as one.
func assertion(t *testing.T, expr string) string {
	t.Helper()
	line := emitJS(t, expr)
	inner := strings.TrimPrefix(line, "expect(")
	// The message is the .art source, single-quoted, and the expression is
	// everything before it.
	if i := strings.LastIndex(inner, ", '"); i >= 0 {
		return inner[:i]
	}
	t.Fatalf("%s: %q carries no source message", expr, line)
	return ""
}

// The table a reviewer reads: one .art expression, one line of JavaScript.
//
// Not a golden, because the point of each row is a *rule* rather than a file --
// `===` for a scalar literal and art_eq otherwise, the parentheses a DSL `not`
// over a comparison needs, art_at folding a path, and an `await` in front of
// each element read and nowhere else.
func TestJSExpressions(t *testing.T) {
	for _, c := range []struct{ art, want string }{
		{`status == 200`, `status === 200`},
		{`status != 500`, `status !== 500`},
		{`status >= 200 and status < 400`, `status >= 200 && status < 400`},
		{`status == 404 or status == 200`, `status === 404 || status === 200`},
		{`not status == 404`, `!(status === 404)`},
		{`not (status == 404)`, `!(status === 404)`},
		{`(status == 200 or status == 201) and status != 204`,
			`(status === 200 || status === 201) && status !== 204`},
		{`body.count > -1`, `art_at(body, "count") > -1`},
		{`body.meta.owner.name == "alice"`, `art_at(body, "meta", "owner", "name") === "alice"`},
		{`body.tags[0] == "urgent"`, `art_at(body, "tags", 0) === "urgent"`},
		{`headers["Content-Type"] contains "json"`,
			`art_contains(art_at(headers, "Content-Type"), "json")`},
		{`body.owner exists`, `await art_exists(() => art_at(body, "owner"))`},
		{`body.gap is null`, `art_is(art_at(body, "gap"), "null")`},
		{`raw matches /ORD-[0-9]+/`, `art_matches(raw, /ORD-[0-9]+/)`},
		{`body.tags == body.want`, `art_eq(art_at(body, "tags"), art_at(body, "want"))`},
		{`body.tags != body.want`, `!art_eq(art_at(body, "tags"), art_at(body, "want"))`},
		{`body.tags == ["a"]`, `art_eq(art_at(body, "tags"), ["a"])`},
		{`raw == "${status} ok"`, "raw === `${art_render(status)} ok`"},
		{`env("HOME") == "/root"`, `art_env("HOME") === "/root"`},
		{`match(raw, /(\d+)/) == "7"`, `art_match(raw, /(\d+)/) === "7"`},
	} {
		if got := assertion(t, c.art); got != c.want {
			t.Errorf("expect %s\n got: %s\nwant: %s", c.art, got, c.want)
		}
	}
}

// An element read is a promise, so the `await` goes in front of the read and the
// synchronous helper around it needs none -- and a thunk that holds one has to be
// async or it is a syntax error.
func TestJSAwaitsOnlyTheAsynchronousReads(t *testing.T) {
	tree := parse(t, "b.art", `scenario "x" {
  var want = "pro"

  step "s" {
    browser {
      goto "https://example.test/"
    }

    expect text(".x") contains want
    expect value("#q") == want
    expect count(".row") > 0
    expect attr("#a", "href") == want
    expect text(".x") exists
    expect page.title == want
  }
}
`)
	files, err := JS{}.Generate(tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`art_contains(await art_text(page, ".x"), want)`,
		`art_eq(await art_value(page, "#q"), want)`,
		`await page.locator(".row").count() > 0`,
		`art_eq(await art_attr(page, "#a", "href"), want)`,
		`await art_exists(async () => await art_text(page, ".x"))`,
		`art_eq(await page.title(), want)`,
		// And the settle loop's predicate is async wherever its body awaits.
		`art_within(async () =>`,
	} {
		if !strings.Contains(files[0].Content, want) {
			t.Errorf("the module does not hold %s:\n%s", want, files[0].Content)
		}
	}
}

// A browser assertion whose shape has a Playwright matcher gets the matcher, and
// one that does not gets the settle loop. The set is the Python target's, one for
// one, which is what "Playwright on all three sides" means in practice.
func TestJSNativeMatchers(t *testing.T) {
	tree := parse(t, "b.art", `scenario "x" {
  step "s" {
    browser {
      goto "https://example.test/"
    }

    expect visible(".modal")
    expect not visible(".spinner")
    expect value("#q") == "pro"
    expect count(".row") == 3
    expect attr("#a", "href") == "/x"
    expect page.url == "https://example.test/"
    expect page.title == "Home"
    expect text(".x") contains "y"
    expect text(".x") == "exactly" within "2s"
  }
}
`)
	files, err := JS{}.Generate(tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`await expect(page.locator(".modal")).toBeVisible({ timeout: 5000 })`,
		`await expect(page.locator(".spinner")).toBeHidden({ timeout: 5000 })`,
		`await expect(page.locator("#q")).toHaveValue("pro", { timeout: 5000 })`,
		`await expect(page.locator(".row")).toHaveCount(3, { timeout: 5000 })`,
		`await expect(page.locator("#a")).toHaveAttribute("href", "/x", { timeout: 5000 })`,
		`await expect(page).toHaveURL("https://example.test/", { timeout: 5000 })`,
		`await expect(page).toHaveTitle("Home", { timeout: 5000 })`,
		`await expect(page.locator(".x")).toContainText("y", { timeout: 5000 })`,
		// toHaveText is not in the set, because it normalises whitespace: an
		// equality against text() falls back to the settle loop, which is exact.
		`await art_within(async () => await art_text(page, ".x") === "exactly", 2.0)`,
	} {
		if !strings.Contains(files[0].Content, want) {
			t.Errorf("the module does not hold %s:\n%s", want, files[0].Content)
		}
	}
	if strings.Contains(files[0].Content, "toHaveText") {
		t.Error("toHaveText was emitted; it normalises whitespace and the interpreter does not")
	}
}

// The two targets must map the same shapes, or a `within` that waits on one side
// polls on the other and nobody finds out. The matchers' names differ; the number
// of them cannot.
func TestBothTargetsMapTheSameNativeShapes(t *testing.T) {
	shapes := []string{
		`visible(".x")`, `not visible(".x")`, `value("#q") == "a"`,
		`count(".x") == 1`, `attr("#a", "href") == "/x"`,
		`page.url == "https://example.test/"`, `page.title == "Home"`,
		`text(".x") contains "y"`,
		// And three that neither maps.
		`text(".x") == "a"`, `text(".x") is null`, `count(".x") > 0`,
	}
	for _, shape := range shapes {
		tree := parse(t, "b.art", `scenario "x" {
  step "s" {
    browser {
      goto "https://example.test/"
    }

    expect `+shape+`
  }
}
`)
		py, err := Python{}.Generate(tree)
		if err != nil {
			t.Fatalf("%s: python: %v", shape, err)
		}
		js, err := JS{}.Generate(tree)
		if err != nil {
			t.Fatalf("%s: js: %v", shape, err)
		}
		pyNative := strings.Contains(py[0].Content, "expect(page")
		jsNative := strings.Contains(js[0].Content, "await expect(page")
		if pyNative != jsNative {
			t.Errorf("expect %s: python uses a playwright matcher: %v, javascript: %v",
				shape, pyNative, jsNative)
		}
	}
}

// Go's regexp is RE2 and JavaScript's is not the same language. The three
// differences that matter are handled rather than emitted as a module that
// throws on import.
func TestJSPattern(t *testing.T) {
	for _, c := range []struct{ pattern, want string }{
		{`ORD-[0-9]+`, `/ORD-[0-9]+/`},
		{`a/b`, `/a\/b/`},
		{`a\/b`, `/a\/b/`},
		{`(?P<id>[0-9]+)`, `/(?<id>[0-9]+)/`},
		{`(?:a|b)`, `/(?:a|b)/`},
		{`\p{L}+`, `/\p{L}+/u`},
		{``, `new RegExp("", "")`},
	} {
		got, err := jsPattern(c.pattern, 1)
		if err != nil {
			t.Errorf("jsPattern(%q) = %v", c.pattern, err)
			continue
		}
		if got != c.want {
			t.Errorf("jsPattern(%q) = %s, want %s", c.pattern, got, c.want)
		}
	}
}

// An inline flag group is refused with its offset, because JavaScript has no
// spelling for one anywhere -- not even at the start of a pattern, which is the
// one place Python accepts it.
func TestJSPatternRefusesAnInlineFlagGroup(t *testing.T) {
	for _, pattern := range []string{`(?i)alice`, `a(?i)b`, `(?is)x`} {
		if _, err := jsPattern(pattern, 7); err == nil {
			t.Errorf("jsPattern(%q) was accepted; javascript has no inline flag group", pattern)
		} else if !strings.Contains(err.Error(), "line 7") {
			t.Errorf("jsPattern(%q) = %q, want it to name the line", pattern, err)
		}
	}
	// And a group JavaScript does have is not refused.
	for _, pattern := range []string{`(?:a)`, `(?=a)`, `(?!a)`, `(?P<n>a)`} {
		if _, err := jsPattern(pattern, 1); err != nil {
			t.Errorf("jsPattern(%q) = %v, want it accepted", pattern, err)
		}
	}
}

// Every builtin the language has needs a JavaScript spelling, or a .art file
// that compiles produces a module that calls something undefined. The arity
// table is shared with the Python target, so this is about the switch.
func TestEveryBuiltinHasAJavaScriptSpelling(t *testing.T) {
	for _, name := range token.Builtins {
		if _, ok := builtinArity[name]; !ok {
			t.Errorf("%s() is a builtin with no arity; the js target cannot emit it", name)
		}
	}
	for name := range builtinArity {
		found := false
		for _, b := range token.Builtins {
			if b == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s() has an arity and is not a builtin", name)
		}
	}
}

// The escaping, for the three literal kinds the target writes.
func TestJSEscaping(t *testing.T) {
	if got, want := jsQuote(`a"b\c`+"\n"), `"a\"b\\c\n"`; got != want {
		t.Errorf("jsQuote = %s, want %s", got, want)
	}
	if got, want := jsQuoteSingle(`it's "x"`), `'it\'s "x"'`; got != want {
		t.Errorf("jsQuoteSingle = %s, want %s", got, want)
	}
	// A backtick and a `${` in the literal text of a template literal would end
	// it and open an interpolation, so both are escaped.
	if got, want := escapeTemplate("a`b${c}"), "a\\`b\\${c}"; got != want {
		t.Errorf("escapeTemplate = %s, want %s", got, want)
	}
}

// The assertion's message is the .art source, which is what makes a vitest
// failure name the expression the way the interpreter's report does -- and it has
// to be the tokens' own text, because ast.Source' trivia would carry a comment
// and a newline into the middle of a string literal.
func TestJSAssertionMessageIsTheSource(t *testing.T) {
	line := emitJS(t, `status == 200 # the happy path`)
	if want := `, 'status == 200')`; !strings.Contains(line, want) {
		t.Errorf("the assertion is %s, want it to carry %s", line, want)
	}
	if strings.Contains(line, "happy path") {
		t.Errorf("the assertion carries the trailing comment: %s", line)
	}
}

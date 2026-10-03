package migrate

import (
	"encoding/json"
	"strings"
	"testing"

	"artemis/pkg/dsl/parser"
)

// parses reports whether src is a whole .art file the parser accepts. Every
// expression this package emits goes through it, because "it looked right" is
// not a test of a code generator.
func parses(t *testing.T, src string) {
	t.Helper()
	if _, bag := parser.Parse("t.art", src); bag.HasErrors() {
		t.Fatalf("does not parse: %v\n%s", bag.All(), src)
	}
}

// inExpect wraps an expression where an expect's subject goes, so a path is
// checked by the real grammar rather than by eye.
func inExpect(expr string) string {
	return "scenario \"t\" {\n  step \"s\" {\n    get \"u\"\n    expect " + expr + " exists\n  }\n}\n"
}

// inValue wraps an expression where an expect's expected value goes.
func inValue(expr string) string {
	return "scenario \"t\" {\n  step \"s\" {\n    get \"u\"\n    expect body.x == " + expr + "\n  }\n}\n"
}

func TestPathExpr(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"$", "body"},
		{"$.id", "body.id"},
		{"$.data.access_token", "body.data.access_token"},
		{"$.items[0].sku", "body.items[0].sku"},
		{"$[0]", "body[0]"},
		{"$['a-b']", `body["a-b"]`},
		{`$["a b"]`, `body["a b"]`},
		{"$.a-b", `body["a-b"]`},
		{"$.2fa", `body["2fa"]`},
		{"$.if", `body["if"]`},
		{"  $.id  ", "body.id"},
	}
	for _, c := range cases {
		got, err := pathExpr("body", c.path)
		if err != nil {
			t.Errorf("pathExpr(%q) = error %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("pathExpr(%q) = %q, want %q", c.path, got, c.want)
		}
		parses(t, inExpect(got))
	}
}

// Every path shape that can match more than one value is refused, because an
// expect reads one value and there is no expression that means "the first of
// whatever matched".
func TestPathExprRefusesWhatTheDSLCannotSay(t *testing.T) {
	cases := []struct{ path, mentions string }{
		{"$..name", "recursive descent"},
		{"$.items[*]", "wildcard"},
		{"$.*", "wildcard"},
		{"$.items[?(@.x > 1)]", "filter"},
		{"$.items[0:2]", "slice"},
		{"$.items[0,1]", "union"},
		{"", "empty"},
		{"id", "does not start with $"},
		{"$.", "ends in a ."},
		{"$.a[", "with no ]"},
		{"$.a[]", "empty []"},
		{"$.a[x]", "neither an index nor a quoted name"},
	}
	for _, c := range cases {
		_, err := pathExpr("body", c.path)
		if err == nil {
			t.Errorf("pathExpr(%q) = nil error, want one", c.path)
			continue
		}
		if !strings.Contains(err.Error(), c.mentions) {
			t.Errorf("pathExpr(%q) = %q, want it to mention %q", c.path, err, c.mentions)
		}
	}
}

func TestPathExprRootIsTheStepTypes(t *testing.T) {
	got, err := pathExpr("stdout", "$.build")
	if err != nil {
		t.Fatal(err)
	}
	if want := "stdout.build"; got != want {
		t.Errorf("pathExpr(stdout, $.build) = %q, want %q", got, want)
	}
}

// An assertion's value is never templated by the YAML runtime, so a {{x}} in
// one is four characters it compared against and has to stay four characters.
func TestPlainLeavesPlaceholdersAlone(t *testing.T) {
	got, err := plain("{{token}}")
	if err != nil {
		t.Fatal(err)
	}
	// `{{` is not `${`: nothing in a DSL string needs escaping for it, and the
	// migrated check compares against the same four characters.
	if want := `"{{token}}"`; got != want {
		t.Errorf("plain({{token}}) = %q, want %q", got, want)
	}
	parses(t, inValue(got))
}

func TestPlainLiterals(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{true, "true"},
		{false, "false"},
		{200, "200"},
		{int64(-7), "-7"},
		{2.5, "2.5"},
		{42.0, "42"},
		{"widget", `"widget"`},
		{json.Number("12345678901234567890"), "12345678901234567890"},
		{[]any{"a", "b"}, `["a", "b"]`},
		{[]any{}, "[]"},
		{map[string]any{"b": 1, "a": "x"}, `{"a": "x", "b": 1}`},
		{map[any]any{1: "x"}, `{"1": "x"}`},
		{[]any{map[string]any{"a": []any{1, 2}}}, `[{"a": [1, 2]}]`},
	}
	for _, c := range cases {
		got, err := plain(c.v)
		if err != nil {
			t.Errorf("plain(%#v) = error %v", c.v, err)
			continue
		}
		if got != c.want {
			t.Errorf("plain(%#v) = %q, want %q", c.v, got, c.want)
		}
		parses(t, inValue(got))
	}
}

// A number JSON permits and the DSL's grammar does not is reported rather than
// emitted, so migration never writes a file the lexer reads as two tokens.
func TestPlainRefusesANumberTheGrammarCannotWrite(t *testing.T) {
	if _, err := plain(json.Number("1e")); err == nil {
		t.Error("plain(1e) = nil error, want one")
	}
	if _, err := plain(json.Number("0x1f")); err == nil {
		t.Error("plain(0x1f) = nil error, want one")
	}
	if got, err := plain(json.Number("1.5e-7")); err != nil || got != "1.5e-7" {
		t.Errorf("plain(1.5e-7) = %q, %v, want 1.5e-7", got, err)
	}
}

func TestTemplated(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"%SERVER%/ping", `"%SERVER%/ping"`},
		{"{{base}}/items", `"${base}/items"`},
		{"{{base}}/items/{{itemId}}", `"${base}/items/${itemId}"`},
		{"{{ base }}", `"${base}"`},
		{"{{env.API_URL}}", `env("API_URL")`},
		{"{{env.HOST}}/v1", `"${env("HOST")}/v1"`},
		{"a } b { c", `"a } b { c"`},
		{"literal ${x}", `"literal \${x}"`},
		{`a "quoted" \ one`, `"a \"quoted\" \\ one"`},
		{"two\nlines\tover", `"two\nlines\tover"`},
	}
	for _, c := range cases {
		got, err := templated(c.in)
		if err != nil {
			t.Errorf("templated(%q) = error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("templated(%q) = %q, want %q", c.in, got, c.want)
		}
		parses(t, "scenario \"t\" {\n  step \"s\" { get "+got+" }\n}\n")
	}
}

// The placeholders TransformText itself rejects are rejected here: the file
// never ran, so there is no behaviour to preserve and guessing would invent one.
func TestTemplatedRefusesPlaceholdersThatHaveNoName(t *testing.T) {
	for _, in := range []string{"{{unclosed", "{{}}", "{{a.b}}", "{{a b}}", "{{if}}", "{{env.}}"} {
		if _, err := templated(in); err == nil {
			t.Errorf("templated(%q) = nil error, want one", in)
		}
	}
}

func TestRegexLiteral(t *testing.T) {
	cases := []struct{ in, want string }{
		{"req-([0-9a-f]+)", "/req-([0-9a-f]+)/"},
		{"/items/([0-9]+)", `/\/items\/([0-9]+)/`},
		{`\d+`, `/\d+/`},
		{`a\/b`, `/a\/b/`},
	}
	for _, c := range cases {
		got, err := regexLiteral(c.in)
		if err != nil {
			t.Errorf("regexLiteral(%q) = error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("regexLiteral(%q) = %q, want %q", c.in, got, c.want)
		}
		parses(t, "scenario \"t\" {\n  step \"s\" {\n    get \"u\"\n    capture x = match(raw, "+got+")\n  }\n}\n")
	}
}

// A /.../ literal cannot cross a line, so a pattern with a newline in it has no
// literal form and is reported instead of being written and then failing to lex.
func TestRegexLiteralRefusesALineBreak(t *testing.T) {
	if _, err := regexLiteral("a\nb"); err == nil {
		t.Error("regexLiteral(a\\nb) = nil error, want one")
	}
}

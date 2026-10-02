package parser

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// Spans are the front end's most load-bearing data after the tokens
// themselves: a diagnostic underlines a span, the tree's JSON encoding
// serialises one per node, and a UI underlines a bad `${` from one. So they
// are asserted exactly -- all six fields -- rather than approximately.
//
// The convention under test, set by pkg/dsl/token: Line and Col are 1-based
// and Col counts bytes; EndLine and EndCol are exclusive, the position just
// past the last byte; Offset is a 0-based byte offset. A node's span runs from
// its first token to its last, and leading trivia belongs to the token, not to
// the span.

// spanOfSource is the span a node ought to have if it covers exactly the
// substring want of src: found by locating want rather than by counting, so a
// test reads as "this node covers this text".
func spanOfSource(t *testing.T, src, want string) token.Span {
	t.Helper()
	off := strings.Index(src, want)
	if off < 0 {
		t.Fatalf("%q is not in the source", want)
	}
	line, col := 1, 1
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	endLine, endCol := line, col
	for i := 0; i < len(want); i++ {
		if want[i] == '\n' {
			endLine++
			endCol = 1
		} else {
			endCol++
		}
	}
	return token.Span{
		File: "t.art", Line: line, Col: col,
		EndLine: endLine, EndCol: endCol, Offset: off,
	}
}

// TestNodeSpansCoverTheirSource walks one file and asserts the span of a node
// from every production, each against the exact text it should cover.
func TestNodeSpansCoverTheirSource(t *testing.T) {
	src := `scenario "checkout" {
  config browser { headless = true }
  var url = env("API_URL")
  step "orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer x"
      body = {"a": [1, 2]}
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }
    expect body.data.count > 0
    expect not body.x exists
    expect page.url contains "/x" within "10s"
    capture token = body.data.access_token
  }
}
`
	f := parseOK(t, src)

	// Every node type the file contains, found by walking and matched to the
	// text it should cover. One table rather than one test per node, because
	// the assertion is identical and the point is the coverage.
	cases := []struct {
		what string
		find func(ast.Node) bool
		text string
	}{
		{"file", is[*ast.File], src},
		{"scenario", is[*ast.Scenario], strings.TrimRight(src, "\n")},
		{"config", is[*ast.ConfigDecl], "config browser { headless = true }"},
		{"var", is[*ast.VarDecl], `var url = env("API_URL")`},
		{"call", is[*ast.Call], `env("API_URL")`},
		{"step", is[*ast.StepDecl], "step \"orders\" {\n    get \"${url}/orders\" {\n      header \"Authorization\" = \"Bearer x\"\n      body = {\"a\": [1, 2]}\n    }\n    timeout = \"5s\"\n    retry { times = 3, delay = \"2s\" }\n    expect body.data.count > 0\n    expect not body.x exists\n    expect page.url contains \"/x\" within \"10s\"\n    capture token = body.data.access_token\n  }"},
		{"request", is[*ast.Request], "get \"${url}/orders\" {\n      header \"Authorization\" = \"Bearer x\"\n      body = {\"a\": [1, 2]}\n    }"},
		{"interp", is[*ast.Interp], `"${url}/orders"`},
		{"object", is[*ast.Object], `{"a": [1, 2]}`},
		{"array", is[*ast.Array], "[1, 2]"},
		{"capture", is[*ast.Capture], "capture token = body.data.access_token"},
		{"member", is[*ast.Member], "body.data.count"},
		{"exists", is[*ast.Exists], "body.x exists"},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			n := find(f, c.find)
			if n == nil {
				t.Fatalf("no %s in the tree", c.what)
			}
			want := spanOfSource(t, src, c.text)
			if got := n.Span(); got != want {
				t.Errorf("span = %+v\n        want %+v\n(covers %q)", got, want, ast.Source(n))
			}
		})
	}
}

// TestSpanExcludesLeadingTrivia is the rule that makes a caret land under the
// right column: a node's span starts at its first token's text, not at the
// indentation in front of it.
func TestSpanExcludesLeadingTrivia(t *testing.T) {
	src := "scenario \"s\" {\n      var x = 1\n}\n"
	f := parseOK(t, src)

	v := find(f, is[*ast.VarDecl])
	got := v.Span()
	if got.Line != 2 || got.Col != 7 {
		t.Errorf("span starts at %d:%d, want 2:7 -- after the six spaces", got.Line, got.Col)
	}
	if got.EndCol != 16 {
		t.Errorf("span ends at col %d, want 16 (exclusive, past the \"1\")", got.EndCol)
	}
	if got.Offset != 21 {
		t.Errorf("offset = %d, want 21 -- 15 bytes of first line plus six spaces", got.Offset)
	}
}

// TestSpanOfMultiByteColumn pins the byte-counting convention token.Span
// documents: a multi-byte rune advances the column by its length, like
// go/token. A test is the only thing that stops this quietly becoming runes.
func TestSpanOfMultiByteColumn(t *testing.T) {
	src := "scenario \"s\" {\n  var x = \"é\"\n  var y = 2\n}\n"
	f := parseOK(t, src)

	var decls []*ast.VarDecl
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.VarDecl); ok {
			decls = append(decls, v)
		}
	})
	if len(decls) != 2 {
		t.Fatalf("vars = %d, want 2", len(decls))
	}

	// "é" is two bytes, so the string literal runs col 11 to col 15: a quote,
	// two bytes, a quote.
	first := decls[0].Span()
	if first.EndCol != 15 {
		t.Errorf("first var ends at col %d, want 15 (é counts two)", first.EndCol)
	}
	// The next line starts over at 1 regardless.
	if second := decls[1].Span(); second.Line != 3 || second.Col != 3 {
		t.Errorf("second var starts at %d:%d, want 3:3", second.Line, second.Col)
	}
}

// TestSpansOfEveryExpressionLevel asserts a span on one node from each
// precedence level, since a level that joined the wrong tokens would still
// produce the right shape.
func TestSpansOfEveryExpressionLevel(t *testing.T) {
	cases := []struct {
		src  string
		find func(ast.Node) bool
		text string
	}{
		{"a or b", is[*ast.Binary], "a or b"},
		{"a and b", is[*ast.Binary], "a and b"},
		{"not a", is[*ast.Unary], "not a"},
		{"a == 1", is[*ast.Binary], "a == 1"},
		{"a exists", is[*ast.Exists], "a exists"},
		{"a is number", is[*ast.IsType], "a is number"},
		{"-a", is[*ast.Unary], "-a"},
		{"a.b", is[*ast.Member], "a.b"},
		{"a[0]", is[*ast.Index], "a[0]"},
		{"f(1)", is[*ast.Call], "f(1)"},
		{"(a)", is[*ast.Paren], "(a)"},
		{"[1]", is[*ast.Array], "[1]"},
		{`{"k": 1}`, is[*ast.Object], `{"k": 1}`},
		{`"${a}b"`, is[*ast.Interp], `"${a}b"`},
		{"1", is[*ast.Literal], "1"},
		{"ab", is[*ast.Ident], "ab"},
	}

	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			x := parseExprOK(t, c.src)
			n := find(x, c.find)
			if n == nil {
				t.Fatalf("node not found in %q", c.src)
			}
			if got, want := n.Span(), spanOfSource(t, c.src, c.text); got != want {
				t.Errorf("span = %+v, want %+v", got, want)
			}
		})
	}
}

// TestDiagnosticSpanIsTheOffendingToken: the design's bar is "spans cover the
// offending token exactly", because a caret under the wrong word is worse than
// no caret.
func TestDiagnosticSpanIsTheOffendingToken(t *testing.T) {
	src := "scenario \"s\" {\n  step \"t\" {\n    run \"x\"\n    expect statu ==\n  }\n}\n"
	_, bag := Parse("t.art", src)

	all := bag.All()
	if len(all) == 0 {
		t.Fatal("no diagnostic for an expect with no right-hand side")
	}
	// The `==` has nothing after it, so the diagnostic points at what follows
	// -- the `}` on the next line -- not at the start of the statement.
	d := all[0]
	if d.Span.Line != 5 || d.Span.Col != 3 {
		t.Errorf("diagnostic at %d:%d, want 5:3 (the \"}\")", d.Span.Line, d.Span.Col)
	}
	if d.Span.EndCol != 4 {
		t.Errorf("end col = %d, want 4: a one-byte token", d.Span.EndCol)
	}
}

// is builds a predicate matching one node type, so a span table reads as a
// list of productions.
func is[T ast.Node](n ast.Node) bool {
	_, ok := n.(T)
	return ok
}

// find is the first node in source order satisfying pred.
func find(root ast.Node, pred func(ast.Node) bool) ast.Node {
	var found ast.Node
	ast.Walk(root, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if pred(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

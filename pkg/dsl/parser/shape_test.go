package parser

import (
	"fmt"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// shape renders a node as a parenthesised form, for tests that are about the
// *structure* the parser chose rather than the text it read.
//
// Precedence is the reason this exists. `expect not body.x exists` is right or
// wrong depending on which of two trees it produces, and a test that asserts
// `(not (exists body.x))` says so in one line, where reaching through
// X.(*ast.Unary).X.(*ast.Exists) says it in five and reads like neither.
func shape(n ast.Node) string {
	switch n := n.(type) {
	case nil:
		return "nil"
	case *ast.File:
		return join("file", nodes(n.Scenarios)...)
	case *ast.Scenario:
		return join("scenario "+n.Name.Text, nodes(n.Body)...)
	case *ast.ConfigDecl:
		return join("config "+n.Subject.Value, shape(n.Block))
	case *ast.VarDecl:
		return join("var "+n.Name.Value, shape(n.Value))
	case *ast.StepDecl:
		parts := make([]string, 0, len(n.Body)+1)
		for _, it := range ast.Children(n) {
			parts = append(parts, shape(it))
		}
		return join("step "+n.Name.Text, parts...)
	case *ast.Request:
		return join("request "+n.Method.Value, optional(n.URL), optionalBlock(n.Block))
	case *ast.Run:
		return join("run", optional(n.Command), optionalBlock(n.Block))
	case *ast.Browser:
		return join("browser", nodes(n.Acts)...)
	case *ast.BrowserAct:
		return join(n.Name.Value, optional(n.Target), optional(n.Value))
	case *ast.Block:
		return join("block", nodes(n.Fields)...)
	case *ast.Field:
		head := "field " + n.Name.Value
		if n.Key != nil {
			head += " key=" + shape(n.Key)
		}
		if n.Block != nil {
			return join(head, shape(n.Block))
		}
		return join(head, optional(n.Value))
	case *ast.Expect:
		head := "expect"
		if !n.Within.Span.IsZero() {
			head += " within=" + shape(n.Budget)
		}
		return join(head, optional(n.Value))
	case *ast.Capture:
		return join("capture "+n.Name.Value, optional(n.Value))
	case *ast.Binary:
		return join(n.Op.Text, shape(n.X), shape(n.Y))
	case *ast.Unary:
		return join(n.Op.Text, shape(n.X))
	case *ast.Exists:
		return join("exists", shape(n.X))
	case *ast.IsType:
		return join("is "+n.Type.Value, shape(n.X))
	case *ast.Member:
		return fmt.Sprintf("%s.%s", shape(n.X), n.Name.Value)
	case *ast.Index:
		return fmt.Sprintf("%s[%s]", shape(n.X), shape(n.Index))
	case *ast.Call:
		parts := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			parts = append(parts, shape(a.Value))
		}
		return join("call "+n.Callee.Name(), parts...)
	case *ast.Object:
		parts := make([]string, 0, len(n.Entries))
		for _, e := range n.Entries {
			parts = append(parts, join("entry", shape(e.Key), optional(e.Value)))
		}
		return join("object", parts...)
	case *ast.Array:
		parts := make([]string, 0, len(n.Elems))
		for _, e := range n.Elems {
			parts = append(parts, shape(e.Value))
		}
		return join("array", parts...)
	case *ast.Paren:
		return join("paren", optional(n.X))
	case *ast.Interp:
		parts := make([]string, 0, len(n.Segments))
		for _, s := range n.Segments {
			parts = append(parts, shape(s.Expr))
		}
		return join("interp", parts...)
	case *ast.Ident:
		return n.Tok.Value
	case *ast.Literal:
		return n.Tok.Text
	case *ast.Bad:
		return "bad"
	}
	return fmt.Sprintf("?%T", n)
}

func join(head string, parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return "(" + head + ")"
	}
	return "(" + head + " " + strings.Join(kept, " ") + ")"
}

// optional renders a child that a recovered parse may have left nil, so a
// shape assertion on a broken file is readable rather than a panic.
func optional(n ast.Expr) string {
	if n == nil {
		return ""
	}
	return shape(n)
}

func optionalBlock(b *ast.Block) string {
	if b == nil {
		return ""
	}
	return shape(b)
}

func nodes[T ast.Node](in []T) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		out = append(out, shape(n))
	}
	return out
}

// parseExprOK parses src as one expression and fails the test on any
// diagnostic, for the cases that are about a valid parse.
func parseExprOK(t *testing.T, src string) ast.Expr {
	t.Helper()
	x, bag := ParseExpr("t.art", src)
	assertNoDiagnostics(t, src, bag)
	return x
}

// parseOK parses src as a file and fails the test on any diagnostic.
func parseOK(t *testing.T, src string) *ast.File {
	t.Helper()
	f, bag := Parse("t.art", src)
	assertNoDiagnostics(t, src, bag)
	assertRoundTrip(t, src, f)
	return f
}

func assertNoDiagnostics(t *testing.T, src string, bag *diag.Bag) {
	t.Helper()
	for _, d := range bag.All() {
		t.Errorf("unexpected diagnostic parsing %q:\n  %d:%d [%s] %s", src, d.Span.Line, d.Span.Col, d.Code, d.Message)
	}
}

// assertRoundTrip is R6 on one input: the tree holds every byte it was built
// from. Every valid-parse test asserts it, so a node type that forgets a token
// in Tokens fails many tests rather than one.
func assertRoundTrip(t *testing.T, src string, n ast.Node) {
	t.Helper()
	got := ast.Source(n)
	if got == src {
		return
	}
	for i := 0; i < len(got) && i < len(src); i++ {
		if got[i] != src[i] {
			t.Fatalf("round trip differs at byte %d:\n got %q\nwant %q", i, got[i:], src[i:])
		}
	}
	t.Fatalf("round trip differs in length: got %d bytes, want %d\n got %q\nwant %q", len(got), len(src), got, src)
}

// codes is the diagnostics' codes in file order, which is what a test about
// error reporting asserts on: the code is the stable contract and the message
// is free to be reworded.
func codes(bag *diag.Bag) []diag.Code {
	all := bag.All()
	out := make([]diag.Code, 0, len(all))
	for _, d := range all {
		out = append(out, d.Code)
	}
	return out
}

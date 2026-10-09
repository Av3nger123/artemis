package ast

import (
	"testing"

	"artemis/pkg/dsl/token"
)

func stampVia(t token.Token) token.Token { t.Span.Via = 7; return t }

func TestCloneIsDeepAndStampsEveryToken(t *testing.T) {
	orig := &Binary{X: &Ident{Tok: word("a")}, Op: word("=="), Y: &Literal{Tok: word("1")}}
	c := Clone(orig, stampVia)

	if c == orig || c.X == orig.X {
		t.Fatal("Clone shared a node with the original")
	}
	for _, k := range c.Tokens(nil) {
		if k.Span.Via != 7 {
			t.Fatalf("token %q not stamped", k.Text)
		}
	}
	for _, k := range orig.Tokens(nil) {
		if k.Span.Via != 0 {
			t.Fatal("Clone stamped the original")
		}
	}
	if Source(c) != Source(orig) {
		t.Fatalf("clone source %q != %q", Source(c), Source(orig))
	}
}

func TestCloneWorksOnInterfaceTypedValues(t *testing.T) {
	var e Expr = &Binary{X: &Ident{Tok: word("a")}, Op: word("=="), Y: &Literal{Tok: word("1")}}
	c := Clone(e, stampVia)
	if c == e {
		t.Fatal("Clone returned the same node")
	}
	for _, k := range c.Tokens(nil) {
		if k.Span.Via != 7 {
			t.Fatalf("token %q not stamped", k.Text)
		}
	}
	var nilE Expr
	if Clone(nilE, stampVia) != nil {
		t.Fatal("nil Expr should clone to nil")
	}
}

func TestCloneCopiesAUseDeclWithItsLines(t *testing.T) {
	u := &UseDecl{Keyword: word("use"), Item: word("login"), Lines: []Stmt{&Drop{Keyword: word("drop"), What: word("expects")}}}
	c := Clone(u, stampVia)
	if c.Lines[0] == u.Lines[0] {
		t.Fatal("lines shared")
	}
	if c.Lines[0].(*Drop).What.Span.Via != 7 || u.Lines[0].(*Drop).What.Span.Via != 0 {
		t.Fatal("stamp reached the wrong tree")
	}
}

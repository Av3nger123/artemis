package ast

import (
	"testing"

	"artemis/pkg/dsl/token"
)

func word(text string) token.Token { return token.Token{Kind: token.Ident, Text: text, Value: text} }

func TestUseDeclChildrenAreItsLinesInOrder(t *testing.T) {
	arg := &Field{Name: word("user"), Assign: word("="), Value: &Ident{Tok: word("u")}}
	drop := &Drop{Keyword: word("drop"), What: word("expects")}
	u := &UseDecl{Keyword: word("use"), Item: word("login"), Lines: []Stmt{arg, drop}}
	got := Children(u)
	if len(got) != 2 || got[0] != Node(arg) || got[1] != Node(drop) {
		t.Fatalf("Children(use) = %v, want [arg drop]", got)
	}
}

func TestUseRefNamesCollectionAndItem(t *testing.T) {
	u := &UseDecl{Collection: word("auth"), Dot: word("."), Item: word("login")}
	if u.Ref() != "auth.login" {
		t.Fatalf("Ref() = %q", u.Ref())
	}
	if (&UseDecl{Item: word("create")}).Ref() != "create" {
		t.Fatal("a bare ref is the item name")
	}
}

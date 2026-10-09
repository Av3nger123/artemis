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

func at(text, file string, off, via int) token.Token {
	return token.Token{Kind: token.Ident, Text: text, Value: text,
		Span: token.Span{File: file, Line: 1 + off, Col: 1, EndLine: 1 + off, EndCol: 2, Offset: off, Via: via}}
}

// A step pkg/dsl/expand builds mixes statements copied out of a collection
// with lines written at the use, in another file or copy. Offsets order only
// the statements from where the action came from; the rest follow in slice
// order.
func TestStepItemsOfMixedOriginKeepForeignOnesLast(t *testing.T) {
	action := &Request{Method: at("get", "c.art", 50, 1), URL: &Ident{Tok: at("u", "c.art", 54, 1)}}
	late := &Expect{Keyword: at("expect", "c.art", 80, 1), Value: &Ident{Tok: at("a", "c.art", 87, 1)}}
	early := &Capture{Keyword: at("capture", "c.art", 60, 1), Name: at("id", "c.art", 68, 1), Value: &Ident{Tok: at("b", "c.art", 73, 1)}}
	foreign1 := &Expect{Keyword: at("expect", "main.art", 10, 0), Value: &Ident{Tok: at("c", "main.art", 17, 0)}}
	otherCopy := &Expect{Keyword: at("expect", "c.art", 5, 2), Value: &Ident{Tok: at("d", "c.art", 12, 2)}}
	s := &StepDecl{Action: action, Body: []Stmt{late, foreign1, early, otherCopy}}
	got := Children(s)
	want := []Node{action, early, late, foreign1, otherCopy}
	if len(got) != len(want) {
		t.Fatalf("Children = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %T %v, want %T %v", i, got[i], got[i].Span(), want[i], want[i].Span())
		}
	}
}

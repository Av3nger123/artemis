package ast

import (
	"testing"

	"artemis/pkg/dsl/token"
)

// tok builds a token as the lexer would, with leading trivia, so a test can
// assemble a tree by hand and still assert on exact source. off is the byte
// offset of the token's text, after its leading trivia.
func tok(kind token.Kind, text string, line, col, off int, leading ...token.Trivia) token.Token {
	return token.Token{
		Kind: kind,
		Text: text,
		Span: token.Span{
			File: "t.art", Line: line, Col: col,
			EndLine: line, EndCol: col + len(text),
			Offset: off,
		},
		Leading: leading,
	}
}

func space(n int) token.Trivia {
	s := ""
	for i := 0; i < n; i++ {
		s += " "
	}
	return token.Trivia{Kind: token.Whitespace, Text: s}
}

func TestJoinSpansFirstToLast(t *testing.T) {
	a := token.Span{File: "t.art", Line: 1, Col: 1, EndLine: 1, EndCol: 4, Offset: 0}
	b := token.Span{File: "t.art", Line: 3, Col: 5, EndLine: 3, EndCol: 9, Offset: 20}

	got := Join(a, b)
	want := token.Span{File: "t.art", Line: 1, Col: 1, EndLine: 3, EndCol: 9, Offset: 0}
	if got != want {
		t.Fatalf("Join = %+v, want %+v", got, want)
	}

	// Argument order must not matter: a node built during recovery can hold
	// its tokens out of order, and a span that started after it ended would
	// make a diagnostic renderer slice backwards.
	if rev := Join(b, a); rev != want {
		t.Fatalf("Join reversed = %+v, want %+v", rev, want)
	}
}

func TestJoinIgnoresZeroSpans(t *testing.T) {
	// Optional tokens -- an absent `within`, a missing trailing comma -- are
	// zero, and a node holding one must not be given a span starting at
	// line 0.
	real := token.Span{File: "t.art", Line: 7, Col: 3, EndLine: 7, EndCol: 9, Offset: 42}
	if got := Join(token.Span{}, real, token.Span{}); got != real {
		t.Fatalf("Join with zeros = %+v, want %+v", got, real)
	}
	if got := Join(token.Span{}, token.Span{}); !got.IsZero() {
		t.Fatalf("Join of nothing = %+v, want zero", got)
	}
}

func TestSourceConcatenatesTriviaAndText(t *testing.T) {
	// `var x  = 1` -- two spaces before the `=`, which is the whole reason the
	// tree holds tokens rather than spans.
	v := &VarDecl{
		Keyword: tok(token.Ident, "var", 1, 1, 0),
		Name:    tok(token.Ident, "x", 1, 5, 4, space(1)),
		Assign:  tok(token.Assign, "=", 1, 8, 7, space(2)),
		Value:   &Literal{Tok: tok(token.Number, "1", 1, 10, 9, space(1))},
	}

	if got, want := Source(v), "var x  = 1"; got != want {
		t.Fatalf("Source = %q, want %q", got, want)
	}

	want := token.Span{File: "t.art", Line: 1, Col: 1, EndLine: 1, EndCol: 11, Offset: 0}
	if got := v.Span(); got != want {
		t.Fatalf("Span = %+v, want %+v", got, want)
	}
}

func TestSourceOfNilIsEmpty(t *testing.T) {
	if got := Source(nil); got != "" {
		t.Fatalf("Source(nil) = %q, want empty", got)
	}
}

func TestOptionalChildrenAreSkipped(t *testing.T) {
	// An `expect` with no `within` holds a zero Within token and a nil Budget.
	// Both have to vanish from Tokens, or Source would emit a stray empty
	// token and Span would start at line 0.
	e := &Expect{
		Keyword: tok(token.Ident, "expect", 1, 1, 0),
		Value:   &Ident{Tok: tok(token.Ident, "ok", 1, 8, 7, space(1))},
	}
	if got, want := Source(e), "expect ok"; got != want {
		t.Fatalf("Source = %q, want %q", got, want)
	}
	if n := len(e.Tokens(nil)); n != 2 {
		t.Fatalf("Tokens = %d, want 2", n)
	}
}

func TestTypedNilChildDoesNotPanic(t *testing.T) {
	// A nil *Ident stored in an Expr is not == nil. The parser is written not
	// to do this; R1 says no input panics, so the tree defends against it too.
	var typed Expr = (*Ident)(nil)
	v := &VarDecl{Keyword: tok(token.Ident, "var", 1, 1, 0), Value: typed}

	if got := Source(v); got != "var" {
		t.Fatalf("Source = %q, want %q", got, "var")
	}
	if kids := Children(v); len(kids) != 0 {
		t.Fatalf("Children = %d, want 0", len(kids))
	}
}

func TestStepItemsAreInSourceOrder(t *testing.T) {
	// A step whose author put `expect` above the action block: the parser
	// accepts it and diagnoses it, so the tree holds the action in Action and
	// the expect in Body -- and Source still has to emit them in the order
	// they were written.
	step := &StepDecl{
		Keyword: tok(token.Ident, "step", 1, 1, 0),
		Name:    tok(token.String, `"s"`, 1, 6, 5, space(1)),
		LBrace:  tok(token.LBrace, "{", 1, 10, 9, space(1)),
		Body: []Stmt{&Expect{
			Keyword: tok(token.Ident, "expect", 2, 3, 13, token.Trivia{Kind: token.Newline, Text: "\n"}, space(2)),
			Value:   &Ident{Tok: tok(token.Ident, "ok", 2, 10, 20, space(1))},
		}},
		Action: &Run{
			Keyword: tok(token.Ident, "run", 3, 3, 26, token.Trivia{Kind: token.Newline, Text: "\n"}, space(2)),
			Command: &Literal{Tok: tok(token.String, `"ls"`, 3, 7, 30, space(1))},
		},
		RBrace: tok(token.RBrace, "}", 4, 1, 35, token.Trivia{Kind: token.Newline, Text: "\n"}),
	}

	want := "step \"s\" {\n  expect ok\n  run \"ls\"\n}"
	if got := Source(step); got != want {
		t.Fatalf("Source =\n%q\nwant\n%q", got, want)
	}

	// Walk order is the same order, so a checker reports in reading order.
	kids := Children(step)
	if len(kids) != 2 {
		t.Fatalf("Children = %d, want 2", len(kids))
	}
	if _, ok := kids[0].(*Expect); !ok {
		t.Fatalf("first child is %T, want *Expect", kids[0])
	}
	if _, ok := kids[1].(*Run); !ok {
		t.Fatalf("second child is %T, want *Run", kids[1])
	}
}

func TestWalkVisitsEveryNodeAndCanStop(t *testing.T) {
	inner := &Binary{
		X:  &Ident{Tok: tok(token.Ident, "a", 1, 1, 0)},
		Op: tok(token.Ident, "and", 1, 3, 2, space(1)),
		Y:  &Ident{Tok: tok(token.Ident, "b", 1, 7, 6, space(1))},
	}

	var seen []string
	Inspect(inner, func(n Node) { seen = append(seen, Source(n)) })
	if len(seen) != 3 {
		t.Fatalf("Inspect visited %d nodes (%v), want 3", len(seen), seen)
	}
	if seen[0] != "a and b" {
		t.Fatalf("first visit = %q, want the whole expression", seen[0])
	}

	// A visitor returning false stops the descent but not the walk.
	var depth int
	Walk(inner, func(n Node) bool {
		depth++
		return false
	})
	if depth != 1 {
		t.Fatalf("Walk descended %d nodes after false, want 1", depth)
	}
}

func TestBadCarriesItsTokens(t *testing.T) {
	// Recovery's tokens have to survive, or a broken file would not round
	// trip.
	b := &Bad{Toks: []token.Token{
		tok(token.Ident, "nto", 1, 1, 0),
		tok(token.Ident, "x", 1, 5, 4, space(1)),
	}}
	if got, want := Source(b), "nto x"; got != want {
		t.Fatalf("Source = %q, want %q", got, want)
	}
	want := token.Span{File: "t.art", Line: 1, Col: 1, EndLine: 1, EndCol: 6, Offset: 0}
	if got := b.Span(); got != want {
		t.Fatalf("Span = %+v, want %+v", got, want)
	}
}

// TestEveryNodeTypeReportsChildren guards the one failure mode Children has:
// a node type added later, or a field added to one, that the switch forgets.
// Tokens is covered by the parser's round-trip test, which no omission
// survives; Children has no such backstop, so the list is pinned here.
func TestEveryNodeTypeReportsChildren(t *testing.T) {
	id := func(name string) Expr { return &Ident{Tok: tok(token.Ident, name, 1, 1, 0)} }

	cases := []struct {
		node Node
		want int
	}{
		{&File{Scenarios: []Decl{&Bad{Toks: []token.Token{tok(token.Ident, "x", 1, 1, 0)}}}}, 1},
		{&Scenario{Body: []Decl{&VarDecl{Value: id("a")}}}, 1},
		{&ConfigDecl{Block: &Block{}}, 1},
		{&VarDecl{Value: id("a")}, 1},
		{&StepDecl{Action: &Run{Command: id("a")}, Body: []Stmt{&Expect{Value: id("b")}}}, 2},
		{&Request{URL: id("u"), Block: &Block{}}, 2},
		{&Run{Command: id("c"), Block: &Block{}}, 2},
		{&Browser{Acts: []Stmt{&BrowserAct{Target: id("t")}}}, 1},
		{&BrowserAct{Target: id("t"), Value: id("v")}, 2},
		{&Block{Fields: []Stmt{&Field{Value: id("v")}}}, 1},
		{&Field{Key: id("k"), Value: id("v")}, 2},
		{&Field{Block: &Block{}}, 1},
		{&Expect{Value: id("v"), Budget: id("b")}, 2},
		{&Capture{Value: id("v")}, 1},
		{&Binary{X: id("a"), Y: id("b")}, 2},
		{&Unary{X: id("a")}, 1},
		{&Exists{X: id("a")}, 1},
		{&IsType{X: id("a")}, 1},
		{&Member{X: id("a")}, 1},
		{&Index{X: id("a"), Index: id("i")}, 2},
		{&Call{Callee: &Ident{}, Args: []Arg{{Value: id("a")}, {Value: id("b")}}}, 3},
		{&Object{Entries: []Entry{{Key: id("k"), Value: id("v")}}}, 2},
		{&Array{Elems: []Elem{{Value: id("a")}}}, 1},
		{&Paren{X: id("a")}, 1},
		{&Interp{Segments: []Segment{{Expr: id("a")}}}, 1},
		{id("a"), 0},
		{&Literal{}, 0},
		{&Bad{}, 0},
	}

	for _, c := range cases {
		if got := len(Children(c.node)); got != c.want {
			t.Errorf("Children(%T) = %d, want %d", c.node, got, c.want)
		}
	}
}

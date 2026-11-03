package encode

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// TestEncodedSpanHasNoVia: token.Span.Via says which use copied a node, which
// is the expander's business and not part of the published tree.
func TestEncodedSpanHasNoVia(t *testing.T) {
	f, _ := parser.Parse("t.art", "scenario \"s\" {}\n")
	f.Scenarios[0].(*ast.Scenario).Keyword.Span.Via = 3
	doc, err := Encode(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(doc)), "via") {
		t.Fatal("Span.Via leaked into the tree encoding")
	}
}

// TestImportPathQuotesBack: an import's path is carried decoded, so Decode has
// to quote it the way the lexer reads it back, escapes and `${` included.
func TestImportPathQuotesBack(t *testing.T) {
	for _, s := range []string{"auth.art", `a"b`, `a\b`, "a${b}", "$5", "tab\there", "nl\n", "\x01", "é.art"} {
		q := quoteString(s)
		toks := lexer.Lex("q.art", q)
		if len(toks) != 2 || toks[0].Kind != token.String || toks[0].Value != s {
			t.Errorf("quoteString(%q) = %s, which lexes back as %+v", s, q, toks)
		}
	}
}

// TestUseWithoutABlockDecodesWithoutOne: a use with no lines prints as a bare
// use line, alias and collection intact.
func TestUseWithoutABlockDecodesWithoutOne(t *testing.T) {
	src := "scenario \"s\" {\n  use auth.login as admin\n  use other\n}\n"
	tree, bag := parser.Parse("u.art", src)
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	doc, err := Encode(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), `"collection": ""`) || strings.Contains(string(doc), `"alias": ""`) {
		t.Errorf("an absent collection or alias must be left out, not written empty:\n%s", doc)
	}
	got, err := Source(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("round trip gave %q, want %q", got, src)
	}
}

// TestStepCallSurvivesTheRoundTrip: the step form's name is what tells it
// from a use, and it decodes with a block even when the block is empty.
func TestStepCallSurvivesTheRoundTrip(t *testing.T) {
	src := "scenario \"s\" {\n  step \"log in\" = auth.login as admin {}\n\n  step \"x\" = me {\n    drop vars\n  }\n  use auth.me\n}\n"
	tree, bag := parser.Parse("u.art", src)
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	doc, err := Encode(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Source(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("round trip gave\n%s\nwant\n%s", got, src)
	}
}

// TestItemKeywordAndParenthesesSurvive: an item's type and whether it has a
// parameter list -- `request r()` against `request r` -- come back as written.
func TestItemKeywordAndParenthesesSurvive(t *testing.T) {
	src := "collection \"c\" {\n  request r() {\n    get \"x\"\n  }\n\n  request s {\n    get \"y\"\n  }\n\n" +
		"  command m {\n    var target = \"migrate\"\n\n    run \"make\" { args = [target] }\n  }\n}\n"
	tree, bag := parser.Parse("c.art", src)
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	doc, err := Encode(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(doc), `"keyword": "command"`) != 1 || strings.Contains(string(doc), `"keyword": "request"`) {
		t.Errorf("keyword is written for a command only:\n%s", doc)
	}
	got, err := Source(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("round trip gave\n%s\nwant\n%s", got, src)
	}
}

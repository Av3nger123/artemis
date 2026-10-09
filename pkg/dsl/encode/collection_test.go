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
	got, err := Source(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("round trip gave %q, want %q", got, src)
	}
}

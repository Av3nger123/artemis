package parser

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/token"
)

// The round trip is R6, and it is the reason the tree holds tokens rather than
// spans:
//
//	ast.Source(Parse(file, src)) == src
//
// byte for byte, for every input. Subsystem D's UI loads a file, changes one
// field and writes it back, and anything the parser discarded is destroyed at
// that moment -- so this is an acceptance test of the front end, not a
// nice-to-have, and it is asserted here while the tree is still cheap to
// change rather than in ART-34 where the printer would get the blame.
//
// pkg/dsl/print owns the real printing: preserving mode re-renders only the
// nodes a UI edited, and canonical mode normalises layout. ast.Source just
// concatenates, which is what makes a failure here unambiguously a tree bug.

// corpus is every .art fixture in this package and in the lexer's, so the two
// stages are held to the same inputs and a file added for one is exercised by
// both.
func corpus(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, dir := range []string{"testdata", filepath.Join("..", "lexer", "testdata")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".art" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			files[path] = readFile(t, path)
		}
	}
	if len(files) < 6 {
		t.Fatalf("corpus has %d files, which is too few to be the whole corpus", len(files))
	}
	return files
}

func TestRoundTripOverTheCorpus(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			f, _ := Parse(path, src)
			assertRoundTrip(t, src, f)
		})
	}
}

// TestValidCorpusParsesWithoutDiagnostics separates the two things the corpus
// is for: the valid files must parse clean, and the invalid ones must round
// trip anyway. A fixture that quietly started producing a diagnostic would
// otherwise hide inside the round-trip test.
func TestValidCorpusParsesWithoutDiagnostics(t *testing.T) {
	valid := []string{
		"testdata/checkout.art",
		"testdata/exprs.art",
		filepath.Join("..", "lexer", "testdata", "checkout.art"),
		filepath.Join("..", "lexer", "testdata", "trivia.art"),
		filepath.Join("..", "lexer", "testdata", "interpolation.art"),
	}
	for _, path := range valid {
		t.Run(path, func(t *testing.T) {
			src := readFile(t, path)
			_, bag := Parse(path, src)
			for _, d := range bag.All() {
				t.Errorf("%s:%d:%d: [%s] %s", path, d.Span.Line, d.Span.Col, d.Code, d.Message)
			}
		})
	}
}

// TestEveryTokenIsInExactlyOneNode is the invariant the round trip rests on,
// asserted directly so that a failure says which token went missing rather
// than which byte.
//
// Markers are the exception and are meant to be: a position-only Invalid token
// has empty text, its diagnostic is in the bag before parsing starts, and it
// contributes nothing to the source.
func TestEveryTokenIsInExactlyOneNode(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			f, _ := Parse(path, src)

			inTree := map[int]int{}
			for _, tk := range f.Tokens(nil) {
				inTree[tk.Span.Offset]++
			}

			for _, tk := range lexTokens(path, src) {
				if tk.IsMarker() {
					continue
				}
				switch inTree[tk.Span.Offset] {
				case 1:
					// Exactly once, which is the whole claim.
				case 0:
					t.Errorf("token %q at %d:%d is in no node", tk.Text, tk.Span.Line, tk.Span.Col)
				default:
					t.Errorf("token %q at %d:%d is in %d nodes", tk.Text, tk.Span.Line, tk.Span.Col, inTree[tk.Span.Offset])
				}
			}
		})
	}
}

// TestRoundTripOfPartialFiles asserts the round trip over every prefix of
// every fixture -- a few thousand truncated files, each one cut at a different
// point in the grammar.
//
// It is the cheapest broad test of recovery there is, and it earned its place:
// it is what found the parser reusing an interpolation delimiter in two
// segments, and the browser block discarding the tokens of an action it did
// not recognise. Neither showed up in any hand-written case.
//
// It asserts only the round trip. Whether a given prefix reports is not a
// property worth pinning here, because a prefix can be a complete file -- the
// whole fixture minus its trailing newline is valid -- and the diagnostics
// recovery produces are recover_test.go's business.
func TestRoundTripOfPartialFiles(t *testing.T) {
	for path, full := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			for n := 0; n <= len(full); n++ {
				src := full[:n]
				f, _ := Parse("partial.art", src)
				if got := ast.Source(f); got != src {
					t.Fatalf("prefix of %d bytes does not round trip:\n got %q\nwant %q", n, got, src)
				}
			}
		})
	}
}

// lexTokens re-lexes a source for the token-coverage test. Re-lexing rather
// than reaching into the parser is the point: the tree is compared against a
// fresh token stream, so a parser that dropped a token cannot hide it by also
// dropping it from the list it is checked against.
func lexTokens(file, src string) []token.Token {
	return lexer.Lex(file, src)
}

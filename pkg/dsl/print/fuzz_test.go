package print

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/parser"
)

// FuzzPreserving is the gate over arbitrary bytes.
//
// "Any valid file" is the issue's wording, and the printer is held to more than
// that: a file being edited in a UI is invalid for as long as someone is typing
// in it, so the round trip has to be exact for every byte string a person can
// put in a buffer. Nothing is excluded -- not an unterminated string, not a NUL,
// not a lone carriage return.
//
// Canonical mode is exercised on the same inputs for the two properties that
// hold unconditionally: it does not panic, and it does not drop a comment.
func FuzzPreserving(f *testing.F) {
	for _, src := range seeds() {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		tree, _ := parser.Parse("fuzz.art", src)
		if tree == nil {
			t.Fatalf("nil tree for %q", src)
		}
		if got := Preserving(tree); got != src {
			t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
		}

		// Every subtree too, which is what a UI prints when it renders the node
		// it is editing.
		ast.Inspect(tree, func(n ast.Node) {
			if got, want := Preserving(n), ast.Source(n); got != want {
				t.Fatalf("node %T does not round trip:\n got %q\nwant %q", n, got, want)
			}
		})

		out := Canonical(tree)
		for _, c := range commentTexts(lexer.Lex("fuzz.art", src)) {
			if !strings.Contains(out, trimTrail(c)) {
				t.Fatalf("canonical output dropped the comment %q from %q", c, src)
			}
		}
	})
}

// FuzzValid is the other half of the gate's wording: fuzz-generated *valid*
// input.
//
// The fuzzer's bytes drive generate(), which emits a file that parses and checks
// clean and is laid out arbitrarily -- tabs and odd indents, blocks inline and
// broken, comments between a field and its separator, blank lines in runs. So
// the strong claims can be asserted on the result: canonical mode is idempotent
// on it, its output still parses and checks clean, and its token stream is
// unchanged.
func FuzzValid(f *testing.F) {
	for _, src := range seeds() {
		f.Add([]byte(src))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		src := generate(data)
		tree, bag := parser.Parse("gen.art", src)
		if bag.Len() > 0 {
			var b strings.Builder
			for _, d := range bag.All() {
				b.WriteString("\n  " + d.Message)
			}
			t.Fatalf("the generator produced a file that does not parse:%s\n%s", b.String(), src)
		}
		if _, checked := check.Check(tree); checked.Len() > 0 {
			var b strings.Builder
			for _, d := range checked.All() {
				b.WriteString("\n  " + d.Message)
			}
			t.Fatalf("the generator produced a file that does not check:%s\n%s", b.String(), src)
		}

		if got := Preserving(tree); got != src {
			t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
		}

		once := Canonical(tree)
		onceTree, onceBag := parser.Parse("gen.art", once)
		if onceBag.Len() > 0 {
			t.Fatalf("canonical output does not parse:\n%s\nfrom:\n%s", once, src)
		}
		if _, checked := check.Check(onceTree); checked.Len() > 0 {
			t.Fatalf("canonical output does not check:\n%s\nfrom:\n%s", once, src)
		}
		if twice := Canonical(onceTree); twice != once {
			t.Fatalf("canonical printing is not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
		}

		want := significant(lexer.Lex("gen.art", src))
		got := significant(lexer.Lex("gen.art", once))
		if len(want) != len(got) {
			t.Fatalf("token stream changed length %d -> %d\nin:\n%s\nout:\n%s", len(want), len(got), src, once)
		}
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("token %d changed %s -> %s\nin:\n%s\nout:\n%s", i, want[i], got[i], src, once)
			}
		}
	})
}

// TestGeneratedFilesAreValid runs the generator over a deterministic spread of
// seeds as an ordinary test, so `go test ./...` exercises it on every run rather
// than only under -fuzz. A generator that silently started producing invalid
// files would make FuzzValid pass by asserting nothing.
func TestGeneratedFilesAreValid(t *testing.T) {
	for i := 0; i < 400; i++ {
		data := []byte{byte(i), byte(i * 7), byte(i * 13), byte(i * 31), byte(i / 3), byte(i * 97)}
		src := generate(data)
		tree, bag := parser.Parse("gen.art", src)
		for _, d := range bag.All() {
			t.Fatalf("seed %d does not parse: %d:%d %s\n%s", i, d.Span.Line, d.Span.Col, d.Message, src)
		}
		if _, checked := check.Check(tree); checked.Len() > 0 {
			for _, d := range checked.All() {
				t.Fatalf("seed %d does not check: %d:%d %s\n%s", i, d.Span.Line, d.Span.Col, d.Message, src)
			}
		}
		if got := Preserving(tree); got != src {
			t.Fatalf("seed %d does not round trip:\n got %q\nwant %q", i, got, src)
		}
		once := Canonical(tree)
		onceTree, onceBag := parser.Parse("gen.art", once)
		for _, d := range onceBag.All() {
			t.Fatalf("seed %d: canonical output does not parse: %d:%d %s\n%s", i, d.Span.Line, d.Span.Col, d.Message, once)
		}
		if twice := Canonical(onceTree); twice != once {
			t.Fatalf("seed %d: canonical printing is not idempotent:\nonce:\n%s\ntwice:\n%s", i, once, twice)
		}
	}
}

// seeds are the corpus plus the shapes that have historically broken a printer:
// a file that is only trivia, a block that was never closed, a comment with
// nothing after it.
func seeds() []string {
	return []string{
		"",
		"\n",
		"#",
		"# just a comment",
		"\ufeff", // a byte-order mark, which the lexer keeps as whitespace
		"scenario \"s\" {}",
		"scenario \"s\" {}\n",
		"scenario \"s\" {\n  var a = 1\n}\n",
		"scenario \"s\" {\n  var a  =  1\t\n}\n",
		"scenario \"s\" {\n\n\n  var a = 1\n\n\n}\n",
		"scenario \"s\" { # c\n  var a = 1 # c\n  # c\n}\n# c",
		"scenario \"s\" {\r\n  var a = 1\r\n}\r\n",
		"scenario \"s\" {\n  config browser { headless = true, viewport = \"1x1\" }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    expect status == 200\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    run \"x\" { args = [1], env { A = \"b\" } }\n    expect exit_code == 0\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    browser { goto \"/\" }\n    expect page.url contains \"/\"\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    retry { times = 1, delay = \"1s\" }\n    timeout = \"1s\"\n  }\n}\n",
		"scenario \"s\" { step \"t\" { get \"/x\" { body = { \"a\": [ ( 1",
		"}}}}]]]])))",
		"scenario \"s\" {\n  var a = \"${\n}\n",
		"scenario \"s\" {\n  var a = /unterminated\n}\n",
		"scenario \"é\" {\n  var a = \"日本\"\n}\n",
		"scenario \"s\" { var a = \x00 }",
	}
}

package print

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// -update rewrites the canonical goldens from what the printer actually
// produces:
//
//	go test ./pkg/dsl/print -update
//
// or `make golden`. Read the diff before committing it: a golden regenerated
// without being read asserts that the code does what the code does.
var update = flag.Bool("update", false, "rewrite the canonical goldens in testdata/canon")

const canonDir = "testdata/canon"

// TestCanonicalGoldens pins the layout itself. The properties below say
// canonical mode is self-consistent; only a golden says it produces a file a
// person would want to read, and only a golden makes a change to the layout
// something someone has to look at and approve.
func TestCanonicalGoldens(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, _ := parser.Parse(path, src)
			checkGolden(t, filepath.Join(canonDir, canonName(path)), Canonical(tree))
		})
	}
}

// TestCanonicalIsIdempotent is the issue's second acceptance criterion.
//
// `artemis fmt -w` that changed a file it had already formatted would make
// every commit a formatting commit.
//
// It is asserted for the files that parse clean, which is what `fmt` is for. A
// file with a *lexical* error cannot be held to it: an unterminated string is
// one token whose text runs to the end of the file, newlines and all, so
// reprinting it puts line breaks inside what canonical mode believes is a
// single line, and the second pass lexes a different file. The printer stays
// total on such a file -- it panics on nothing and deletes nothing, which the
// goldens cover -- and `artemis fmt` refusing to format a file with diagnostics
// is ART-35's call to make rather than something to paper over here.
func TestCanonicalIsIdempotent(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			skipUnlessClean(t, path, src)
			tree, _ := parser.Parse(path, src)
			once := Canonical(tree)
			again, _ := parser.Parse(path, once)
			if got := Canonical(again); got != once {
				t.Errorf("canonical printing is not idempotent\nonce:\n%s\ntwice:\n%s", once, got)
			}
		})
	}
}

// TestCanonicalEndsWithOneNewline is the invariant that keeps a formatted file
// out of the "no newline at end of file" column of a diff.
func TestCanonicalEndsWithOneNewline(t *testing.T) {
	for path, src := range corpus(t) {
		if !parsesClean(path, src) {
			continue // a multi-line token; see TestCanonicalIsIdempotent
		}
		tree, _ := parser.Parse(path, src)
		out := Canonical(tree)
		if out == "" {
			continue
		}
		if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
			t.Errorf("%s: canonical output must end with exactly one newline, got %q", path, tail(out))
		}
		if strings.Contains(out, "\r") {
			t.Errorf("%s: canonical output still holds a carriage return", path)
		}
		for i, line := range strings.Split(out, "\n") {
			if trimTrail(line) != line {
				t.Errorf("%s:%d: canonical output has trailing whitespace: %q", path, i+1, line)
			}
		}
	}
}

// TestCanonicalChangesLayoutOnly is R4, and it is the assertion that makes the
// other canonical tests worth having: a formatter is allowed to move bytes
// around and is not allowed to change what the file says.
//
// Comma tokens are filtered out of both streams because a comma *is* layout in
// this grammar -- `Sep = "," | newline` -- so canonical mode drops the ones it
// replaced with a line break and the trailing one an author left in a list.
// Everything else must match exactly: a requoted string, a reformatted number,
// a rewritten regex, a dropped paren or a reassociated operator chain all fail
// here.
func TestCanonicalChangesLayoutOnly(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, bag := parser.Parse(path, src)
			out := Canonical(tree)
			if bag.Len() > 0 {
				// A file that did not parse holds ast.Bad nodes, whose source
				// is reprinted as lines rather than rebuilt from the grammar,
				// so its token stream is not a claim this test can make.
				t.Skip("file does not parse; token stream is not comparable")
			}
			want := significant(lexer.Lex(path, src))
			got := significant(lexer.Lex(path, out))
			if len(want) != len(got) {
				t.Fatalf("token stream changed length: %d -> %d\nin:  %v\nout: %v", len(want), len(got), want, got)
			}
			for i := range want {
				if want[i] != got[i] {
					t.Errorf("token %d changed: %s -> %s", i, want[i], got[i])
				}
			}
		})
	}
}

// TestCanonicalKeepsEveryComment is R3. Canonical mode normalises all layout
// and a comment is not layout: it is the only thing in a .art file that exists
// purely for the next person to read.
func TestCanonicalKeepsEveryComment(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, _ := parser.Parse(path, src)
			want := commentTexts(lexer.Lex(path, src))
			got := commentTexts(lexer.Lex(path, Canonical(tree)))
			for _, c := range want {
				if !removeOne(&got, c) {
					t.Errorf("canonical output dropped the comment %q", c)
				}
			}
		})
	}
}

// TestCanonicalOutputIsClean is R5: a file that says nothing wrong before
// formatting says nothing wrong after it. This is the check that would catch a
// layout rule that produced a file the parser reads differently -- a hoisted
// comment swallowing the rest of a line, an inline block that lost a separator.
func TestCanonicalOutputIsClean(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, bag := parser.Parse(path, src)
			_, checked := check.Check(tree)
			bag.Merge(checked)
			before := codes(bag)

			out := Canonical(tree)
			outTree, outBag := parser.Parse(path, out)
			_, outChecked := check.Check(outTree)
			outBag.Merge(outChecked)
			after := codes(outBag)

			if len(before) == 0 && len(after) > 0 {
				t.Errorf("a clean file does not survive formatting; canonical output reports %v\n%s", after, out)
			}
		})
	}
}

// TestCanonicalOfASubtree is what `artemis fmt` on one field will need and what
// the UI needs for a preview: canonical mode is defined on any node, not only
// on a file.
func TestCanonicalOfASubtree(t *testing.T) {
	src := read(t, filepath.Join("..", "parser", "testdata", "checkout.art"))
	tree, _ := parser.Parse("checkout.art", src)

	seen := 0
	ast.Inspect(tree, func(n ast.Node) {
		if _, ok := n.(*ast.VarDecl); !ok {
			return
		}
		seen++
		if got := Canonical(n); !strings.HasPrefix(got, "var ") {
			t.Errorf("Canonical of a var decl is %q", got)
		}
	})
	if seen == 0 {
		t.Fatal("no var decl in checkout.art")
	}
	if got := Canonical(nil); got != "" {
		t.Errorf("Canonical(nil) = %q, want empty", got)
	}
}

// parsesClean reports whether a fixture produces no diagnostics at all, which
// is the condition canonical mode's strict claims are made under.
func parsesClean(path, src string) bool {
	_, bag := parser.Parse(path, src)
	return bag.Len() == 0
}

func skipUnlessClean(t *testing.T, path, src string) {
	t.Helper()
	if !parsesClean(path, src) {
		t.Skip("file does not parse clean")
	}
}

// significant is the token stream with trivia and separators removed: what the
// file says, rather than how it is laid out.
func significant(toks []token.Token) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		switch {
		case t.Kind == token.EOF, t.Kind == token.Comma, t.IsMarker():
			continue
		}
		out = append(out, fmt.Sprintf("%s(%s)", t.Kind, t.Text))
	}
	return out
}

func commentTexts(toks []token.Token) []string {
	var out []string
	for _, t := range toks {
		out = appendComments(out, t.Leading)
		out = appendComments(out, t.Trailing)
	}
	return out
}

func removeOne(have *[]string, want string) bool {
	for i, h := range *have {
		if h == want {
			*have = append((*have)[:i], (*have)[i+1:]...)
			return true
		}
	}
	return false
}

func codes(bag *diag.Bag) []string {
	var out []string
	for _, d := range bag.All() {
		out = append(out, string(d.Code))
	}
	sort.Strings(out)
	return out
}

// canonName is a fixture's golden name. The corpus spans four directories and
// two of them hold a checkout.art, so the label is part of the name.
func canonName(path string) string {
	dir := filepath.Clean(filepath.Dir(path))
	label := filepath.Base(dir)
	if label == "testdata" {
		label = filepath.Base(filepath.Dir(dir))
		if label == "." || label == string(filepath.Separator) {
			label = "print"
		}
	}
	return label + "-" + strings.TrimSuffix(filepath.Base(path), ".art") + ".canon"
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test ./pkg/dsl/print -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s does not match. If the layout change was deliberate, run "+
			"`go test ./pkg/dsl/print -update` and read the diff.\n got:\n%s\nwant:\n%s",
			filepath.Base(path), got, string(want))
	}
}

func tail(s string) string {
	if len(s) > 12 {
		return s[len(s)-12:]
	}
	return s
}

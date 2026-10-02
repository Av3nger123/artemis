// Package print's tests are the worklane's first hard acceptance gate.
//
//	print.Preserving(parser.Parse(file, src)) == src
//
// byte for byte, over every fixture in pkg/dsl, over every prefix of every one
// of them, over every node of every tree, and over fuzz-generated input -- both
// arbitrary bytes and generated *valid* files. Subsystem D's UI loads a file,
// changes one field and writes it back; anything the front end discarded is
// destroyed at that moment, and a front end that was not lossless from the
// start cannot be made lossless later without being rewritten. So this is an
// A-level test, asserted now, rather than a D task after the UI exists.
package print

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
)

// corpusDirs is every directory in pkg/dsl holding .art fixtures: the lexer's
// trivia and interpolation cases, the parser's exhaustive expression file and
// the design document's worked example, and -- the ones that matter most here --
// ART-32's hostile invalid files, which are what a file mid-edit in a UI
// actually looks like.
var corpusDirs = []string{
	filepath.Join("..", "lexer", "testdata"),
	filepath.Join("..", "parser", "testdata"),
	filepath.Join("..", "testdata", "invalid"),
	"testdata",
}

// corpus is every .art file in those directories, keyed by path.
func corpus(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, dir := range corpusDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".art" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			files[path] = read(t, path)
		}
	}
	if len(files) < 25 {
		t.Fatalf("corpus has %d files, which is too few to be the whole corpus", len(files))
	}
	return files
}

// TestPreservingIsExactOverTheCorpus is the gate itself.
func TestPreservingIsExactOverTheCorpus(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, _ := parser.Parse(path, src)
			if got := Preserving(tree); got != src {
				t.Errorf("Preserving(parse(src)) != src\n got:\n%q\nwant:\n%q", got, src)
			}
		})
	}
}

// TestPreservingIsExactForEveryNode asserts the gate for every subtree, not
// just the root: a UI prints the node it is editing, and `artemis ast` will
// print a slice of a file. A printer that only agreed at the root would be one
// `ast.Source` call away from looking correct.
func TestPreservingIsExactForEveryNode(t *testing.T) {
	for path, src := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			tree, _ := parser.Parse(path, src)
			ast.Inspect(tree, func(n ast.Node) {
				if got, want := Preserving(n), ast.Source(n); got != want {
					t.Fatalf("node %T does not round trip\n got: %q\nwant: %q", n, got, want)
				}
			})
		})
	}
}

// TestPreservingIsExactForEveryPrefix is ART-31's sweep pointed at the printer:
// a few thousand truncated files, each cut at a different point in the grammar,
// which is the cheapest broad test of the recovery path there is. A file being
// edited in a UI is a truncated file for as long as the user is typing.
func TestPreservingIsExactForEveryPrefix(t *testing.T) {
	for path, full := range corpus(t) {
		t.Run(path, func(t *testing.T) {
			for n := 0; n <= len(full); n++ {
				src := full[:n]
				tree, _ := parser.Parse("partial.art", src)
				if got := Preserving(tree); got != src {
					t.Fatalf("prefix of %d bytes does not round trip:\n got %q\nwant %q", n, got, src)
				}
			}
		})
	}
}

// TestPreservingIsAstSourceWhenNothingIsEdited pins the claim the gate rests
// on: for an untouched tree this mode is concatenation, so no layout rule can
// break it. A regression that made preserving mode re-render an intact node
// would still pass the tests above if the renderer happened to agree with the
// file; this one fails on the attempt.
func TestPreservingIsAstSourceWhenNothingIsEdited(t *testing.T) {
	for path, src := range corpus(t) {
		tree, _ := parser.Parse(path, src)
		if !intact(tree) {
			t.Errorf("%s: a freshly parsed tree reports an edit", path)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

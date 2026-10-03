// Package encode's tests run over the same corpus pkg/dsl/print's do, because
// they are asserting the same kind of thing about a second serialisation of the
// tree: that nothing is lost.
//
//	print.Canonical(Decode(Encode(tree))) == print.Canonical(tree)
//	Encode(Decode(j)) == j
//
// over every .art fixture in pkg/dsl, plus a schema golden so that an
// unintended change to the UI contract fails CI rather than a released UI.
package encode

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

// -update rewrites the goldens from what the code actually produces:
//
//	go test ./pkg/dsl/encode -update
//
// or `make golden`. Read the diff before committing it -- a golden regenerated
// without being read asserts that the code does what the code does, and this
// particular golden is a published contract.
var update = flag.Bool("update", false, "rewrite the goldens in testdata")

// corpusDirs is every directory in pkg/dsl holding .art fixtures: the lexer's
// trivia and interpolation cases, the parser's exhaustive expression file and
// the design document's worked example, ART-32's hostile invalid files, and the
// printer's layout fixtures.
var corpusDirs = []string{
	filepath.Join("..", "lexer", "testdata"),
	filepath.Join("..", "parser", "testdata"),
	filepath.Join("..", "testdata", "invalid"),
	filepath.Join("..", "print", "testdata"),
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

// encoded is a fixture's canonical form, the tree parsed from it, and that
// tree's encoding.
//
// The round trip is anchored at *canonical* source rather than at the file as
// written, because that is where the fixed point is: this encoding carries what
// canonical layout carries, and `--from-json` writes canonical layout. ok is
// false for a file that does not parse clean, which has no canonical form to be
// a fixed point of -- the invalid corpus is covered by the totality tests
// instead.
func encoded(t *testing.T, path, src string) (canon string, tree *ast.File, doc []byte, ok bool) {
	t.Helper()
	first, bag := parser.Parse(path, src)
	if bag.HasErrors() {
		return "", nil, nil, false
	}
	canon = print.Canonical(first)
	tree, bag = parser.Parse(path, canon)
	if bag.HasErrors() {
		t.Fatalf("%s: canonical output does not parse: %v", path, bag.All())
	}
	info, _ := check.Check(tree)
	doc, err := Encode(tree, info)
	if err != nil {
		t.Fatalf("%s: encoding: %v", path, err)
	}
	return canon, tree, doc, true
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// golden compares got against the golden at testdata/name, and rewrites it
// under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test ./pkg/dsl/encode -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s is out of date; run `go test ./pkg/dsl/encode -update` and read the diff\n%s",
			path, firstDifference(string(want), got))
	}
}

// firstDifference is the line the two strings first disagree on, with its
// number, because a 4000-line JSON diff printed in full tells nobody anything.
func firstDifference(want, got string) string {
	wl, gl := lines(want), lines(got)
	for i := 0; i < len(wl) || i < len(gl); i++ {
		w, g := at(wl, i), at(gl, i)
		if w != g {
			return "line " + itoa(i+1) + ":\n  want: " + w + "\n   got: " + g
		}
	}
	return "(the files differ only in their trailing bytes)"
}

func lines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func at(ls []string, i int) string {
	if i < len(ls) {
		return ls[i]
	}
	return "(end of file)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

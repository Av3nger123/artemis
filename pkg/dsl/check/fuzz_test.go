package check

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// FuzzCheck asserts for arbitrary bytes what the package's tests assert for
// the files someone thought to write down.
//
//  1. Nothing panics. The checker's input is whatever the parser recovered,
//     which means a nil Action, a nil Expr and an ast.Bad in every position --
//     so "handle the broken tree" is the normal case here, not an edge case,
//     and the front end's promise is that a malformed file yields diagnostics
//     and never a stack trace.
//  2. A non-nil Info and Bag always come back, because a caller merges them
//     unconditionally.
//  3. Every diagnostic is renderable: a registered code, a message, and a span
//     inside the file. A span that ran past the end of the source would make
//     the terminal renderer slice out of range, turning the thing that reports
//     the user's mistake into a crash of its own.
//
// Seeded from the invalid corpus and the parser's fixtures, because those are
// the shapes already known to be hostile.
func FuzzCheck(f *testing.F) {
	for _, src := range seedCorpus(f) {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		tree, _ := parser.Parse("fuzz.art", src)
		info, bag := Check(tree)

		if info == nil || bag == nil {
			t.Fatalf("nil Info or Bag for %q", src)
		}

		for _, d := range bag.All() {
			if !diag.Registered(d.Code) {
				t.Fatalf("diagnostic code %q is not registered: %q", d.Code, src)
			}
			if d.Message == "" {
				t.Fatalf("diagnostic %q has no message: %q", d.Code, src)
			}
			if d.Span.Offset < 0 || d.Span.Offset > len(src) {
				t.Fatalf("span offset %d outside a %d-byte file: %q", d.Span.Offset, len(src), src)
			}
			if d.Span.Line < 1 || d.Span.Col < 1 {
				t.Fatalf("span %+v is not 1-based: %q", d.Span, src)
			}
			if d.Span.EndLine < d.Span.Line {
				t.Fatalf("span %+v ends before it starts: %q", d.Span, src)
			}
		}

		// Every step a scenario holds was typed. The Info is a promise about
		// every scenario, and a step the descent never reached would be a step
		// ART-37's lowerer has no type for -- which is the one way this
		// package can fail silently. A collection's steps are not Check's: a
		// flow's steps are checked where pkg/dsl/expand puts them, so the walk
		// is over the scenarios, as Check's own is.
		for _, d := range tree.Scenarios {
			sc, ok := d.(*ast.Scenario)
			if !ok {
				continue
			}
			ast.Inspect(sc, func(n ast.Node) {
				if s, ok := n.(*ast.StepDecl); ok {
					if _, typed := info.steps[s]; !typed {
						t.Fatalf("a step was not typed: %q", src)
					}
				}
			})
		}

		// Running twice must give the same answer. The checker holds no state
		// between calls, and a scope that leaked across scenarios -- the one
		// mistake the source-order walk makes easy -- would show up here as a
		// second run disagreeing with the first.
		_, again := Check(tree)
		if again.Len() != bag.Len() {
			t.Fatalf("second Check reported %d diagnostics, first reported %d: %q",
				again.Len(), bag.Len(), src)
		}
	})
}

// seedCorpus is every .art file the front end's tests already keep: the
// invalid corpus, which is chosen to be hostile, and the parser's fixtures,
// which cover every production.
func seedCorpus(f *testing.F) []string {
	f.Helper()

	var out []string
	for _, dir := range []string{
		filepath.Join("..", "testdata", "invalid"),
		filepath.Join("..", "parser", "testdata"),
		filepath.Join("..", "lexer", "testdata"),
	} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.art"))
		if err != nil {
			f.Fatalf("globbing %s: %v", dir, err)
		}
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				f.Fatalf("reading %s: %v", p, err)
			}
			out = append(out, string(b))
		}
	}
	if len(out) == 0 {
		f.Fatal("no seeds found; the fuzz target would start from nothing")
	}
	return out
}

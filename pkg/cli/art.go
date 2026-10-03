package cli

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// artExt is the DSL's extension. It is what `artemis parse` dispatches on while
// two formats exist, and the only thing `artemis fmt` will open.
const artExt = ".art"

// isArtFile reports whether path names a .art file. The comparison is
// case-insensitive, because a path typed on a case-insensitive filesystem is
// still the file the user meant.
func isArtFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), artExt)
}

// frontEnd is the whole front end over one file's source: lex and parse, then
// resolve names, with the two bags merged.
//
// The checker's Info comes back as well as the bag, because it holds the two
// facts nothing else can re-derive without risking a second answer: every
// step's type and scope, and whether every `expect` is form-shaped. `artemis
// ast` serialises them; see pkg/dsl/encode.
//
// Merging rather than short-circuiting is the point. A file with a syntax error
// on line 3 and an unknown field on line 20 reports both, because the parser
// recovers at statement boundaries and the checker skips only the steps whose
// action did not parse. This is pkg/dsl/corpus_test.go's function of the same
// name; that one stays where it is because it tests the front end rather than
// the CLI, and the two agreeing is what makes the corpus goldens a prediction
// of what these commands print.
func frontEnd(file, src string) (*ast.File, *check.Info, *diag.Bag) {
	tree, bag := parser.Parse(file, src)
	info, checked := check.Check(tree)
	bag.Merge(checked)
	return tree, info, bag
}

// loadArt reads path, runs the front end over it, and renders every diagnostic
// it produced to the command's stderr.
//
// The tree and the checker's Info come back whatever happened, and the error is
// non-nil exactly when the bag holds an error diagnostic -- so a caller that
// only reports chooses the tree and a caller that writes to disk chooses the
// error. A read failure is a plain error with no tree: a file that is not there
// has no contents to be diagnosed about.
func loadArt(cmd *cobra.Command, path string) (*ast.File, *check.Info, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return nil, nil, err
	}

	tree, info, bag := frontEnd(path, string(src))
	diags := bag.All()
	if len(diags) > 0 {
		files := diag.NewFiles()
		files.Add(path, string(src))
		// stderr: stdout carries documents only -- fmt's output, parse's ok
		// line, and later --json -- so `artemis fmt x.art > out.art` is a
		// formatted file and never a file with an error report on top of it.
		if err := diag.Terminal(cmd.ErrOrStderr(), files, diags); err != nil {
			return tree, info, err
		}
	}
	if bag.HasErrors() {
		return tree, info, errorsIn(path, diags)
	}
	return tree, info, nil
}

// errorsIn is the one-line reason attached to a non-zero exit. The diagnostics
// themselves have already been rendered; this is what survives when a CI log
// keeps only the last line, so it names the count and the file.
func errorsIn(path string, diags []diag.Diagnostic) error {
	n := 0
	for _, d := range diags {
		if d.Severity == diag.Error {
			n++
		}
	}
	if n == 1 {
		return fmt.Errorf("1 error in %s", path)
	}
	return fmt.Errorf("%d errors in %s", n, path)
}

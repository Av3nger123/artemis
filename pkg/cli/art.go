package cli

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// artExt and isArtFile live in pkg/dsl/front now: the MCP server's workspace
// check needs the same answer about the same path, and two copies of a file
// extension is how two clients start disagreeing about what they will open.
// Kept as aliases because every command in this package names them.
const artExt = front.Ext

func isArtFile(path string) bool { return front.IsArtFile(path) }

// frontEnd is front.Compile under the name every command in this package
// already calls. The composition itself -- and the reasoning for merging the
// bags rather than short-circuiting -- moved to pkg/dsl/front, so that the MCP
// server can run the same front end over a source string rather than growing a
// second one.
//
// pkg/dsl/corpus_test.go keeps its own copy, deliberately. That one tests the
// front end rather than the CLI, and the two agreeing is what makes the corpus
// goldens a prediction of what these commands print -- a property that dies the
// moment both call the same function.
func frontEnd(file, src string) (*ast.File, *check.Info, *diag.Bag) {
	return front.Compile(file, src)
}

// artFile is one file after the front end has run over it: the source, the
// tree, the checker's Info and every diagnostic, in file order.
//
// The two renderings -- diag.Terminal for a person, diag.JSON for a client --
// are the only thing `artemis parse` and `artemis parse --json` do
// differently, so they share everything up to this struct and differ after it.
// A UI and a terminal reading different diagnostics about the same file would
// be the worst possible failure of this contract.
type artFile struct {
	path  string
	src   string
	tree  *ast.File
	info  *check.Info
	diags []diag.Diagnostic
	fatal bool // the bag holds an error, so the file does not compile
}

// readArt reads path and runs the front end over it, rendering nothing.
//
// A read failure is a plain error with no artFile: a file that is not there
// has no contents to be diagnosed about.
func readArt(path string) (*artFile, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return nil, err
	}
	tree, info, bag := frontEnd(path, string(src))
	return &artFile{
		path:  path,
		src:   string(src),
		tree:  tree,
		info:  info,
		diags: bag.All(),
		fatal: bag.HasErrors(),
	}, nil
}

// err is the one-line failure for a file that does not compile, or nil.
func (f *artFile) err() error {
	if !f.fatal {
		return nil
	}
	return errorsIn(f.path, f.diags)
}

// render writes the diagnostics the way a person reads them.
//
// stderr: stdout carries documents only -- fmt's output, parse's ok line,
// `artemis ast`'s tree and `artemis parse --json`'s envelope -- so `artemis
// fmt x.art > out.art` is a formatted file and never a file with an error
// report on top of it.
func (f *artFile) render(w io.Writer) error {
	if len(f.diags) == 0 {
		return nil
	}
	files := diag.NewFiles()
	files.Add(f.path, f.src)
	return diag.Terminal(w, files, f.diags)
}

// loadArt is readArt plus render: the path every command but `artemis parse
// --json` takes.
//
// The tree and the checker's Info come back whatever happened, and the error is
// non-nil exactly when the bag holds an error diagnostic -- so a caller that
// only reports chooses the tree and a caller that writes to disk chooses the
// error.
func loadArt(cmd *cobra.Command, path string) (*ast.File, *check.Info, error) {
	f, err := readArt(path)
	if err != nil {
		return nil, nil, err
	}
	if err := f.render(cmd.ErrOrStderr()); err != nil {
		return f.tree, f.info, err
	}
	return f.tree, f.info, f.err()
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

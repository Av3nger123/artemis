// Package front is the DSL's front end over one file's source: lex and parse,
// expand imported collections' uses, then resolve names, with the diagnostic
// bags merged.
//
// It is its own package because three callers need the same answer about the
// same file -- the CLI commands, pkg/dsl's corpus goldens, and the MCP server's
// artemis_validate. A terminal, a UI and an agent reading different diagnostics
// about one file is the failure docs/artemis-mcp-client.md exists to prevent,
// and one function is how that is prevented by construction rather than by
// anyone remembering.
package front

import (
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/parser"
)

// Ext is the DSL's extension. It is what `artemis parse` dispatches on, the
// only thing `artemis fmt` will open, and what the MCP server's workspace check
// requires of a scenario path.
const Ext = ".art"

// IsArtFile reports whether path names a .art file. The comparison ignores
// case, because a path typed on a case-insensitive filesystem is still the file
// the user meant.
func IsArtFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), Ext)
}

// Unit is one file through the whole front end.
type Unit struct {
	// Tree is the file as written: what fmt, ast and the MCP formatter read.
	Tree *ast.File
	// Expanded is what runs: what check, lower and build read. It is Tree
	// itself when the file has no import, collection or use.
	Expanded *ast.File
	// Info is the checker's facts about Expanded.
	Info *check.Info
	// Sources is every file read, the root included, by the name its spans
	// carry -- what a terminal renderer needs to echo a diagnostic in a
	// collection file.
	Sources map[string]string
	// Bag is every diagnostic: the parser's, the expander's and the
	// checker's, the last two with their use chains.
	Bag *diag.Bag
	// Uses is the expansion's use table, which a span's Via indexes (Via-1):
	// what `artemis expand` reads to say where each step came from.
	Uses []expand.Use
}

// CompileWith is the whole front end: parse, expand, check. l reads imported
// files; nil means the file has no disk to read from, and an import in it is
// an error.
//
// Tree is the file as written -- what fmt and ast read. Expanded is what runs:
// the same pointer when the file has no import, collection or use, so a file
// that does not use collections compiles to exactly what Compile gives.
//
// The checker runs on the expanded tree, so a fault in a step a use brought in
// is reported where the step was written, in the collection, and carries the
// use lines that brought it in as UsedFrom.
func CompileWith(file, src string, l expand.Loader) *Unit {
	tree, bag := parser.Parse(file, src)
	res, eb := expand.Expand(tree, l)
	bag.Merge(eb)
	info, checked := check.CheckChained(res.File, res.Chain)
	for _, d := range checked.All() {
		d.UsedFrom = res.Chain(d.Span)
		bag.Add(d)
	}
	sources := map[string]string{file: src}
	for k, v := range res.Sources {
		sources[k] = v
	}
	return &Unit{Tree: tree, Expanded: res.File, Info: info, Sources: sources, Bag: bag, Uses: res.Uses}
}

// Compile lexes, parses and name-checks src, and returns every diagnostic in
// file order. It is CompileWith under a nil loader: a file with no import,
// collection or use compiles exactly as it always did, and an import is the
// error "import needs a file on disk".
//
// The checker's Info comes back beside the bag because it holds the two facts
// nothing else can re-derive without risking a second answer: every step's type
// and scope, and whether every `expect` is form-shaped. pkg/dsl/encode
// serialises them for `artemis ast`.
//
// The bags are merged rather than short-circuited. A file with a syntax error
// on line 3 and an unknown field on line 20 reports both, because the parser
// recovers at statement boundaries and the checker skips only the steps whose
// action did not parse.
func Compile(file, src string) (*ast.File, *check.Info, *diag.Bag) {
	u := CompileWith(file, src, nil)
	return u.Tree, u.Info, u.Bag
}

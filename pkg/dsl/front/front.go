// Package front is the DSL's front end over one file's source: lex and parse,
// then resolve names, with the two diagnostic bags merged.
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

// Compile lexes, parses and name-checks src, and returns every diagnostic in
// file order.
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
	tree, bag := parser.Parse(file, src)
	info, checked := check.Check(tree)
	bag.Merge(checked)
	return tree, info, bag
}

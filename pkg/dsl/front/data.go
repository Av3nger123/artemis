package front

import (
	"path/filepath"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/builtin"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
)

// checkData reads every json(), csv() and file() whose path is a literal, and
// reports a path that names nothing and a data file that does not parse.
//
// Here, when the file compiles, rather than when a step reaches the call:
// the data is in the scenario's own repository, so a missing fixture or a
// trailing comma in users.json is a fault in the file -- reported with its
// line, before anything runs -- and not a step that errors halfway through a
// suite. A path built at run time (`json(env("DATA"))`) is the run's to read.
//
// It reads through l, the loader imports go through, so the MCP server's
// workspace bound applies to data as it does to collections. With no loader --
// Compile, which formats and parses without a disk -- nothing is read.
func checkData(tree *ast.File, l expand.Loader, bag *diag.Bag, chain func(token.Span) []token.Span) {
	if l == nil || tree == nil {
		return
	}
	found := &diag.Bag{}
	ast.Inspect(tree, func(n ast.Node) {
		call, ok := n.(*ast.Call)
		if !ok || call.Callee == nil || len(call.Args) != 1 {
			return
		}
		name := call.Callee.Name()
		fn, ok := builtin.Lookup(name)
		if !ok || !fn.Static {
			return
		}
		lit, ok := call.Args[0].Value.(*ast.Literal)
		if !ok || lit == nil || lit.Kind() != token.String || lit.Tok.Value == "" {
			return
		}
		path := lit.Tok.Value
		span := lit.Span()
		_, src, err := l.Load(span.File, path)
		if err != nil {
			found.Error(span, diag.DataFileMissing, "%s(): there is no file %s", name, path).
				Hintf("a path is relative to the file it is written in, here %s", dirOf(span.File))
			return
		}
		switch name {
		case "json":
			if _, err := eval.ParseJSON([]byte(src)); err != nil {
				found.Error(span, diag.DataNotJSON, "json(): %s is not JSON: %v", path, err)
			}
		case "csv":
			if _, err := eval.ParseCSV([]byte(src)); err != nil {
				found.Error(span, diag.DataNotCSV, "csv(): %s is not CSV: %v", path, err)
			}
		}
	})
	// A call a use brought in from a collection carries the use lines that
	// brought it, as the checker's diagnostics do.
	for _, d := range found.All() {
		d.UsedFrom = chain(d.Span)
		bag.Add(d)
	}
}

// dirOf names the directory a path is relative to, as a reader would write it.
func dirOf(file string) string {
	dir := filepath.Dir(file)
	if dir == "." || dir == "" {
		return "the current directory"
	}
	return dir + string(filepath.Separator)
}

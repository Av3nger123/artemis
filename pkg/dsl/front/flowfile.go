package front

import (
	"path/filepath"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// FlowDecl reports the flow a flow file declares, `flow checkout { ... }`: the
// keyword, which a diagnostic about the file points at, and the flow's name.
// ok is false for any other file.
//
// It reads the parsed tree for a top-level *ast.Flow, which the parser makes
// of `flow <name> {` even when what follows does not parse.
func FlowDecl(file, src string) (keyword token.Token, name string, ok bool) {
	tree, _ := parser.Parse(file, src)
	for _, d := range tree.Scenarios {
		if f, isFlow := d.(*ast.Flow); isFlow {
			return f.Keyword, f.Name.Value, true
		}
	}
	return token.Token{}, "", false
}

// FlowHeader reports the path a scenario file's header names, `flow
// "../flows/checkout.art"`, as written: relative to the scenario file. ok is
// false for a file with no header.
func FlowHeader(file, src string) (path string, ok bool) {
	tree, _ := parser.Parse(file, src)
	for _, d := range tree.Scenarios {
		if r, isRef := d.(*ast.FlowRef); isRef {
			return r.Path.Value, true
		}
	}
	return "", false
}

// HeaderNames reports whether a scenario file at scenarioFile whose header is
// header names the flow file at flowFile. The header is relative to the
// scenario file; both paths are compared made absolute and cleaned.
func HeaderNames(scenarioFile, header, flowFile string) bool {
	named, err := filepath.Abs(filepath.Join(filepath.Dir(scenarioFile), filepath.FromSlash(header)))
	if err != nil {
		return false
	}
	flow, err := filepath.Abs(flowFile)
	if err != nil {
		return false
	}
	return named == flow
}

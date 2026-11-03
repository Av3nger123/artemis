package front

import (
	"path/filepath"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// FlowDecl reports the flow a flow file declares, `flow checkout { ... }`: the
// keyword, which a diagnostic about the file points at, and the flow's name.
// ok is false for any other file.
//
// It reads the parsed tree for a top-level *ast.Flow. When the tree has none,
// the first declaration word below the imports is read off the tokens: `flow`
// followed by a name is a flow file whether or not the parser knows the form
// yet, so a flow file that does not parse is still not run as a scenario file.
func FlowDecl(file, src string) (keyword token.Token, name string, ok bool) {
	tree, _ := parser.Parse(file, src)
	for _, d := range tree.Scenarios {
		if f, isFlow := d.(*ast.Flow); isFlow {
			return f.Keyword, f.Name.Value, true
		}
	}
	kw, next, found := firstDecl(file, src)
	if !found || next.Kind != token.Ident {
		return token.Token{}, "", false
	}
	return kw, next.Value, true
}

// FlowHeader reports the path a scenario file's header names, `flow
// "../flows/checkout.art"`, as written: relative to the scenario file. ok is
// false for a file with no header. Like FlowDecl, it falls back to the first
// declaration word when the tree holds no *ast.FlowRef.
func FlowHeader(file, src string) (path string, ok bool) {
	tree, _ := parser.Parse(file, src)
	for _, d := range tree.Scenarios {
		if r, isRef := d.(*ast.FlowRef); isRef {
			return r.Path.Value, true
		}
	}
	_, next, found := firstDecl(file, src)
	if !found || next.Kind != token.String {
		return "", false
	}
	return next.Value, true
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

// firstDecl finds the file's first declaration below its imports when that
// declaration opens with the word flow, and answers the flow keyword and the
// token after it.
func firstDecl(file, src string) (kw, next token.Token, ok bool) {
	toks := lexer.Lex(file, src)
	i := 0
	for i < len(toks) && toks[i].Kind == token.Ident && toks[i].Value == "import" {
		i += 2 // import "<path>"
		if i < len(toks) && toks[i].Kind == token.Ident && toks[i].Value == "as" {
			i += 2 // as <name>
		}
	}
	if i+1 >= len(toks) || toks[i].Kind != token.Ident || toks[i].Value != "flow" {
		return token.Token{}, token.Token{}, false
	}
	return toks[i], toks[i+1], true
}

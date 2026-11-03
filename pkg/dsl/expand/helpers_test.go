package expand

import (
	"slices"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// run expands main.art from files and returns the canonical text of the
// result and every diagnostic message, one per line.
func run(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	tree, bag := parser.Parse("main.art", files["main.art"])
	if bag.HasErrors() {
		t.Fatalf("main.art does not parse: %v", bag.All())
	}
	res, eb := Expand(tree, MapLoader(files))
	var msgs []string
	for _, d := range eb.All() {
		msgs = append(msgs, d.Span.File+": "+string(d.Code)+": "+d.Message)
	}
	return print.Canonical(res.File), strings.Join(msgs, "\n")
}

// runEdited is run with every item of every file but main.art passed through
// edit after it parses and before it is indexed: a test builds in Go the
// item forms the parser does not read yet. It is Expand's own sequence, with
// the imported files loaded ahead of the root's imports.
func runEdited(t *testing.T, files map[string]string, edit func(*ast.RequestDecl)) (string, string) {
	t.Helper()
	out, eb := expandEdited(t, files, edit)
	var msgs []string
	for _, d := range eb.All() {
		msgs = append(msgs, d.Span.File+": "+string(d.Code)+": "+d.Message)
	}
	return print.Canonical(out), strings.Join(msgs, "\n")
}

// expandEdited is runEdited's expanded file and diagnostics.
func expandEdited(t *testing.T, files map[string]string, edit func(*ast.RequestDecl)) (*ast.File, *diag.Bag) {
	t.Helper()
	tree, bag := parser.Parse("main.art", files["main.art"])
	if bag.HasErrors() {
		t.Fatalf("main.art does not parse: %v", bag.All())
	}
	eb := diag.New()
	e := &expander{
		l: MapLoader(files), bag: eb, res: &Result{File: tree, Sources: map[string]string{}},
		loaded: map[string]*unit{}, loading: []string{"main.art"},
		owner: map[*ast.Collection]*unit{}, literal: map[*ast.StepDecl]bool{},
		standing: map[*ast.Collection]standing{},
	}
	for name, src := range files {
		if name == "main.art" {
			continue
		}
		f, pb := parser.Parse(name, src)
		if pb.HasErrors() {
			t.Fatalf("%s does not parse: %v", name, pb.All())
		}
		for _, d := range f.Scenarios {
			if c, ok := d.(*ast.Collection); ok {
				for _, it := range c.Items {
					if r, ok := it.(*ast.RequestDecl); ok {
						edit(r)
					}
				}
			}
		}
		e.res.Sources[name] = src
		e.loaded[name] = &unit{name: name, tree: f, ok: true}
	}
	for _, u := range e.loaded {
		e.index(u)
	}
	root := &unit{name: "main.art", tree: tree, ok: true}
	e.imports(root)
	e.index(root)
	out := &ast.File{EOF: tree.EOF}
	for _, d := range tree.Scenarios {
		if s, ok := d.(*ast.Scenario); ok {
			cp := *s
			cp.Body = e.body(root, s.Body)
			out.Scenarios = append(out.Scenarios, &cp)
		}
	}
	return out, eb
}

// varInputs is an edit for runEdited: it turns an item's parameters into the
// vars above its action that Task 4's parser will read -- `login(user = "a",
// password)` becomes `var user = "a"` and `var password` -- placed at the
// parameters' names, with the ones named in private made private.
func varInputs(private ...string) func(*ast.RequestDecl) {
	return func(r *ast.RequestDecl) {
		if r.Params == nil {
			return
		}
		for _, p := range r.Params.List {
			at := p.Name.Span
			v := &ast.VarDecl{Secret: p.Secret, Keyword: synth(token.Ident, "var", "var", at), Name: p.Name}
			if !isNil(p.Default) {
				v.Assign, v.Value = p.Assign, p.Default
			}
			if slices.Contains(private, p.Name.Value) {
				v.Private = synth(token.Ident, "private", "private", at)
			}
			r.Inputs = append(r.Inputs, v)
		}
		r.Params = nil
	}
}

func noDiags(t *testing.T, diags string) {
	t.Helper()
	if diags != "" {
		t.Fatalf("unexpected diagnostics:\n%s", diags)
	}
}

// parserParse parses src as main.art.
func parserParse(src string) (*ast.File, *diag.Bag) { return parser.Parse("main.art", src) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

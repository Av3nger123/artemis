package expand

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// The parser does not read flow files or scenario files yet, so these helpers
// build their trees from the old form it does read, and hand the flow files to
// expansion through the preparsed hook.
//
// A flow file is written as an old-form file whose one scenario's title is
// the flow's name and whose body is the flow's. A scenario file is written as
// old-form scenarios whose `var x = v` lines stand for `x = v`, under a first
// line `# flow "<path>"` that stands for the header.

// flowLoader is a MapLoader that hands expansion some files already parsed.
type flowLoader struct {
	MapLoader
	trees map[string]*ast.File
}

func (l flowLoader) preparsed(name string) *ast.File { return l.trees[name] }

// flowEdit changes a flow's tree before it is expanded: what the old form
// cannot say, a private var or one with no value.
type flowEdit func(*ast.Flow)

// private marks the flow's vars of these names private.
func private(names ...string) flowEdit {
	return func(f *ast.Flow) {
		for _, d := range f.Body {
			if v, ok := d.(*ast.VarDecl); ok && slices.Contains(names, v.Name.Value) {
				v.Private = synth(token.Ident, "private", "private", v.Keyword.Span)
			}
		}
	}
}

// required takes the value off the flow's vars of these names.
func required(names ...string) flowEdit {
	return func(f *ast.Flow) {
		for _, d := range f.Body {
			if v, ok := d.(*ast.VarDecl); ok && slices.Contains(names, v.Name.Value) {
				v.Value, v.Assign = nil, token.Token{}
			}
		}
	}
}

// oldFlow is src, an old-form file with one scenario, as a flow file: the
// scenario made a flow named by its title, the imports kept.
func oldFlow(t *testing.T, name, src string, edits ...flowEdit) *ast.File {
	t.Helper()
	tree, bag := parser.Parse(name, src)
	if bag.HasErrors() {
		t.Fatalf("%s does not parse: %v", name, bag.All())
	}
	for i, d := range tree.Scenarios {
		s, ok := d.(*ast.Scenario)
		if !ok {
			continue
		}
		kw := synth(token.Ident, "flow", "flow", s.Keyword.Span)
		kw.Leading = s.Keyword.Leading
		f := &ast.Flow{
			Keyword: kw,
			Name:    synth(token.Ident, s.Name.Value, s.Name.Value, s.Name.Span),
			LBrace:  s.LBrace,
			Body:    s.Body,
			RBrace:  s.RBrace,
		}
		for _, edit := range edits {
			edit(f)
		}
		tree.Scenarios[i] = f
	}
	return tree
}

// scenarioFile is src as main.art, a scenario file: its first line is
// `# flow "<path>"`, and each scenario's var lines are its sets.
func scenarioFile(t *testing.T, src string) *ast.File {
	t.Helper()
	first, _, _ := strings.Cut(src, "\n")
	header, ok := strings.CutPrefix(first, "# flow ")
	if !ok {
		t.Fatalf("a scenario file's first line is # flow \"<path>\", not %q", first)
	}
	tree, bag := parser.Parse("main.art", src)
	if bag.HasErrors() {
		t.Fatalf("main.art does not parse: %v", bag.All())
	}
	at := func(col, n int) token.Span {
		return token.Span{File: "main.art", Line: 1, Col: col, EndLine: 1, EndCol: col + n, Offset: col - 1}
	}
	path := strings.Trim(header, `"`)
	ref := &ast.FlowRef{
		Keyword: synth(token.Ident, "flow", "flow", at(3, 4)),
		Path:    synth(token.String, header, path, at(8, len(header))),
	}
	out := &ast.File{Scenarios: []ast.Decl{ref}, EOF: tree.EOF}
	for _, d := range tree.Scenarios {
		s, ok := d.(*ast.Scenario)
		if !ok {
			out.Scenarios = append(out.Scenarios, d)
			continue
		}
		cp := *s
		cp.Body = nil
		for _, b := range s.Body {
			if v, ok := b.(*ast.VarDecl); ok {
				cp.Body = append(cp.Body, &ast.Set{Name: v.Name, Assign: v.Assign, Value: v.Value})
				continue
			}
			cp.Body = append(cp.Body, b)
		}
		out.Scenarios = append(out.Scenarios, &cp)
	}
	return out
}

// expanded is one scenario file through expansion and the checker, as
// pkg/dsl/front runs them.
type expanded struct {
	out   string // the canonical text of the expanded file
	diags string // every diagnostic, one per line: file:line: code: message
	res   *Result
	bag   *diag.Bag
}

// expandFlows expands main, a scenario file, with files beside it. The files
// named in flows are flow files, written as oldFlow reads them; edits apply
// to each of them.
func expandFlows(t *testing.T, main string, files map[string]string, flows []string, edits ...flowEdit) expanded {
	t.Helper()
	trees := map[string]*ast.File{}
	for _, name := range flows {
		trees[name] = oldFlow(t, name, files[name], edits...)
	}
	res, bag := Expand(scenarioFile(t, main), flowLoader{MapLoader: files, trees: trees})
	_, cb := check.CheckChained(res.File, res.Chain)
	for _, d := range cb.All() {
		d.UsedFrom = res.Chain(d.Span)
		bag.Add(d)
	}
	var msgs []string
	for _, d := range bag.All() {
		msgs = append(msgs, d.Span.File+":"+strconv.Itoa(d.Span.Line)+": "+string(d.Code)+": "+d.Message)
	}
	return expanded{out: print.Canonical(res.File), diags: strings.Join(msgs, "\n"), res: res, bag: bag}
}

// only is the diagnostics of bag with code.
func only(bag *diag.Bag, code diag.Code) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range bag.All() {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

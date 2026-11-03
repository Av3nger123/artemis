package migrate

import (
	"path"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// collections is the collection each of a file's imports names, by the name
// a use reads it by: the import's as, or its file's name. A name whose file
// is not among the inputs maps to nil.
func (m *migration) collections(file string, imports []*ast.Import) map[string]*ast.Collection {
	out := map[string]*ast.Collection{}
	for _, imp := range imports {
		name := imp.Name.Value
		if imp.Name.Text == "" {
			name, _ = parser.ImportName(imp.Path.Value)
		}
		var coll *ast.Collection
		if tree := m.trees[path.Join(path.Dir(file), imp.Path.Value)]; tree != nil {
			for _, d := range tree.Scenarios {
				if c, ok := d.(*ast.Collection); ok {
					coll = c
					break
				}
			}
		}
		out[name] = coll
	}
	return out
}

// pin keeps what a step that calls an item sends.
//
// An old use filled an input it was not passed from the item's default. A
// step fills it from the flow's context first, so an input whose name the
// flow also binds -- a var, or a var a step writes -- would now take the
// flow's value. For each such input the step is passed the default
// explicitly, which is what the old file sent. A default that reads another
// input cannot be written at the step, and is noted instead.
func (m *migration) pin(file string, body []ast.Decl, colls map[string]*ast.Collection) {
	flow := &ast.Flow{Body: body}
	type call struct {
		use  *ast.UseDecl
		item *ast.RequestDecl
	}
	var calls []call
	bound := map[string]bool{}
	for _, d := range body {
		if v, ok := d.(*ast.VarDecl); ok {
			bound[v.Name.Value] = true
		}
	}
	ast.Inspect(flow, func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Capture:
			bound[v.Name.Value] = true
		case *ast.UseDecl:
			coll, known := colls[v.Collection.Value]
			if coll == nil {
				if known || v.Collection.Text != "" {
					m.note("%s: the collection %s is not among the files migrated, so whether the flow's names now replace the defaults of %s is not checked",
						at(file, v.Step), v.Collection.Value, v.Ref())
				}
				return
			}
			item := find(coll, v.Item.Value)
			if item == nil {
				return
			}
			calls = append(calls, call{v, item})
			for _, st := range item.Body {
				if c, ok := st.(*ast.Capture); ok {
					out := c.Name.Value
					if v.Alias.Text != "" {
						out = v.Alias.Value + "_" + out
					}
					bound[out] = true
				}
			}
		}
	})
	for _, c := range calls {
		if c.item.Params == nil {
			continue
		}
		passed := map[string]bool{}
		last := -1
		for i, l := range c.use.Lines {
			if f, ok := l.(*ast.Field); ok && !token.IsUseLine(f.Name.Value) {
				passed[f.Name.Value] = true
				last = i
			}
		}
		var pins []ast.Stmt
		for _, p := range c.item.Params.List {
			name := p.Name.Value
			if p.Default == nil || passed[name] || !bound[name] {
				continue
			}
			if reads(p.Default) {
				m.note("%s: %s's default for %s reads another input, and the flow's own %s now reaches the item in its place; pass %s at the step if that is wrong",
					at(file, c.use.Step), c.use.Ref(), name, name, name)
				continue
			}
			pins = append(pins, &ast.Field{
				Name:   word(token.Ident, name),
				Assign: word(token.Assign, "="),
				Value:  ast.Clone(p.Default, bare),
			})
		}
		if len(pins) == 0 {
			continue
		}
		lines := append([]ast.Stmt{}, c.use.Lines[:last+1]...)
		lines = append(lines, pins...)
		c.use.Lines = append(lines, c.use.Lines[last+1:]...)
	}
}

// find is the request a collection names name, or nil.
func find(c *ast.Collection, name string) *ast.RequestDecl {
	for _, d := range c.Items {
		if r, ok := d.(*ast.RequestDecl); ok && r.Name.Value == name {
			return r
		}
	}
	return nil
}

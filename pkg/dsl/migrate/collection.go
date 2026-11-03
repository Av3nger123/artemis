package migrate

import (
	"path"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// collectionFile is a collection file in the new forms: each item's params
// are var inputs above its action, its captures are vars after it, secret is
// gone, and an item's keyword names its type -- command for a run, query for
// a db -- because an item whose keyword and action disagree no longer
// compiles. The file keeps its path.
func (m *migration) collectionFile(key string, tree *ast.File) *ast.File {
	cp := ast.Clone(tree, identity)
	for _, d := range cp.Scenarios {
		c, ok := d.(*ast.Collection)
		if !ok {
			continue
		}
		for _, it := range c.Items {
			if r, ok := it.(*ast.RequestDecl); ok {
				m.item(key, r)
			}
		}
	}
	dir := path.Dir(key)
	m.convert(cp, key, dir, dir)
	return cp
}

// item rewrites one request in place.
func (m *migration) item(file string, r *ast.RequestDecl) {
	switch r.Action.(type) {
	case *ast.Run:
		r.Keyword = retext(r.Keyword, "command")
	case *ast.DB:
		r.Keyword = retext(r.Keyword, "query")
	}
	if r.Params == nil {
		return
	}
	for _, p := range r.Params.List {
		v := &ast.VarDecl{Keyword: word(token.Ident, "var"), Name: bare(p.Name)}
		v.Keyword = carry(p.Name, carry(p.Secret, v.Keyword))
		if p.Secret.Text != "" {
			m.note("%s: secret is removed from the input %s of %s, and reports no longer withhold its value",
				at(file, p.Secret), p.Name.Value, r.Name.Value)
		}
		if p.Default != nil {
			v.Assign, v.Value = word(token.Assign, "="), p.Default
		}
		r.Inputs = append(r.Inputs, v)
	}
	r.Params = nil
}

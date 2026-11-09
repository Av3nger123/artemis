package migrate

import (
	"fmt"
	"path"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// ids gives every step, item call, scenario and collection of f that is named
// in quotes -- the old form -- a snake_case id, and keeps the quoted text as
// its label: `step "Get a token"` is `step get_a_token as "Get a token"`, so a
// report reads the same before and after. An id is the text made snake_case
// (token.SnakeFrom), numbered _2, _3 when its scope already has it: a flow's
// steps, or a file's scenarios. A collection's id is the name an import with
// no as already gave it, so every import still resolves. A node already named
// by an id is left as it is, which makes a second migration change nothing.
//
// An item or flow named by an identifier that is not snake_case is renamed to
// one, and every step in the run that calls the item follows. An import with
// no as of a collection whose id is not the name the import gave it before
// gains `as <that name>`, so every step that reads it still resolves.
func (m *migration) ids(key string, f *ast.File) {
	imported := m.imports(key, f)
	scenarios := newScope()
	// Every id already written is taken before any is made, so a made id
	// never takes one that a later line of the file holds.
	for _, d := range f.Scenarios {
		if v, ok := d.(*ast.Scenario); ok {
			scenarios.take(v.Name)
		}
	}
	for _, d := range f.Scenarios {
		switch v := d.(type) {
		case *ast.Collection:
			p, _ := m.plan(key)
			switch {
			case v.Name.Kind == token.String:
				v.Name, v.LabelAs, v.Label = relabel(v.Name, p.id)
				m.noteID(key, p)
			case v.Name.Value != p.id:
				v.Name = renamed(v.Name, p.id)
				m.noteID(key, p)
			}
			for _, it := range v.Items {
				if r, ok := it.(*ast.RequestDecl); ok {
					if n, ok := p.renames[r.Name.Value]; ok {
						r.Name = renamed(r.Name, n)
					}
				}
			}
		case *ast.Flow:
			if v.Name.Kind == token.Ident && !token.IsSnakeID(v.Name.Value) {
				v.Name = renamed(v.Name, token.SnakeFrom(v.Name.Value))
			}
			ast.Inspect(v, func(n ast.Node) {
				if u, ok := n.(*ast.UseDecl); ok {
					if p, ok := imported[u.Collection.Value]; ok {
						if n, ok := p.renames[u.Item.Value]; ok {
							u.Item = renamed(u.Item, n)
						}
					}
				}
			})
			steps := newScope()
			for _, b := range v.Body {
				ast.Inspect(b, func(n ast.Node) {
					switch s := n.(type) {
					case *ast.StepDecl:
						steps.take(s.Name)
					case *ast.UseDecl:
						if s.Step.Text != "" {
							steps.take(s.Name)
						}
					}
				})
			}
			for _, b := range v.Body {
				ast.Inspect(b, func(n ast.Node) {
					switch s := n.(type) {
					case *ast.StepDecl:
						s.Name, s.LabelAs, s.Label = steps.id(s.Name, s.LabelAs, s.Label)
					case *ast.UseDecl:
						if s.Step.Text != "" {
							s.Name, s.LabelAs, s.Label = steps.id(s.Name, s.LabelAs, s.Label)
						}
					}
				})
			}
		case *ast.Scenario:
			v.Name, v.LabelAs, v.Label = scenarios.id(v.Name, v.LabelAs, v.Label)
		}
	}
}

// collectionPlan is what migration does to the collection in one file: the
// id it ends with, the name an import with no as reached it by before, and
// the new id of each item whose name was not snake_case.
type collectionPlan struct {
	id, was string
	renames map[string]string
}

// plan is the collection plan of the file key, read from the input; ok is
// false when key holds no collection.
func (m *migration) plan(key string) (collectionPlan, bool) {
	t := m.trees[key]
	if t == nil {
		return collectionPlan{}, false
	}
	for _, d := range t.Scenarios {
		c, ok := d.(*ast.Collection)
		if !ok {
			continue
		}
		p := collectionPlan{renames: map[string]string{}}
		switch {
		case c.Name.Kind == token.String:
			// A file whose name cannot be one was always imported with as.
			if name, ok := parser.ImportName(key); ok {
				p.was = name
			}
			p.id = p.was
			if !token.IsSnakeID(p.id) {
				p.id = token.SnakeFrom(stem(key))
			}
		default:
			p.was = c.Name.Value
			p.id = c.Name.Value
			if !token.IsSnakeID(p.id) {
				p.id = token.SnakeFrom(p.id)
			}
		}
		items := newScope()
		for _, it := range c.Items {
			if r, ok := it.(*ast.RequestDecl); ok && token.IsSnakeID(r.Name.Value) {
				items.take(r.Name)
			}
		}
		for _, it := range c.Items {
			if r, ok := it.(*ast.RequestDecl); ok && !token.IsSnakeID(r.Name.Value) {
				p.renames[r.Name.Value] = items.fresh(token.SnakeFrom(r.Name.Value))
			}
		}
		return p, true
	}
	return collectionPlan{}, false
}

// noteID says that a collection's id is not the name its file was imported
// by: this run's imports are given an as, and one outside it needs one too.
func (m *migration) noteID(key string, p collectionPlan) {
	if p.was != "" && p.was != p.id {
		m.note("%s: the collection is %s now; an import of it outside this migration needs: as %s", key, p.id, parser.IdentLike(p.was))
	}
}

// imports gives each import of f with no as the name its collection was read
// by before, when its id is not that name, and returns the plan of every
// imported collection by the name f's steps read it by.
func (m *migration) imports(key string, f *ast.File) map[string]collectionPlan {
	out := map[string]collectionPlan{}
	for _, d := range f.Scenarios {
		imp, ok := d.(*ast.Import)
		if !ok {
			continue
		}
		p, ok := m.plan(path.Clean(path.Join(path.Dir(key), imp.Path.Value)))
		if !ok {
			continue
		}
		name := imp.Name.Value
		if imp.Name.Text == "" {
			if p.was == "" {
				continue
			}
			name = p.was
			if p.was != p.id {
				imp.As, imp.Name = word(token.Ident, "as"), word(token.Ident, p.was)
			}
		}
		out[name] = p
	}
	return out
}

// renamed is old with its text replaced by id, keeping its comments.
func renamed(old token.Token, id string) token.Token {
	t := word(token.Ident, id)
	t.Leading, t.Trailing = old.Leading, old.Trailing
	return t
}

// scope is the ids one scope has given out.
type scope map[string]bool

func newScope() scope { return scope{} }

// fresh is base, numbered _2, _3 when the scope has it, and takes it.
func (s scope) fresh(base string) string {
	id := base
	for n := 2; s[id]; n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	s[id] = true
	return id
}

// take reserves name when it is already an id.
func (s scope) take(name token.Token) {
	if name.Kind == token.Ident {
		s[name.Value] = true
	}
}

// id is the id, as and label for a node named name. A quoted name becomes an
// id the scope has not given out, and its label; an id is kept, and taken.
func (s scope) id(name, as, label token.Token) (token.Token, token.Token, token.Token) {
	if name.Kind != token.String {
		s[name.Value] = true
		return name, as, label
	}
	base := token.SnakeFrom(name.Value)
	id := base
	for n := 2; s[id]; n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	s[id] = true
	return relabel(name, id)
}

// relabel is the id that replaces the quoted name old, with old's comments in
// front of it, and old itself as the label after as -- unless old already
// says the id, which then reads the same with no label.
func relabel(old token.Token, id string) (name, as, label token.Token) {
	name = word(token.Ident, id)
	name.Leading = old.Leading
	if old.Value == id {
		name.Trailing = old.Trailing
		return name, token.Token{}, token.Token{}
	}
	label = old
	label.Leading = nil
	return name, word(token.Ident, "as"), label
}

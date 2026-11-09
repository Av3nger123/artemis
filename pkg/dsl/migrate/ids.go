package migrate

import (
	"fmt"

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
func (m *migration) ids(key string, f *ast.File) {
	scenarios := newScope()
	for _, d := range f.Scenarios {
		switch v := d.(type) {
		case *ast.Collection:
			if v.Name.Kind == token.String {
				v.Name, v.LabelAs, v.Label = m.collectionID(key, v.Name)
			}
		case *ast.Flow:
			steps := newScope()
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

// collectionID is the id and label of a collection titled title in the file
// key: the file's import name when that is an id, and else the file's name
// made one, with a note that the imports that read it need an as.
func (m *migration) collectionID(key string, title token.Token) (name, as, label token.Token) {
	id, ok := parser.ImportName(key)
	if !ok || !token.IsSnakeID(id) {
		was := id
		id = token.SnakeFrom(stem(key))
		m.note("%s: the collection is %s now; an import that read it as %s needs: as %s", key, id, was, parser.IdentLike(was))
	}
	return relabel(title, id)
}

// scope is the ids one scope has given out.
type scope map[string]bool

func newScope() scope { return scope{} }

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

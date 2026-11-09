package front

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// checkIDs holds every id in tree, the file as written, to the two rules
// every collection, item, flow, step and scenario id follows: it is
// snake_case (id-not-snake-case), and it is the only one of its kind in its
// scope (duplicate-id). A scope is a collection's items, a flow's steps --
// setup, teardown and every block included, because a step's id names its
// trace and its report entry wherever it runs -- or a file's scenarios.
//
// A quoted name is the old form, which oldForms reports, and a name with no
// span was made by expansion, not written: neither is checked here.
func checkIDs(tree *ast.File, bag *diag.Bag) {
	if tree == nil {
		return
	}
	scenarios := ids{bag: bag, what: "scenario"}
	for _, d := range tree.Scenarios {
		switch v := d.(type) {
		case *ast.Collection:
			snake(bag, "collection", v.Name)
			items := ids{bag: bag, what: "item"}
			for _, it := range v.Items {
				if r, ok := it.(*ast.RequestDecl); ok {
					items.add(r.Keyword.Value, r.Name)
				}
			}
		case *ast.Flow:
			snake(bag, "flow", v.Name)
			steps := ids{bag: bag, what: "step"}
			for _, b := range v.Body {
				ast.Inspect(b, func(n ast.Node) {
					switch s := n.(type) {
					case *ast.StepDecl:
						steps.add("step", s.Name)
					case *ast.UseDecl:
						if s.Step.Text != "" {
							steps.add("step", s.Name)
						}
					}
				})
			}
		case *ast.Scenario:
			scenarios.add("scenario", v.Name)
		}
	}
}

// ids is one scope: the ids seen in it so far, by the token that took each.
type ids struct {
	bag  *diag.Bag
	what string
	seen map[string]token.Token
}

func (s *ids) add(what string, name token.Token) {
	if !written(name) {
		return
	}
	snake(s.bag, what, name)
	if s.seen == nil {
		s.seen = map[string]token.Token{}
	}
	if first, ok := s.seen[name.Value]; ok {
		s.bag.Error(name.Span, diag.DuplicateID, "%s is already the id of the %s on line %d", name.Value, s.what, first.Span.Line).
			Hintf("give one of them another id; the label after as may still say the same")
		return
	}
	s.seen[name.Value] = name
}

// snake reports name when it is an identifier and not a snake_case id.
func snake(bag *diag.Bag, what string, name token.Token) {
	if !written(name) || token.IsSnakeID(name.Value) {
		return
	}
	bag.Error(name.Span, diag.IDNotSnakeCase, "the %s id %s is not snake_case", what, name.Value).
		Hintf("write it %s, and put the text people read in a label: %s %s as \"...\"", token.SnakeFrom(name.Value), what, token.SnakeFrom(name.Value))
}

// written reports whether name is an id someone wrote: an identifier with a
// place in a file.
func written(name token.Token) bool {
	return name.Kind == token.Ident && !name.Span.IsZero()
}

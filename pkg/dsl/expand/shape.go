package expand

import (
	"path"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// checkShape holds a collection file to its shape: one collection, named
// after the file, and nothing else to run. Then `import "orders.art"` puts
// exactly `orders` in scope -- the import line says what a scenario gets --
// and the file, the collection and every `use orders.x` share one name. A
// file with no collection is not a collection file and is left alone.
func (e *expander) checkShape(u *unit) {
	var colls []*ast.Collection
	var scenarios []*ast.Scenario
	for _, d := range u.tree.Scenarios {
		switch v := d.(type) {
		case *ast.Collection:
			colls = append(colls, v)
		case *ast.Scenario:
			scenarios = append(scenarios, v)
		}
	}
	if len(colls) == 0 {
		return
	}
	base := path.Base(filepath.ToSlash(u.name))
	// orders.art and orders.collection.art (what artemis generate writes) both hold orders.
	stem := strings.TrimSuffix(strings.TrimSuffix(base, path.Ext(base)), ".collection")

	first := colls[0]
	if name := first.Name.Value; name != stem && parser.IsIdentName(name) {
		if parser.IsIdentName(stem) {
			e.bag.Error(first.Name.Span, diag.CollectionFileName, "collection %q is in %s: a collection is named after its file", name, base).
				Hintf("name it %s, or rename the file to %s.art", stem, name)
		} else {
			e.bag.Error(first.Name.Span, diag.CollectionFileName, "collection %q is in %s: a collection is named after its file, and %q cannot name one", name, base, stem).
				Hintf("rename the file to %s.art", name)
		}
	}
	for _, c := range colls[1:] {
		e.bag.Error(c.Name.Span, diag.SecondCollection, "%s holds a second collection, %q: a file holds one collection", base, c.Name.Value).
			Hintf("move it to a file of its own, %s.art", parser.IdentLike(c.Name.Value))
	}
	for _, s := range scenarios {
		e.bag.Error(s.Name.Span, diag.ScenarioInCollectionFile, "scenario %q is in collection file %s: a collection file holds only its collection", s.Name.Value, base).
			Hintf("move the scenario to a scenario file, which imports %s", base)
	}
}

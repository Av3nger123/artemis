package expand

import (
	"path"
	"path/filepath"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// checkShape holds a collection file to its shape: one collection, and
// nothing else to run. Then an import names exactly one collection, the
// file's own, by the name the import gives it. A file with no collection is
// not a collection file and is left alone.
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
	for _, c := range colls[1:] {
		e.bag.Error(c.Name.Span, diag.SecondCollection, "%s holds a second collection, %q: a file holds one collection", base, c.Name.Value).
			Hintf("move it to a file of its own")
	}
	for _, s := range scenarios {
		e.bag.Error(s.Name.Span, diag.ScenarioInCollectionFile, "scenario %q is in collection file %s: a collection file holds only its collection", s.Name.Value, base).
			Hintf("move the scenario to a scenario file, which imports %s", base)
	}
}

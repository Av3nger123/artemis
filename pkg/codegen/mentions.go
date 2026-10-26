package codegen

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lower"
)

// mentions reports whether any expect or capture of st names root.
//
// The roots a step binds for every reader -- status, body, raw, headers, a
// terminal's three streams -- are bound on every step, because nearly every
// step reads one. `cookies` and `duration_ms` are read by few steps and cost a
// line each to bind, so a target binds them only for the step that names one:
// a generated test reads like the .art did, not like the runner's internals.
func mentions(st *lower.Step, root string) bool {
	found := false
	visit := func(x ast.Expr) {
		if x == nil || found {
			return
		}
		ast.Inspect(x, func(n ast.Node) {
			if id, ok := n.(*ast.Ident); ok && id.Name() == root {
				found = true
			}
		})
	}
	for _, e := range st.Expects {
		visit(e.Value)
	}
	for _, c := range st.Captures {
		visit(c.Value)
	}
	return found
}

// httpCookies is the scenario's `config http { cookies = ... }` setting: the
// expression, or nil for the default, which is a jar.
func httpCookies(sc *lower.Scenario) ast.Expr {
	if sc.HTTP == nil {
		return nil
	}
	return sc.HTTP.Cookies
}

// hasAPIStep reports whether sc sends any request, which is what decides
// whether it needs a session at all.
func hasAPIStep(sc *lower.Scenario) bool {
	for _, st := range sc.Steps {
		if st.Request != nil {
			return true
		}
	}
	return false
}

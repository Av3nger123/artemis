package codegen

import (
	"fmt"

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
	for _, st := range allSteps(sc) {
		if st.Request != nil {
			return true
		}
	}
	return false
}

// allSteps is every step of sc in the order it runs: setup, its own, teardown.
func allSteps(sc *lower.Scenario) []*lower.Step {
	out := make([]*lower.Step, 0, len(sc.Setup)+len(sc.Steps)+len(sc.Teardown))
	out = append(out, sc.Setup...)
	out = append(out, sc.Steps...)
	return append(out, sc.Teardown...)
}

// refuseSuites is an error for a file a target cannot export faithfully yet: a
// suite.art, or a scenario that reads `suite`. A suite's setup runs once for a
// whole folder under the interpreter, and an export of one scenario has no such
// place to run it -- emitting `suite["token"]` with nothing defining it would
// be a module that fails on its first line. The error says what is missing.
func refuseSuites(tree *ast.File, scenarios []*lower.Scenario) error {
	if tree == nil {
		return nil
	}
	for _, d := range tree.Scenarios {
		if s, ok := d.(*ast.Scenario); ok && s.Keyword.Value == "suite" {
			return fmt.Errorf("line %d: a suite.art runs once for its folder, and artemis build exports one file's scenarios; it does not export suites yet", s.Keyword.Span.Line)
		}
	}
	for _, sc := range scenarios {
		declared := false
		for _, v := range sc.Vars {
			declared = declared || v.Name == "suite"
		}
		if declared {
			continue
		}
		for _, st := range allSteps(sc) {
			if mentions(st, "suite") || readsInAction(st, "suite") {
				return fmt.Errorf("line %d: scenario %q reads suite.<name> from a suite.art, which artemis build does not export yet", st.Line, sc.Name)
			}
		}
		for _, v := range sc.Vars {
			if readsName(v.Value, "suite") {
				return fmt.Errorf("line %d: scenario %q reads suite.<name> from a suite.art, which artemis build does not export yet", v.Line, sc.Name)
			}
		}
	}
	return nil
}

// readsInAction reports whether a step's action reads name: its URL, headers,
// body, command, arguments or browser acts.
func readsInAction(st *lower.Step, name string) bool {
	var exprs []ast.Expr
	if r := st.Request; r != nil {
		exprs = append(exprs, r.URL, r.Body)
		for _, ps := range [][]*lower.Param{r.Headers, r.Query, r.Form, r.Multipart} {
			for _, p := range ps {
				exprs = append(exprs, p.Key, p.Value)
			}
		}
	}
	if r := st.Run; r != nil {
		exprs = append(exprs, r.Command, r.Args, r.Cwd, r.Stdin)
		for _, p := range r.Env {
			exprs = append(exprs, p.Value)
		}
	}
	for _, a := range st.Acts {
		exprs = append(exprs, a.Target, a.Value)
	}
	for _, x := range exprs {
		if readsName(x, name) {
			return true
		}
	}
	return false
}

// readsName reports whether x names name anywhere in it.
func readsName(x ast.Expr, name string) bool {
	if x == nil {
		return false
	}
	found := false
	ast.Inspect(x, func(n ast.Node) {
		if id, ok := n.(*ast.Ident); ok && id.Name() == name {
			found = true
		}
	})
	return found
}

package expand

import (
	"maps"

	"artemis/pkg/dsl/ast"
)

// prefix is what a use's step is named: the step form's own name, else its
// alias when it has one, else the ref as the use writes it, "orders.create".
func prefix(use *ast.UseDecl, item string) string {
	if use.Name.Text != "" {
		return use.StepName()
	}
	if use.Alias.Text != "" {
		return use.Alias.Value
	}
	return use.Collection.Value + "." + item
}

// renameCaptures applies `as alias` to the step one use expanded to: every
// capture it makes becomes alias_name.
//
// Only template material is rewritten: an identifier whose Via is this use's
// via. An argument written at the use keeps the Via of the use line, which is
// outside this use, so it is read in the terms of the scenario around the use
// and is left alone.
func (e *expander) renameCaptures(steps []*ast.StepDecl, alias string, via int) {
	seen := map[string]bool{}
	for _, s := range steps {
		var caps []*ast.Capture
		for _, st := range s.Body {
			if c, ok := st.(*ast.Capture); ok {
				caps = append(caps, c)
			}
		}
		// A step's action and expects run before its captures, so only a
		// later step reads what this one captures.
		e.renameReads(s, seen, alias, via)
		for _, c := range caps {
			seen[c.Name.Value] = true
			n := alias + "_" + c.Name.Value
			c.Name.Text, c.Name.Value = n, n
		}
	}
}

// renameReads rewrites, in n, every template identifier naming one of names
// to alias_name. A call's callee names a builtin and is never a read.
func (e *expander) renameReads(n ast.Node, names map[string]bool, alias string, via int) {
	callee := map[*ast.Ident]bool{}
	ast.Inspect(n, func(x ast.Node) {
		switch v := x.(type) {
		case *ast.Call:
			callee[v.Callee] = true
		case *ast.Ident:
			if callee[v] || !names[v.Tok.Value] || !e.within(v.Tok.Span.Via, via) {
				return
			}
			nm := alias + "_" + v.Tok.Value
			v.Tok.Text, v.Tok.Value = nm, nm
		}
	})
}

// within reports whether a token stamped with Via v was brought in by use via.
func (e *expander) within(v, via int) bool {
	return v == via
}

// scope is what a use can read: the scenario's vars and the captures above
// the use, which bind fills a missing input from.
type scope struct {
	vars  map[string]bool // every scenario var
	above map[string]bool // every capture written above this point, at any depth
}

// newScope is the scope of a scenario body: its vars.
func newScope(decls []ast.Decl) *scope {
	sc := &scope{vars: map[string]bool{}, above: map[string]bool{}}
	for _, d := range decls {
		if v, ok := d.(*ast.VarDecl); ok {
			sc.vars[v.Name.Value] = true
		}
	}
	return sc
}

// has reports whether name is in the context at this point: a var of the
// scenario, which binds it everywhere, or a capture written above, in any
// step, phase or block branch before this point. A capture in only one branch
// of an if counts: the name is read as written, and the checker says whether
// it is bound on every path (capture-maybe-unbound).
func (sc *scope) has(name string) bool {
	return sc.vars[name] || sc.above[name]
}

// below is sc for a body nested under the given decls -- a phase's or a
// block clause's, or a use's below the steps beside it -- which can read
// every capture they write.
func (sc *scope) below(decls []ast.Decl) *scope {
	cp := *sc
	cp.above = maps.Clone(sc.above)
	captured(decls, cp.above)
	return &cp
}

// captured adds to names every capture decls write, at any depth.
func captured(decls []ast.Decl, names map[string]bool) {
	for _, d := range decls {
		ast.Inspect(d, func(n ast.Node) {
			if c, ok := n.(*ast.Capture); ok {
				names[c.Name.Value] = true
			}
		})
	}
}

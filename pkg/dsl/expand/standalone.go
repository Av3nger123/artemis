package expand

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// A collection's standing: whether its standalone check has run, and how it
// went. The zero value is unchecked.
type standing uint8

const (
	unchecked standing = iota
	checking           // its check is running: a sibling use inside it is expanded as is
	sound              // checked without error
	broken             // checked with an error, which has been reported; never expanded
)

// checkCollection checks every item of c on its own, once: each becomes a
// synthetic scenario that goes through check.Check, and the checker's
// diagnostics are reported against the collection file. It reports whether c
// is sound; a broken collection is never expanded, so its errors are not
// reported again per use.
//
// A collection is checked when its file is indexed, or earlier, when a flow
// being checked uses it first.
func (e *expander) checkCollection(u *unit, c *ast.Collection) bool {
	switch e.standing[c] {
	case sound, checking:
		return true
	case broken:
		return false
	}
	if !u.ok {
		// The parser has reported it; checking what it recovered would only
		// cascade. resolve already refuses a use of it.
		e.standing[c] = broken
		return false
	}
	e.standing[c] = checking
	for _, d := range c.Items {
		if !e.checkItem(u, c, d) {
			e.standing[c] = broken
		}
	}
	if e.standing[c] == checking {
		e.standing[c] = sound
	}
	return e.standing[c] == sound
}

// checkItem checks one request or flow of c and reports whether it is sound.
//
// Expanding a flow's nested uses writes to the use table, the bag and the
// hoisted vars; here all three are scratch. The steps built are thrown away,
// so their Via must index nothing in the real table, and the checker's
// diagnostics are reported with no Via: they point at the collection as
// written, not at a use.
func (e *expander) checkItem(u *unit, c *ast.Collection, item ast.Decl) bool {
	var name *token.Token
	var params *ast.Params
	switch it := item.(type) {
	case *ast.RequestDecl:
		name, params = &it.Name, it.Params
	case *ast.FlowDecl:
		name, params = &it.Name, it.Params
	default:
		return true
	}

	uses, bag, silent := e.res.Uses, e.bag, e.silent
	e.res.Uses, e.bag = nil, diag.New()
	defer func() { e.res.Uses = uses }()

	vars := paramVars(params)
	var steps []ast.Decl
	sc := newScope(vars)
	switch it := item.(type) {
	case *ast.RequestDecl:
		steps = []ast.Decl{requestStep(it, it.Name.Value, nil, identity)}
	case *ast.FlowDecl:
		stack := []string{c.Name.Value + "." + it.Name.Value}
		for _, d := range it.Body {
			switch v := d.(type) {
			case *ast.StepDecl:
				steps = append(steps, ast.Clone(v, identity))
			case *ast.UseDecl:
				// The flow's parameters are vars of the synthetic scenario, so
				// an argument naming one needs no substitution.
				use := ast.Clone(v, identity)
				steps = append(steps, e.expandUse(u, c, use, 0, stack, sc.at(nil, steps))...)
			}
		}
	}

	e.rootCaptures(item)
	ok := !e.bag.HasErrors() && e.silent == silent
	if ok {
		// Hoisted secret vars go above the steps that read them: the checker
		// binds a var for what comes after it.
		body := append(append(vars, *sc.hoisted...), steps...)
		file := &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
			Keyword: synth(token.Ident, "scenario", "scenario", token.Span{}),
			Name:    synth(token.String, quote(c.Name.Value+"."+name.Value), c.Name.Value+"."+name.Value, token.Span{}),
			Body:    body,
		}}}
		_, cb := check.Check(file)
		e.bag.Merge(cb)
		ok = !e.bag.HasErrors()
	}

	scratch := e.bag
	e.bag, e.silent = bag, silent
	for _, d := range scratch.All() {
		d.Span.Via = 0
		e.bag.Add(d)
	}
	return ok
}

// paramVars is a var for each parameter, in order: its default as its value,
// or null for a required one, `secret` carried over. Each is placed at the
// parameter's name, so a diagnostic on it points at the parameter.
func paramVars(params *ast.Params) []ast.Decl {
	if params == nil {
		return nil
	}
	out := make([]ast.Decl, 0, len(params.List))
	for _, p := range params.List {
		at := p.Name.Span
		var value ast.Expr = &ast.Literal{Tok: synth(token.Null, "null", "", at)}
		if !isNil(p.Default) {
			value = ast.Clone(p.Default, identity)
		}
		out = append(out, &ast.VarDecl{
			Secret:  p.Secret,
			Keyword: synth(token.Ident, "var", "var", at),
			Name:    p.Name,
			Assign:  synth(token.Assign, "=", "", at),
			Value:   value,
		})
	}
	return out
}

// observationRoots is every step type's observation roots: body, status,
// stdout, page and the rest.
var observationRoots = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range []check.StepType{check.API, check.Terminal, check.Browser} {
		for _, r := range check.Roots(t) {
			m[r] = true
		}
	}
	return m
}()

// rootCaptures reports every capture item writes itself -- not a nested
// use's, which its own collection's check covers -- that is named after an
// observation root. `as` renames a use's captures and every template read of
// them, and a later step's read of the root itself is one: under `as z`,
// `capture body = ...` then `expect body.y == 1` would read z_body.y.
func (e *expander) rootCaptures(item ast.Decl) {
	var bodies [][]ast.Stmt
	switch it := item.(type) {
	case *ast.RequestDecl:
		bodies = append(bodies, it.Body)
	case *ast.FlowDecl:
		for _, d := range it.Body {
			if s, ok := d.(*ast.StepDecl); ok {
				bodies = append(bodies, s.Body)
			}
		}
	}
	for _, body := range bodies {
		for _, st := range body {
			c, ok := st.(*ast.Capture)
			if !ok || !observationRoots[c.Name.Value] {
				continue
			}
			e.bag.Error(c.Name.Span, diag.RootCapture, "a collection may not capture %q: it names a step's observation", c.Name.Value).
				Hintf("rename it, e.g. %s_value; a later step's `%s` would be ambiguous", c.Name.Value, c.Name.Value)
		}
	}
}

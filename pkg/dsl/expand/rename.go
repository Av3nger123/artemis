package expand

import (
	"maps"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/taint"
	"artemis/pkg/dsl/token"
)

// prefix is what a use's step is named: its alias when it has one, else the
// ref as the use writes it, "orders.create".
func prefix(use *ast.UseDecl, item string) string {
	if use.Alias.Text != "" {
		return use.Alias.Value
	}
	return use.Collection.Value + "." + item
}

// varPrefix is what a hoisted secret var is named after: the use's alias, or
// the request's bare name.
func varPrefix(use *ast.UseDecl, item string) string {
	if use.Alias.Text != "" {
		return use.Alias.Value
	}
	return item
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

// scope is what a use's arguments can read, for secret parameters: the
// scenario's vars (hoisted ones included) and the bindings that are secret
// above the use.
type scope struct {
	vars       map[string]bool // every scenario var, hoisted ones included
	secretVars map[string]bool // the secret ones
	secrets    map[string]bool // secretVars plus the secret captures above this use
	hoisted    *[]ast.Decl     // secret vars to append to the scenario body, in use order
}

// newScope is the scope of a scenario body: its vars, and a hoisted list.
func newScope(decls []ast.Decl) *scope {
	sc := &scope{vars: map[string]bool{}, secretVars: map[string]bool{}, hoisted: &[]ast.Decl{}}
	for _, d := range decls {
		if v, ok := d.(*ast.VarDecl); ok {
			sc.vars[v.Name.Value] = true
			if v.Secret.Text != "" {
				sc.secretVars[v.Name.Value] = true
			}
		}
	}
	return sc
}

// at is sc for a use below the given steps, which are written in the same
// terms as the use: their secret captures are secret there too.
func (sc *scope) at(base map[string]bool, steps []ast.Decl) *scope {
	secrets := maps.Clone(sc.secretVars)
	maps.Copy(secrets, base)
	for _, d := range steps {
		s, ok := d.(*ast.StepDecl)
		if !ok {
			continue
		}
		for _, st := range s.Body {
			if c, ok := st.(*ast.Capture); ok && c.Secret.Text != "" {
				secrets[c.Name.Value] = true
			}
		}
	}
	cp := *sc
	cp.secrets = secrets
	return &cp
}

// secretArg is what a secret parameter p stands for at this use, given the
// expression x bound to it. An x that reads only scenario vars is hoisted
// into `secret var <prefix>_<param> = x` and the parameter reads that var;
// one that reads a capture stays as written, and must already be secret.
//
// The var is returned for bind to append to the scenario once the whole use
// has bound; its name is in sc from now on, so a later default can read it.
func (e *expander) secretArg(sc *scope, p *ast.Param, x ast.Expr, pre, ref string, stamp func(token.Token) token.Token) (ast.Expr, *ast.VarDecl) {
	if free := firstFree(x, sc.vars); free != "" {
		if !taint.Secret(x, sc.secrets) {
			e.bag.Error(x.Span(), diag.SecretArgument, "%s is secret in %s, so its argument must be secret too", p.Name.Value, ref).
				Hintf("make %s a secret capture", free)
		}
		return x, nil
	}
	name := pre + "_" + p.Name.Value
	at := stamp(p.Name).Span
	v := &ast.VarDecl{
		Secret:  stamp(p.Secret),
		Keyword: synth(token.Ident, "var", "var", at),
		Name:    synth(token.Ident, name, name, at),
		Assign:  synth(token.Assign, "=", "", at),
		Value:   x,
	}
	sc.vars[name] = true
	sc.secretVars[name] = true
	sc.secrets[name] = true
	return &ast.Ident{Tok: synth(token.Ident, name, name, x.Span())}, v
}

// firstFree is the first identifier x reads that is not one of vars -- a
// capture, as far as a secret argument is concerned -- or "" when x reads
// only vars, literals and builtin calls.
func firstFree(x ast.Expr, vars map[string]bool) string {
	free := ""
	callee := map[*ast.Ident]bool{}
	ast.Inspect(x, func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Call:
			callee[v.Callee] = true
		case *ast.Ident:
			if free == "" && !callee[v] && !vars[v.Tok.Value] {
				free = v.Tok.Value
			}
		}
	})
	return free
}

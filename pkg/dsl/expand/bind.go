package expand

import (
	"reflect"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// bind matches a use's argument lines to the item's parameters and returns
// the expression each parameter stands for at this use, already substituted
// so a default that names an earlier parameter sees that parameter's value.
//
// An argument is the expression written at the use and keeps its own span; a
// default is copied out of the collection and stamped.
func (e *expander) bind(use *ast.UseDecl, item ast.Decl, params *ast.Params, stamp func(token.Token) token.Token) (map[string]ast.Expr, bool) {
	var list []*ast.Param
	if params != nil {
		list = params.List
	}
	declared := map[string]bool{}
	var names []string
	for _, p := range list {
		declared[p.Name.Value] = true
		names = append(names, p.Name.Value)
	}

	ok := true
	args := map[string]*ast.Field{}
	for _, f := range arguments(use) {
		name := f.Name.Value
		switch {
		case f.Key != nil || f.Block != nil:
			e.bag.Error(f.Span(), diag.BadOverride, "an argument is name = value")
			ok = false
		case !declared[name]:
			r := e.bag.Error(f.Name.Span, diag.UnknownArgument, "%s has no parameter %q", use.Ref(), name)
			if !r.DidYouMean(name, names) {
				r.Hintf("%s", takes(itemName(item), names))
			}
			ok = false
		case args[name] != nil:
			e.bag.Error(f.Name.Span, diag.DuplicateArgument, "%s is passed twice", name)
			ok = false
		default:
			args[name] = f
		}
	}

	env := map[string]ast.Expr{}
	var missing, required []string
	for _, p := range list {
		name := p.Name.Value
		switch {
		case args[name] != nil:
			env[name] = args[name].Value
		case !isNil(p.Default):
			env[name] = subst(ast.Clone(p.Default, stamp), env)
		default:
			missing = append(missing, name)
		}
		if isNil(p.Default) {
			required = append(required, name)
		}
	}
	if len(missing) > 0 {
		e.bag.Error(use.Item.Span, diag.MissingArgument, "%s needs %s", use.Ref(), strings.Join(missing, ", ")).
			Hintf("%s requires %s", use.Ref(), strings.Join(required, ", "))
		ok = false
	}
	return env, ok
}

// arguments is the use block's argument lines: the fields that are not an
// override word.
func arguments(use *ast.UseDecl) []*ast.Field {
	var out []*ast.Field
	for _, l := range use.Lines {
		if f, ok := l.(*ast.Field); ok && !token.IsUseLine(f.Name.Value) {
			out = append(out, f)
		}
	}
	return out
}

// takes is the hint listing what an item accepts: "login takes user, password".
func takes(item string, names []string) string {
	if len(names) == 0 {
		return item + " takes no arguments"
	}
	return item + " takes " + strings.Join(names, ", ")
}

// subst returns x with every identifier that names a parameter replaced by
// that parameter's bound expression. Only bare identifiers are parameters:
// the X of a member access `p.field` is substituted (so p can be an object),
// but a member's Name is not, and neither is a call's callee -- env is a
// builtin, never a parameter.
//
// x is rewritten in place, so callers pass a copy; the replacements are fresh
// copies of env's values, one per occurrence.
func subst(x ast.Expr, env map[string]ast.Expr) ast.Expr {
	if isNil(x) {
		return x
	}
	switch v := x.(type) {
	case *ast.Ident:
		if r, ok := env[v.Tok.Value]; ok && !isNil(r) {
			return ast.Clone(r, identity)
		}
	case *ast.Interp:
		for i := range v.Segments {
			v.Segments[i].Expr = subst(v.Segments[i].Expr, env)
		}
	case *ast.Unary:
		v.X = operand(v.X, env)
	case *ast.Binary:
		v.X = operand(v.X, env)
		v.Y = operand(v.Y, env)
	case *ast.Exists:
		v.X = operand(v.X, env)
	case *ast.IsType:
		v.X = operand(v.X, env)
	case *ast.Member:
		v.X = operand(v.X, env)
	case *ast.Index:
		v.X = operand(v.X, env)
		v.Index = subst(v.Index, env)
	case *ast.Call:
		for i := range v.Args {
			v.Args[i].Value = subst(v.Args[i].Value, env)
		}
	case *ast.Object:
		for i := range v.Entries {
			v.Entries[i].Key = subst(v.Entries[i].Key, env)
			v.Entries[i].Value = subst(v.Entries[i].Value, env)
		}
	case *ast.Array:
		for i := range v.Elems {
			v.Elems[i].Value = subst(v.Elems[i].Value, env)
		}
	case *ast.Paren:
		v.X = subst(v.X, env)
	}
	return x
}

// operand is subst for an operand of an operator: a replacement that is
// itself an operation is parenthesised, so `qty * 2` with qty = a + 1 reads
// (a + 1) * 2.
func operand(x ast.Expr, env map[string]ast.Expr) ast.Expr {
	r := subst(x, env)
	if r == x || atomic(r) {
		return r
	}
	sp := r.Span()
	open := token.Span{File: sp.File, Line: sp.Line, Col: sp.Col, EndLine: sp.Line, EndCol: sp.Col, Offset: sp.Offset, Via: sp.Via}
	shut := token.Span{File: sp.File, Line: sp.EndLine, Col: sp.EndCol, EndLine: sp.EndLine, EndCol: sp.EndCol, Offset: sp.Offset, Via: sp.Via}
	return &ast.Paren{
		LParen: synth(token.LParen, "(", "", open),
		X:      r,
		RParen: synth(token.RParen, ")", "", shut),
	}
}

// atomic reports whether x binds tighter than any operator, so it needs no
// parentheses wherever it lands.
func atomic(x ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.Literal, *ast.Paren, *ast.Member, *ast.Index, *ast.Call,
		*ast.Interp, *ast.Object, *ast.Array:
		return true
	}
	return false
}

// substStep substitutes env into every expression of a copied step: the
// action's, every expect's and capture's value, and every field's. A
// capture's name is never a parameter.
func substStep(s *ast.StepDecl, env map[string]ast.Expr) {
	switch a := s.Action.(type) {
	case *ast.Request:
		a.URL = subst(a.URL, env)
		substBlock(a.Block, env)
	case *ast.Run:
		a.Command = subst(a.Command, env)
		substBlock(a.Block, env)
	case *ast.Browser:
		substLines(a.Acts, env)
	}
	substLines(s.Body, env)
}

// substLines substitutes env into a list of statements.
func substLines(lines []ast.Stmt, env map[string]ast.Expr) {
	for _, l := range lines {
		switch v := l.(type) {
		case *ast.Expect:
			v.Value = subst(v.Value, env)
			v.Budget = subst(v.Budget, env)
		case *ast.Capture:
			v.Value = subst(v.Value, env)
		case *ast.Field:
			v.Key = subst(v.Key, env)
			v.Value = subst(v.Value, env)
			substBlock(v.Block, env)
		case *ast.BrowserAct:
			v.Target = subst(v.Target, env)
			v.Value = subst(v.Value, env)
		case *ast.BodySet:
			v.Value = subst(v.Value, env)
		case *ast.In:
			substLines(v.Lines, env)
		}
	}
}

func substBlock(b *ast.Block, env map[string]ast.Expr) {
	if b != nil {
		substLines(b.Fields, env)
	}
}

// isNil reports whether x holds no node, interface nil or typed nil alike.
func isNil(x ast.Node) bool {
	if x == nil {
		return true
	}
	v := reflect.ValueOf(x)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

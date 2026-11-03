package expand

import (
	"fmt"
	"reflect"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// input is one value an item takes: a parameter of the old form,
// `request r(user, password = "pw")`, or a var above the item's action,
// `var user` or `private var tries = 1`. bind reads both through it.
type input struct {
	Name    token.Token
	Default ast.Expr // nil when required
	Private bool     // takes only its own default: no argument, no context name
}

// inputs is what r takes, in the order it declares them: its parameters, then
// the vars above its action. An item has one or the other.
func inputs(r *ast.RequestDecl) []input {
	var out []input
	if r.Params != nil {
		for _, p := range r.Params.List {
			out = append(out, input{Name: p.Name, Default: p.Default})
		}
	}
	for _, v := range r.Inputs {
		out = append(out, input{Name: v.Name, Default: v.Value, Private: v.Private.Text != ""})
	}
	return out
}

// bind matches a use's argument lines to the item's inputs and returns the
// expression each input stands for at this use, already substituted so a
// default that names an earlier input sees that input's value.
//
// An input takes the first of these that has a value: the argument the use
// passes, the name in the context at the use -- a var of the scenario, or a
// capture above the use, as sc.has reports -- and the item's own default.
// With none, it is missing-argument. A private input takes only its default.
//
// An argument is the expression written at the use and keeps its own span; a
// default is copied out of the collection and stamped. A context name is the
// bare identifier, placed where the item reads the input: the checker then
// judges whether the name is bound at that point, and a diagnostic on it
// points into the item with the use as its chain.
func (e *expander) bind(use *ast.UseDecl, item ast.Decl, list []input, sc *scope, stamp func(token.Token) token.Token) (map[string]ast.Expr, bool) {
	declared := map[string]input{}
	var names []string
	for _, in := range list {
		declared[in.Name.Value] = in
		if !in.Private {
			names = append(names, in.Name.Value)
		}
	}

	ok := true
	args := map[string]*ast.Field{}
	for _, f := range arguments(use) {
		name := f.Name.Value
		in, isInput := declared[name]
		switch {
		case f.Key != nil || f.Block != nil:
			e.bag.Error(f.Span(), diag.BadOverride, "an argument is name = value")
			ok = false
		case !isInput:
			r := e.bag.Error(f.Name.Span, diag.UnknownArgument, "%s has no parameter %q", use.Ref(), name)
			if !r.DidYouMean(name, names) {
				r.Hintf("%s", takes(itemName(item), names))
			}
			ok = false
		case in.Private:
			e.bag.Error(f.Name.Span, diag.PrivateInput, "%s is private to %s", name, use.Ref()).
				Hintf("remove it; a private var takes only its own default")
			ok = false
		case args[name] != nil:
			e.bag.Error(f.Name.Span, diag.DuplicateArgument, "%s is passed twice", name)
			ok = false
		default:
			args[name] = f
		}
	}

	env := map[string]ast.Expr{}
	var missing []string
	for _, in := range list {
		name := in.Name.Value
		switch {
		case args[name] != nil:
			env[name] = args[name].Value
		case !in.Private && sc.has(name):
			// At the input's name in the item, stamped: subst moves it onto
			// each place the item reads the input.
			env[name] = &ast.Ident{Tok: synth(token.Ident, name, name, stamp(in.Name).Span)}
		case !isNil(in.Default):
			env[name] = subst(ast.Clone(in.Default, stamp), env)
		default:
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		e.bag.Error(use.Item.Span, diag.MissingArgument, "%s needs %s", use.Ref(), strings.Join(missing, ", ")).
			Hintf("%s", missingHint(missing))
		ok = false
	}
	return env, ok
}

// missingHint is the hint for the inputs a call leaves without a value: only
// those, since an input the context supplies is not missing, and the two ways
// to give one a value.
func missingHint(missing []string) string {
	lines := make([]string, len(missing))
	for i, name := range missing {
		lines[i] = name + " = ..."
	}
	if len(missing) == 1 {
		return fmt.Sprintf("pass it, %s, or write var %s above this step", lines[0], missing[0])
	}
	return fmt.Sprintf("pass them, %s, or write those vars above this step", strings.Join(lines, ", "))
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
// copies of env's values, one per occurrence, placed by relocate.
func subst(x ast.Expr, env map[string]ast.Expr) ast.Expr {
	if isNil(x) {
		return x
	}
	switch v := x.(type) {
	case *ast.Ident:
		if r, ok := env[v.Tok.Value]; ok && !isNil(r) {
			return ast.Clone(r, relocate(v.Tok.Span))
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

// relocate places a replacement's tokens for the identifier at at: a token
// written where at was -- same file, same copy -- is moved onto at. That is a
// parameter's default, copied out of the parameter list; left at its own
// offset, it would stretch the node it lands in back to that list and sort a
// statement holding it above the action. An argument written at the use has
// another file or Via, keeps its span, and Join and SplitOrigin set it aside.
func relocate(at token.Span) func(token.Token) token.Token {
	return func(t token.Token) token.Token {
		if !t.Span.IsZero() && t.Span.File == at.File && t.Span.Via == at.Via {
			t.Span = at
		}
		return t
	}
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
	case *ast.DB:
		a.Conn = subst(a.Conn, env)
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

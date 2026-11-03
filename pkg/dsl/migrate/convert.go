package migrate

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// convert rewrites, in place, the old forms under n that have a new spelling
// in the same place: secret is removed, with a note; capture is var; use is
// step "<name>" = ...; drop captures is drop vars; and a data path written in
// a file in from is rewritten for a file in to. n is a copy: the parsed input
// is never changed, because a suite's material is copied into many flows.
func (m *migration) convert(n ast.Node, file, from, to string) {
	ast.Inspect(n, func(x ast.Node) {
		switch v := x.(type) {
		case *ast.VarDecl:
			if v.Secret.Text != "" {
				m.note("%s: secret is removed from var %s, and reports no longer withhold its value", at(file, v.Secret), v.Name.Value)
				v.Keyword = carry(v.Secret, v.Keyword)
				v.Secret = token.Token{}
			}
		case *ast.Capture:
			if v.Secret.Text != "" {
				m.note("%s: secret is removed from capture %s, and reports no longer withhold its value", at(file, v.Secret), v.Name.Value)
				v.Keyword = carry(v.Secret, v.Keyword)
				v.Secret = token.Token{}
			}
			v.Keyword = retext(v.Keyword, "var")
		case *ast.Drop:
			if v.What.Value == "captures" {
				v.What = retext(v.What, "vars")
			}
		case *ast.UseDecl:
			stepForm(v)
		case *ast.Import:
			if from != to {
				v.Path.Text, v.Path.Value = quote(rebase(v.Path.Value, from, to)), rebase(v.Path.Value, from, to)
			}
		}
	})
	if from != to {
		rewriteExprs(n, func(x ast.Expr) ast.Expr {
			m.rebaseData(x, file, from, to)
			return x
		})
	}
}

// stepForm turns `use a.b as x { ... }` into `step "x" = a.b as x { ... }`.
// The step keeps the name the old use gave it, which is what every report
// showed: the alias, or the reference as written.
func stepForm(u *ast.UseDecl) {
	if u.Keyword.Text == "" {
		return // already the step form
	}
	name := u.Ref()
	if u.Alias.Text != "" {
		name = u.Alias.Value
	}
	u.Step = carry(u.Keyword, word(token.Ident, "step"))
	u.Name = str(name)
	u.Assign = word(token.Assign, "=")
	u.Keyword = token.Token{}
	if u.LBrace.Text == "" {
		// The step form always has a block.
		u.LBrace, u.RBrace = word(token.LBrace, "{"), word(token.RBrace, "}")
	}
}

// dataCalls are the builtins whose path is relative to the file they are
// written in.
var dataCalls = map[string]bool{"json": true, "csv": true, "file": true}

// rebaseData rewrites the path of a json(), csv() or file() call that has
// moved from one folder to another, so that it still names the same file. A
// path built at run time cannot be rewritten, and says so in a note.
func (m *migration) rebaseData(x ast.Expr, file, from, to string) {
	c, ok := x.(*ast.Call)
	if !ok || c.Callee == nil || !dataCalls[c.Callee.Name()] || len(c.Args) == 0 {
		return
	}
	switch arg := c.Args[0].Value.(type) {
	case *ast.Literal:
		if arg.Kind() == token.String {
			p := rebase(arg.Tok.Value, from, to)
			arg.Tok.Text, arg.Tok.Value = quote(p), p
		}
	default:
		m.note("%s: the path %s() reads is built at run time, so it is not rewritten for the flow's folder; check it still names its file",
			at(file, c.Callee.Tok), c.Callee.Name())
	}
}

// suiteReads rewrites `suite.x` to `x` under n: a suite's names are the
// flow's own once its vars and setup are copied into the flow.
func suiteReads(n ast.Node) {
	rewriteExprs(n, func(x ast.Expr) ast.Expr {
		mem, ok := x.(*ast.Member)
		if !ok {
			return x
		}
		root, ok := mem.X.(*ast.Ident)
		if !ok || root.Name() != "suite" {
			return x
		}
		return &ast.Ident{Tok: carry(root.Tok, mem.Name)}
	})
}

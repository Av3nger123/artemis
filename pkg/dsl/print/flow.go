package print

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// flow prints a control-flow block. Each clause breaks like a phase -- its
// head and brace on one line, its body a paragraph per item -- and a clause
// that follows another starts on the line the previous one closes on, as Go
// writes `} else {`: `} else if`, `} catch {`, `} finally {`, `} until cond`.
// A comment between the two keeps them apart, because a joined line has no
// place to put one.
func (c *canon) flow(f *ast.FlowDecl) {
	joined := false
	for i, cl := range f.Clauses {
		c.clause(cl, joined)
		var next token.Token
		switch {
		case i+1 < len(f.Clauses):
			next = f.Clauses[i+1].Keyword
		case f.Until.Text != "":
			next = f.Until
		}
		joined = c.closeClause(cl.RBrace, next)
	}
	if f.Until.Text == "" {
		return
	}
	text := join(" ", f.Until.Text, c.expr(f.Cond))
	all := append(toks(f.Until), nodeToks(f.Cond)...)
	if joined {
		c.w.push(" " + text)
		for _, t := range rest(all) {
			c.w.push(" " + t)
		}
		c.w.endLine()
		return
	}
	c.line(all, text)
}

// clause prints one clause's head, brace and body. joined is whether the
// previous clause's brace is waiting on the current line.
func (c *canon) clause(cl *ast.Clause, joined bool) {
	head := join(" ", cl.Keyword.Text, cl.If.Text, c.expr(cl.Expr))
	headToks := append(toks(cl.Keyword, cl.If), nodeToks(cl.Expr)...)
	for _, o := range cl.Options {
		head = join(" ", head, o.Name.Text, c.expr(o.Value))
		headToks = append(headToks, o.Tokens(nil)...)
	}
	head = join(" ", head, cl.LBrace.Text)
	headToks = append(headToks, toks(cl.LBrace)...)
	if joined {
		c.w.push(" " + head)
		for _, t := range rest(headToks) {
			c.w.push(" " + t)
		}
		c.w.endLine()
	} else {
		c.line(headToks, head)
	}
	c.w.in()
	c.w.suppress()
	for i, d := range cl.Body {
		if i > 0 {
			switch d.(type) {
			case *ast.StepDecl, *ast.FlowDecl, *ast.UseDecl:
				c.w.blank()
			}
		}
		if inner, ok := d.(*ast.Clause); ok {
			// A switch's case: a clause of its own, closed on its own line.
			c.clause(inner, false)
			c.closeClause(inner.RBrace, token.Token{})
			continue
		}
		c.decl(d)
	}
	c.w.outd()
}

// closeClause writes a clause's closing brace and reports whether next -- the
// keyword of the clause or the until that follows -- can go on the same line.
// When it can, the line is left open for it.
func (c *canon) closeClause(rbrace, next token.Token) bool {
	cs, _ := above(rbrace)
	if len(cs) > 0 {
		c.w.in()
		c.w.suppress()
		for _, cm := range cs {
			if cm.blank {
				c.w.blank()
			}
			c.w.push(cm.text)
			c.w.endLine()
		}
		c.w.outd()
	}
	c.w.suppress()
	c.w.push(rbrace.Text)
	trailing := appendComments(nil, rbrace.Trailing)
	if next.Text != "" && len(trailing) == 0 {
		if before, _ := above(next); len(before) == 0 {
			return true
		}
	}
	for _, text := range trailing {
		c.w.push(" " + text)
	}
	c.w.endLine()
	return false
}

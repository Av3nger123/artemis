package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// collection always breaks, like a scenario, with a blank line between items.
func (c *canon) collection(k *ast.Collection) {
	head := join(" ", k.Keyword.Text, k.Name.Text)
	if len(k.Items) == 0 && !hasComment(k) {
		c.line(toks(k.Keyword, k.Name, k.LBrace, k.RBrace), join(" ", head, braces(k.LBrace, k.RBrace)))
		return
	}
	c.line(toks(k.Keyword, k.Name, k.LBrace), join(" ", head, k.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for i, d := range k.Items {
		if i > 0 {
			c.w.blank()
		}
		c.decl(d)
	}
	c.w.outd()
	c.close(k.RBrace)
}

// params renders `(sku, qty = 1, secret password)`. The commas are layout, so
// the ones the author wrote are not consulted.
func (c *canon) params(ps *ast.Params) string {
	if ps == nil {
		return "()"
	}
	parts := make([]string, 0, len(ps.List))
	for _, p := range ps.List {
		s := join(" ", p.Secret.Text, p.Name.Text)
		if p.Default != nil {
			s += " = " + c.expr(p.Default)
		}
		parts = append(parts, s)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// requestDecl prints exactly as a step does, with `request name(params)` as
// the head.
func (c *canon) requestDecl(r *ast.RequestDecl) {
	head := r.Keyword.Text + " " + r.Name.Text + c.params(r.Params)
	lead := append(toks(r.Keyword, r.Name), nodeToks(r.Params)...)
	items := stepItems(&ast.StepDecl{Action: r.Action, Body: r.Body})
	if len(items) == 0 && !hasComment(r) {
		c.line(append(lead, toks(r.LBrace, r.RBrace)...), join(" ", head, braces(r.LBrace, r.RBrace)))
		return
	}
	c.line(append(lead, toks(r.LBrace)...), join(" ", head, r.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for _, it := range items {
		switch v := it.(type) {
		case ast.Action:
			c.action(v)
		case ast.Stmt:
			c.stmt(v)
		}
	}
	c.w.outd()
	c.close(r.RBrace)
}

// flowDecl always breaks, and gives each step after the first a blank line
// above it, as a scenario body does.
func (c *canon) flowDecl(f *ast.FlowDecl) {
	head := f.Keyword.Text + " " + f.Name.Text + c.params(f.Params)
	lead := append(toks(f.Keyword, f.Name), nodeToks(f.Params)...)
	if len(f.Body) == 0 && !hasComment(f) {
		c.line(append(lead, toks(f.LBrace, f.RBrace)...), join(" ", head, braces(f.LBrace, f.RBrace)))
		return
	}
	c.line(append(lead, toks(f.LBrace)...), join(" ", head, f.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for i, d := range f.Body {
		if _, ok := d.(*ast.StepDecl); ok && i > 0 {
			c.w.blank()
		}
		c.decl(d)
	}
	c.w.outd()
	c.close(f.RBrace)
}

// useDecl is `use a.b as x` and, when there is a block, its lines.
func (c *canon) useDecl(u *ast.UseDecl) {
	head := join(" ", u.Keyword.Text, u.Collection.Text+u.Dot.Text+u.Item.Text, u.As.Text, u.Alias.Text)
	lead := toks(u.Keyword, u.Collection, u.Dot, u.Item, u.As, u.Alias)
	if u.LBrace.Text == "" {
		c.line(lead, head)
		return
	}
	c.useLines(append(lead, u.LBrace), head, u.LBrace, u.Lines, u.RBrace)
}

// useLines renders `head { ... }` for a use block or an in block. A block of
// only one-line fields -- arguments, `header "X" = v` -- that fits the margin
// stays inline, as a request block does; anything else breaks one line per
// entry. lead is the head's tokens, the opening brace included.
func (c *canon) useLines(lead []token.Token, head string, lb token.Token, lines []ast.Stmt, rb token.Token) {
	if s, ok := c.inlineUseLines(lb, lines, rb); ok && len(head)+1+len(s) <= c.w.width() {
		all := lead
		for _, l := range lines {
			all = append(all, nodeToks(l)...)
		}
		c.line(append(all, toks(rb)...), join(" ", head, s))
		return
	}
	c.line(lead, join(" ", head, lb.Text))
	c.w.in()
	c.w.suppress()
	for _, l := range lines {
		c.stmt(l)
	}
	c.w.outd()
	c.close(rb)
}

// inlineUseLines is a use or in block on one line, or false when its shape
// does not allow that: a comment inside it, or a line that is not a plain
// field.
func (c *canon) inlineUseLines(lb token.Token, lines []ast.Stmt, rb token.Token) (string, bool) {
	if hasCommentTrivia(lb.Trailing) || hasCommentTrivia(rb.Leading) {
		return "", false
	}
	if len(lines) == 0 {
		return braces(lb, rb), true
	}
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		f, ok := l.(*ast.Field)
		if !ok || f.Block != nil || hasComment(f) {
			return "", false
		}
		parts = append(parts, assign(join(" ", f.Name.Text, c.expr(f.Key)), f.Assign, c.expr(f.Value)))
	}
	return lb.Text + " " + strings.Join(parts, ", ") + " " + rb.Text, true
}

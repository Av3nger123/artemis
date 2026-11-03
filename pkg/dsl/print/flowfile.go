package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
)

// The canonical layout of flow files and scenario files.

// flowDecl breaks like a scenario: `flow checkout {`, its body, `}`, with a
// blank line above each step, phase and block.
func (c *canon) flowDecl(f *ast.Flow) {
	head := join(" ", f.Keyword.Text, f.Name.Text)
	if len(f.Body) == 0 && !hasComment(f) {
		c.line(toks(f.Keyword, f.Name, f.LBrace, f.RBrace), join(" ", head, braces(f.LBrace, f.RBrace)))
		return
	}
	c.line(toks(f.Keyword, f.Name, f.LBrace), join(" ", head, f.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for i, d := range f.Body {
		switch d.(type) {
		case *ast.StepDecl, *ast.PhaseDecl, *ast.BlockDecl:
			if i > 0 {
				c.w.blank()
			}
		}
		c.decl(d)
	}
	c.w.outd()
	c.close(f.RBrace)
}

// isDataScenario reports whether s is a scenario of a scenario file, which is
// laid out as a table of sets rather than as a body of steps. A scenario with
// no set prints the same either way.
func isDataScenario(s *ast.Scenario) bool {
	for _, d := range s.Body {
		if _, ok := d.(*ast.Set); ok {
			return true
		}
	}
	return false
}

// maxInlineSets is how many sets a data scenario may hold and still print on
// one line.
const maxInlineSets = 3

// dataScenario prints on one line when it holds at most maxInlineSets sets,
// no tags and no comment inside its braces, and the line fits:
//
//	scenario "with a coupon" { quantity = 3  coupon = "TEN" }
//
// Otherwise it prints one line each, with the "=" signs aligned, so a file of
// scenarios reads as the table it is.
func (c *canon) dataScenario(s *ast.Scenario) {
	head := join(" ", s.Keyword.Text, s.Name.Text)
	if inline, ok := c.inlineData(s); ok && len(head)+1+len(inline) <= c.w.width() {
		c.line(s.Tokens(nil), join(" ", head, inline))
		return
	}
	width := 0
	for _, d := range s.Body {
		if n := len(dataName(d)); n > width {
			width = n
		}
	}
	c.line(toks(s.Keyword, s.Name, s.LBrace), join(" ", head, s.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for _, d := range s.Body {
		switch v := d.(type) {
		case *ast.Set:
			c.line(v.Tokens(nil), aligned(v.Name.Text, width, c.expr(v.Value)))
		case *ast.TagsDecl:
			c.line(v.Tokens(nil), aligned(v.Keyword.Text, width, c.expr(v.Value)))
		default:
			c.decl(d)
		}
	}
	c.w.outd()
	c.close(s.RBrace)
}

// inlineData is a data scenario's braces and sets on one line, two spaces
// between sets, or false when its shape does not allow that.
func (c *canon) inlineData(s *ast.Scenario) (string, bool) {
	if len(s.Body) > maxInlineSets || hasCommentTrivia(s.LBrace.Trailing) || hasCommentTrivia(s.RBrace.Leading) {
		return "", false
	}
	parts := make([]string, 0, len(s.Body))
	for _, d := range s.Body {
		v, ok := d.(*ast.Set)
		if !ok || hasComment(v) {
			return "", false
		}
		part := assign(v.Name.Text, v.Assign, c.expr(v.Value))
		if strings.Contains(part, "\n") {
			return "", false
		}
		parts = append(parts, part)
	}
	return s.LBrace.Text + " " + strings.Join(parts, "  ") + " " + s.RBrace.Text, true
}

// dataName is the left-hand side of a line of a data scenario, which the
// alignment is measured over.
func dataName(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.Set:
		return v.Name.Text
	case *ast.TagsDecl:
		return v.Keyword.Text
	}
	return ""
}

// aligned is `name = value` with name padded to width.
func aligned(name string, width int, value string) string {
	return name + strings.Repeat(" ", width-len(name)) + " = " + value
}

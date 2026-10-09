package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// canon is canonical mode: the one layout engine, which preserving mode also
// calls for the nodes it has to re-render.
//
// The split that runs through this file is statements versus expressions.
// Statements are line-oriented -- one per line, indented, with comments placed
// above or at the end. Expressions are always inline, and live in expr.go. A
// brace block sits between the two: it breaks one field per line, or renders
// inline when the whole statement fits in the margin.
type canon struct{ w *writer }

// node dispatches on what n is. Every interface in ast is covered, and *ast.Bad
// -- which satisfies all four -- lands in the Decl case, where bad() prints its
// source verbatim.
func (c *canon) node(n ast.Node) {
	switch v := n.(type) {
	case *ast.File:
		c.file(v)
	case *ast.Block:
		c.blockAfter(nil, "", v, false)
	case *ast.Params:
		c.w.push(c.params(v))
	case *ast.Param:
		c.w.push(strings.TrimSuffix(strings.TrimPrefix(c.params(&ast.Params{List: []*ast.Param{v}}), "("), ")"))
	case ast.Decl:
		c.decl(v)
	case ast.Stmt:
		c.stmt(v)
	case ast.Action:
		c.action(v)
	case ast.Expr:
		c.w.push(c.expr(v))
	}
}

// file is the top-level declarations, separated by exactly one blank line,
// then whatever trivia the EOF token is holding -- a file's closing comment
// lives there, and dropping it would break R3 on the first fixture that has
// one. Consecutive imports are the exception: they stack with no blank line
// between them, and the blank line comes after the last.
func (c *canon) file(f *ast.File) {
	for i, d := range f.Scenarios {
		if i > 0 {
			_, prevImport := f.Scenarios[i-1].(*ast.Import)
			_, thisImport := d.(*ast.Import)
			if !prevImport || !thisImport {
				c.w.blank()
			}
		}
		c.decl(d)
	}
	c.eof(f.EOF)
}

// eof writes the comments the end of the file is carrying, each on its own line.
func (c *canon) eof(t token.Token) {
	cs, _ := above(t)
	for _, cm := range cs {
		if cm.blank {
			c.w.blank()
		}
		c.w.push(cm.text)
		c.w.endLine()
	}
	for _, text := range appendComments(nil, t.Trailing) {
		c.w.push(text)
		c.w.endLine()
	}
}

func (c *canon) decl(d ast.Decl) {
	switch v := d.(type) {
	case *ast.Scenario:
		c.scenario(v)
	case *ast.ConfigDecl:
		c.config(v)
	case *ast.VarDecl:
		c.varDecl(v)
	case *ast.StepDecl:
		c.step(v)
	case *ast.Import:
		c.line(v.Tokens(nil), join(" ", v.Keyword.Text, v.Path.Text))
	case *ast.Collection:
		c.collection(v)
	case *ast.RequestDecl:
		c.requestDecl(v)
	case *ast.FlowDecl:
		c.flowDecl(v)
	case *ast.UseDecl:
		c.useDecl(v)
	case *ast.Bad:
		c.bad(v)
	}
}

// scenario always breaks: it is the file's structural unit, and a scenario
// squeezed onto one line is not what anyone wants out of `fmt -w`. An empty one
// is the exception, because `scenario "s" {}` reads better than three lines of
// nothing.
func (c *canon) scenario(s *ast.Scenario) {
	head := join(" ", s.Keyword.Text, s.Name.Text)
	if len(s.Body) == 0 && !hasComment(s) {
		c.line(toks(s.Keyword, s.Name, s.LBrace, s.RBrace), join(" ", head, braces(s.LBrace, s.RBrace)))
		return
	}
	c.line(toks(s.Keyword, s.Name, s.LBrace), join(" ", head, s.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for i, d := range s.Body {
		// A step always gets a blank line above it, whether or not the author
		// left one. It is the one piece of paragraphing canonical mode supplies
		// rather than keeps, because `migrate` and `generate` build trees with
		// no trivia at all and a wall of twelve steps with nothing between them
		// is not a file anyone wants to read. The first item in the body is
		// against the brace and gets nothing.
		if _, ok := d.(*ast.StepDecl); ok && i > 0 {
			c.w.blank()
		}
		c.decl(d)
	}
	c.w.outd()
	c.close(s.RBrace)
}

// config is `config browser { ... }`, whose block follows the general
// fits-inline rule -- which is the whole reason a one-field config block
// survives `fmt -w` on one line.
func (c *canon) config(d *ast.ConfigDecl) {
	c.blockAfter(toks(d.Keyword, d.Subject), join(" ", d.Keyword.Text, d.Subject.Text), d.Block, true)
}

func (c *canon) varDecl(d *ast.VarDecl) {
	// join drops an empty string, so a plain var prints as it always has and a
	// `secret var` needs no second branch here.
	left := join(" ", d.Secret.Text, d.Keyword.Text, d.Name.Text)
	c.line(d.Tokens(nil), assign(left, d.Assign, c.expr(d.Value)))
}

// step always breaks, like a scenario, and renders its action and its
// statements in source order.
func (c *canon) step(s *ast.StepDecl) {
	items := stepItems(s)
	head := join(" ", s.Keyword.Text, s.Name.Text)
	if len(items) == 0 && !hasComment(s) {
		c.line(toks(s.Keyword, s.Name, s.LBrace, s.RBrace), join(" ", head, braces(s.LBrace, s.RBrace)))
		return
	}
	c.line(toks(s.Keyword, s.Name, s.LBrace), join(" ", head, s.LBrace.Text))
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
	c.close(s.RBrace)
}

func (c *canon) action(a ast.Action) {
	switch v := a.(type) {
	case *ast.Request:
		c.blockAfter(append(toks(v.Method), nodeToks(v.URL)...),
			join(" ", v.Method.Text, c.expr(v.URL)), v.Block, false)
	case *ast.Run:
		c.blockAfter(append(toks(v.Keyword), nodeToks(v.Command)...),
			join(" ", v.Keyword.Text, c.expr(v.Command)), v.Block, false)
	case *ast.Browser:
		c.browser(v)
	case *ast.Bad:
		c.bad(v)
	}
}

// browser always breaks: its block is a list of actions a person reads down,
// and `fmt -w` collapsing five of them onto one line would be a worse file than
// the one it was given.
func (c *canon) browser(b *ast.Browser) {
	if len(b.Acts) == 0 && !hasComment(b) {
		c.line(toks(b.Keyword, b.LBrace, b.RBrace), join(" ", b.Keyword.Text, braces(b.LBrace, b.RBrace)))
		return
	}
	c.line(toks(b.Keyword, b.LBrace), join(" ", b.Keyword.Text, b.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for _, a := range b.Acts {
		c.stmt(a)
	}
	c.w.outd()
	c.close(b.RBrace)
}

func (c *canon) stmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.Field:
		c.field(v)
	case *ast.Expect:
		c.line(v.Tokens(nil), join(" ", v.Keyword.Text, c.expr(v.Value), v.Within.Text, c.expr(v.Budget)))
	case *ast.Capture:
		c.line(v.Tokens(nil), assign(
			join(" ", v.Secret.Text, v.Keyword.Text, v.Name.Text), v.Assign, c.expr(v.Value)))
	case *ast.BrowserAct:
		c.line(v.Tokens(nil), assign(join(" ", v.Name.Text, c.expr(v.Target)), v.Assign, c.expr(v.Value)))
	case *ast.BodySet:
		left := v.Body.Text
		for _, p := range v.Path {
			left += p.Dot.Text + p.Name.Text
		}
		c.line(v.Tokens(nil), assign(left, v.Assign, c.expr(v.Value)))
	case *ast.Drop:
		c.line(v.Tokens(nil), join(" ", v.Keyword.Text, v.What.Text))
	case *ast.In:
		c.useLines(toks(v.Keyword, v.Step, v.LBrace), join(" ", v.Keyword.Text, v.Step.Text), v.LBrace, v.Lines, v.RBrace)
	case *ast.Bad:
		c.bad(v)
	}
}

// field is both shapes: `name [key] = value` on one line, and `name { ... }`,
// whose block decides for itself whether it fits.
func (c *canon) field(f *ast.Field) {
	left := join(" ", f.Name.Text, c.expr(f.Key))
	if f.Block != nil {
		c.blockAfter(append(toks(f.Name), nodeToks(f.Key)...), left, f.Block, separable(f.Name.Text))
		return
	}
	c.line(f.Tokens(nil), assign(left, f.Assign, c.expr(f.Value)))
}

// blockAfter writes prefix followed by b: inline when the whole statement fits
// in the margin at this indentation and the block holds no comment, one field
// per line otherwise.
//
// The decision reads the tree and the current indent and nothing else -- never
// trivia -- which is what makes canonical mode idempotent by construction: its
// own output measures the same as what it was given.
func (c *canon) blockAfter(head []token.Token, prefix string, b *ast.Block, sep bool) {
	if b == nil {
		c.line(head, prefix)
		return
	}
	if s, ok := c.inlineBlock(b, sep); ok && len(prefix)+1+len(s) <= c.w.width() {
		c.line(append(head, b.Tokens(nil)...), join(" ", prefix, s))
		return
	}
	c.line(append(head, toks(b.LBrace)...), join(" ", prefix, b.LBrace.Text))
	c.w.in()
	c.w.suppress()
	for _, f := range b.Fields {
		c.stmt(f)
	}
	c.w.outd()
	c.close(b.RBrace)
}

// inlineBlock renders b on one line, or reports that it cannot be.
//
// It cannot when the block's interior holds a comment -- an inline `#` would
// comment out the rest of the line -- or an ast.Bad, whose source is lines rather than a
// phrase, or more than one field in a block the grammar gives no separator
// (see separable). The caller measures the result; this only says whether the
// shape allows it.
func (c *canon) inlineBlock(b *ast.Block, sep bool) (string, bool) {
	if interiorComment(b) {
		return "", false
	}
	if len(b.Fields) == 0 {
		return braces(b.LBrace, b.RBrace), true
	}
	if len(b.Fields) > 1 && !sep {
		return "", false
	}
	parts := make([]string, 0, len(b.Fields))
	for _, f := range b.Fields {
		s, ok := c.inlineStmt(f)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	return b.LBrace.Text + " " + strings.Join(parts, ", ") + " " + b.RBrace.Text, true
}

// inlineStmt is one field as a phrase, for a block being rendered inline.
func (c *canon) inlineStmt(s ast.Stmt) (string, bool) {
	f, ok := s.(*ast.Field)
	if !ok {
		return "", false // an ast.Bad, or a statement that is not a field
	}
	left := join(" ", f.Name.Text, c.expr(f.Key))
	if f.Block == nil {
		return assign(left, f.Assign, c.expr(f.Value)), true
	}
	inner, ok := c.inlineBlock(f.Block, separable(f.Name.Text))
	if !ok {
		return "", false
	}
	return join(" ", left, inner), true
}

// interiorComment reports whether a comment sits *inside* b, which is what
// stops it being rendered on one line.
//
// A comment after the closing brace -- `config browser { headless = true } #
// why` -- is not inside it, and must not force the block open: canon.line puts
// it at the end of whatever line the block ends up on. Nor is one before the
// opening brace. Everything between the two is interior: after the `{`, before
// the `}`, or anywhere in a field.
func interiorComment(b *ast.Block) bool {
	if hasCommentTrivia(b.LBrace.Trailing) || hasCommentTrivia(b.RBrace.Leading) {
		return true
	}
	for _, f := range b.Fields {
		if hasComment(f) {
			return true
		}
	}
	return false
}

// separable reports whether a block's fields may be separated by commas.
//
// Only three blocks in the grammar take a separator at all -- the design's
// `Sep = "," | newline` appears in ConfigDecl, in a `run` block's `env`, and in
// `retry`, and nowhere else. The parser is more lenient than that and accepts a
// comma between request fields too, but canonical output is what `migrate`,
// `generate` and `fmt -w` write and what `artemis grammar` describes, so it
// stays inside the published grammar: a request or `run` block with two fields
// in it breaks, which is also how the design document's own worked example is
// written.
func separable(name string) bool { return name == "env" || name == "retry" }

// bad prints source that did not parse, verbatim, one line at a time at the
// current indentation.
//
// Formatting a broken file is not something anything should be doing, but
// deleting the bytes of the line that broke would be unforgivable, so the
// printer keeps them and stays total. See Canonical's doc comment: idempotence
// is only claimed for a file that parses clean.
func (c *canon) bad(b *ast.Bad) {
	for _, line := range strings.Split(ast.Source(b), "\n") {
		if line = strings.Trim(line, " \t\v\f\r"); line != "" {
			c.w.push(line)
			c.w.endLine()
		}
	}
}

// line writes one statement: the blank line and own-line comments above it, the
// content, then every comment canonical layout had nowhere else to put.
func (c *canon) line(toks []token.Token, content string) {
	var first token.Token
	if len(toks) > 0 {
		first = toks[0]
	}
	cs, blank := above(first)
	for _, cm := range cs {
		if cm.blank {
			c.w.blank()
		}
		c.w.push(cm.text)
		c.w.endLine()
	}
	if blank {
		c.w.blank()
	}
	c.w.push(content)
	for _, text := range rest(toks) {
		c.w.push(" " + text)
	}
	c.w.endLine()
}

// close writes a block's closing brace.
//
// The comments above it are written one level *in*, where the body they belong
// to is, and the brace itself at the block's own level -- the placement gofmt
// makes for the same shape. A blank line the author left before the brace is
// dropped: canonical mode has no blank line against a `}`.
//
// It must be called with the writer already dedented, so it indents itself back
// for the comments.
func (c *canon) close(rbrace token.Token) {
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
	for _, text := range appendComments(nil, rbrace.Trailing) {
		c.w.push(" " + text)
	}
	c.w.endLine()
}

// assign joins a left-hand side to a value with ` = `, and only when there was
// an `=` or a value to join. A truncated `timeout` with neither must not grow
// one.
func assign(left string, eq token.Token, value string) string {
	if eq.Text == "" && value == "" {
		return left
	}
	return join(" ", left, "=", value)
}

// braces is `{}` for an empty block, from the tokens that were actually there,
// so a file truncated after `{` does not gain a `}`.
func braces(l, r token.Token) string { return l.Text + r.Text }

// toks is a token list literal, dropping the tokens that were never in the file
// -- an absent `within`, an absent closing brace -- the way ast's own
// appendTok does.
func toks(ts ...token.Token) []token.Token {
	out := make([]token.Token, 0, len(ts))
	for _, t := range ts {
		if t.Text != "" || len(t.Leading) > 0 || len(t.Trailing) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// nodeToks is a child's tokens, so the line that renders it also owns its
// comments.
func nodeToks(n ast.Node) []token.Token {
	if isNil(n) {
		return nil
	}
	return n.Tokens(nil)
}

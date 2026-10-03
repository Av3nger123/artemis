package print

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// pres is preserving mode.
//
// It is a descent that stops as early as it can: at every node it asks whether
// anything in the subtree is synthetic, and when the answer is no it writes the
// bytes that subtree was parsed from and returns. For a tree nothing has edited
// the answer is no at the root, so the gate -- Preserving(parse(src)) == src --
// is ast.Source and cannot be broken by a layout rule.
//
// Below an edit the descent does three things. Intact siblings are still
// written verbatim, which is what makes the diff small. A node that is entirely
// new gets a line of its own, laid out by the canonical renderer at the
// indentation its neighbours use. And a synthetic token sitting between intact
// ones gets *glue*: a single space, inserted only where one side of the join is
// synthetic, because a synthetic token carries no trivia and the space that
// used to be there belonged to the token it replaced.
type pres struct {
	b strings.Builder

	// prevSynth is why glue is safe. Two intact tokens are written exactly as
	// they were, byte for byte, with no inspection -- inserting a space between
	// `a` and `==` would corrupt a file nobody edited. Glue is considered only
	// when one side of a join was built in Go.
	prevSynth bool

	// needNL is set after a whole line has been written for a new node. The
	// next token usually opens with its own line break and nothing is needed;
	// when it does not -- a field inserted into a block that was written on one
	// line -- this is what stops the two running together.
	needNL bool

	// unit is the indentation this file uses, detected once, so a node inserted
	// into a tab-indented file gets a tab.
	unit string
}

// ctx is where in the file the descent is: the indentation a new line at this
// level should start with.
type ctx struct{ indent string }

func (p *pres) child(c ctx, items []ast.Node) ctx {
	if ind, ok := blockIndent(items); ok {
		return ctx{indent: ind}
	}
	return ctx{indent: c.indent + p.unit}
}

// node is the three-way decision this mode is.
func (p *pres) node(n ast.Node, c ctx) {
	if isNil(n) {
		return
	}
	if intact(n) {
		p.verbatim(ast.Source(n))
		return
	}
	if lineOriented(n) && ownReal(n) == 0 {
		p.newLine(n, c)
		return
	}
	p.descend(n, c)
}

// verbatim writes source exactly, which is the whole point of this mode.
func (p *pres) verbatim(s string) {
	if s == "" {
		return
	}
	p.breakIfNeeded(s)
	p.b.WriteString(s)
	p.prevSynth = false
}

// newLine writes a node that was built rather than parsed, canonically, on
// lines of its own at c's indentation.
//
// The canonical renderer runs at depth zero and every line it produces is
// prefixed here, so a new step's inner lines are indented relative to the file
// rather than relative to the margin. No trailing newline is written: the token
// that follows -- the next statement, or the block's closing brace -- opens with
// the line break the file already had.
func (p *pres) newLine(n ast.Node, c ctx) {
	rendered := strings.TrimRight(Canonical(n), "\n")
	if rendered == "" {
		return
	}
	if p.b.Len() > 0 && !endsWithNewline(p.b.String()) {
		p.b.WriteString("\n")
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if i > 0 {
			p.b.WriteString("\n")
		}
		p.b.WriteString(c.indent + line)
	}
	p.prevSynth = false
	p.needNL = true
}

// tok writes one token: verbatim with its trivia when it came from the file,
// its text with glue when it did not.
func (p *pres) tok(t token.Token) {
	if t.Text == "" && len(t.Leading) == 0 && len(t.Trailing) == 0 {
		return // a token that was never in the file, the way ast.appendTok skips it
	}
	if !synthetic(t) {
		src := t.Source()
		p.breakIfNeeded(src)
		if p.prevSynth && needsGlue(lastByte(p.b.String()), firstByte(src)) {
			p.b.WriteString(" ")
		}
		p.b.WriteString(src)
		p.prevSynth = false
		return
	}
	p.glue(t.Text)
}

// glue writes synthetic text, separated from what is already there by a single
// space when the join would otherwise run two words together.
func (p *pres) glue(s string) {
	if s == "" {
		return
	}
	p.breakIfNeeded(s)
	if needsGlue(lastByte(p.b.String()), firstByte(s)) {
		p.b.WriteString(" ")
	}
	p.b.WriteString(s)
	p.prevSynth = true
}

// breakIfNeeded closes off a line written for a new node when what comes next
// does not open one itself.
func (p *pres) breakIfNeeded(next string) {
	if !p.needNL {
		return
	}
	p.needNL = false
	if c := firstByte(next); c != '\n' && c != '\r' {
		p.b.WriteString("\n")
	}
}

// list walks a block's items at the indentation its intact members use.
func (p *pres) list(items []ast.Node, c ctx) {
	inner := p.child(c, items)
	for _, it := range items {
		p.node(it, inner)
	}
}

// descend walks a node's own tokens and its children in source order.
//
// It mirrors each node's Tokens method deliberately: the two lists are the same
// list, and a node that gained a field and was added to one but not the other
// fails the round-trip test rather than silently losing a byte. The default case
// -- a leaf, and anything a later issue adds -- writes the node's tokens, which
// is exactly right for an ast.Ident, an ast.Literal and an ast.Bad.
func (p *pres) descend(n ast.Node, c ctx) {
	switch v := n.(type) {
	case *ast.File:
		for _, d := range v.Scenarios {
			p.node(d, c)
		}
		p.tok(v.EOF)
	case *ast.Scenario:
		p.tok(v.Keyword)
		p.tok(v.Name)
		p.tok(v.LBrace)
		p.list(decls(v.Body), c)
		p.tok(v.RBrace)
	case *ast.ConfigDecl:
		p.tok(v.Keyword)
		p.tok(v.Subject)
		p.node(v.Block, c)
	case *ast.VarDecl:
		p.tok(v.Keyword)
		p.tok(v.Name)
		p.tok(v.Assign)
		p.node(v.Value, c)
	case *ast.StepDecl:
		p.tok(v.Keyword)
		p.tok(v.Name)
		p.tok(v.LBrace)
		p.list(stepItems(v), c)
		p.tok(v.RBrace)
	case *ast.Request:
		p.tok(v.Method)
		p.node(v.URL, c)
		p.node(v.Block, c)
	case *ast.Run:
		p.tok(v.Keyword)
		p.node(v.Command, c)
		p.node(v.Block, c)
	case *ast.Browser:
		p.tok(v.Keyword)
		p.tok(v.LBrace)
		p.list(stmts(v.Acts), c)
		p.tok(v.RBrace)
	case *ast.BrowserAct:
		p.tok(v.Name)
		p.node(v.Target, c)
		p.tok(v.Assign)
		p.node(v.Value, c)
		p.tok(v.Comma)
	case *ast.Block:
		p.tok(v.LBrace)
		p.list(stmts(v.Fields), c)
		p.tok(v.RBrace)
	case *ast.Field:
		p.tok(v.Name)
		p.node(v.Key, c)
		p.tok(v.Assign)
		p.node(v.Value, c)
		p.node(v.Block, c)
		p.tok(v.Comma)
	case *ast.Expect:
		p.tok(v.Keyword)
		p.node(v.Value, c)
		p.tok(v.Within)
		p.node(v.Budget, c)
		p.tok(v.Comma)
	case *ast.Capture:
		p.tok(v.Keyword)
		p.tok(v.Name)
		p.tok(v.Assign)
		p.node(v.Value, c)
		p.tok(v.Comma)
	case *ast.Interp:
		for _, s := range v.Segments {
			p.tok(s.Delim)
			p.node(s.Expr, c)
		}
		p.tok(v.End)
	case *ast.Unary:
		p.tok(v.Op)
		p.node(v.X, c)
	case *ast.Binary:
		p.node(v.X, c)
		p.tok(v.Op)
		p.node(v.Y, c)
	case *ast.Exists:
		p.node(v.X, c)
		p.tok(v.Op)
	case *ast.IsType:
		p.node(v.X, c)
		p.tok(v.Op)
		p.tok(v.Type)
	case *ast.Member:
		p.node(v.X, c)
		p.tok(v.Dot)
		p.tok(v.Name)
	case *ast.Index:
		p.node(v.X, c)
		p.tok(v.LBracket)
		p.node(v.Index, c)
		p.tok(v.RBracket)
	case *ast.Call:
		p.node(v.Callee, c)
		p.tok(v.LParen)
		for _, a := range v.Args {
			p.node(a.Value, c)
			p.tok(a.Comma)
		}
		p.tok(v.RParen)
	case *ast.Object:
		p.tok(v.LBrace)
		for _, e := range v.Entries {
			p.node(e.Key, c)
			p.tok(e.Colon)
			p.node(e.Value, c)
			p.tok(e.Comma)
		}
		p.tok(v.RBrace)
	case *ast.Array:
		p.tok(v.LBracket)
		for _, e := range v.Elems {
			p.node(e.Value, c)
			p.tok(e.Comma)
		}
		p.tok(v.RBracket)
	case *ast.Paren:
		p.tok(v.LParen)
		p.node(v.X, c)
		p.tok(v.RParen)
	default:
		for _, t := range n.Tokens(nil) {
			p.tok(t)
		}
	}
}

// lineOriented reports whether a node occupies lines of its own rather than
// sitting inside one, which is what decides how a *new* node is written: a new
// field gets a line, a new expression is spliced into the line it is part of.
func lineOriented(n ast.Node) bool {
	switch n.(type) {
	case *ast.Scenario, *ast.ConfigDecl, *ast.VarDecl, *ast.StepDecl,
		*ast.Request, *ast.Run, *ast.Browser, *ast.BrowserAct,
		*ast.Field, *ast.Expect, *ast.Capture:
		return true
	}
	return false
}

// ownReal is how many of a node's own tokens -- not its children's -- came from
// a file.
//
// Zero means the node itself is new even if a subtree under it was moved there
// intact, which is the question newLine needs answered: a field whose name and
// `=` were built in Go has never been on a line, so it needs one.
func ownReal(n ast.Node) int {
	total := countReal(n)
	for _, ch := range ast.Children(n) {
		total -= countReal(ch)
	}
	return total
}

func countReal(n ast.Node) int {
	if isNil(n) {
		return 0
	}
	count := 0
	for _, t := range n.Tokens(nil) {
		if !synthetic(t) {
			count++
		}
	}
	return count
}

// needsGlue reports whether a space is needed between two adjacent bytes for
// them not to read as one token. It is only ever asked about a join where one
// side was built in Go.
func needsGlue(prev, next byte) bool {
	if prev == 0 || next == 0 {
		return false
	}
	if isSpaceByte(prev) || isSpaceByte(next) {
		return false
	}
	if strings.IndexByte("([{.", prev) >= 0 {
		return false
	}
	if strings.IndexByte(")]},:.", next) >= 0 {
		return false
	}
	return true
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func endsWithNewline(s string) bool {
	return strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\r")
}

func lastByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

func decls(ds []ast.Decl) []ast.Node {
	out := make([]ast.Node, 0, len(ds))
	for _, d := range ds {
		if !isNil(d) {
			out = append(out, d)
		}
	}
	return out
}

func stmts(ss []ast.Stmt) []ast.Node {
	out := make([]ast.Node, 0, len(ss))
	for _, s := range ss {
		if !isNil(s) {
			out = append(out, s)
		}
	}
	return out
}

// detectUnit is the indentation the file uses, read off the first line that has
// any: a file indented with tabs gets a tab when a node is inserted into it,
// because the file is the authority and not this package's constants.
func detectUnit(n ast.Node) string {
	for _, t := range n.Tokens(nil) {
		if ind, ok := indentOf(t); ok && ind != "" {
			return ind
		}
	}
	return indentUnit
}

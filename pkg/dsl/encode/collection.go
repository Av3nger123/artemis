package encode

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// The encoders and builders for imports, collections and uses, kept apart from
// node.go and build.go so the scenario grammar's two files read as they did.

func (e *enc) importDecl(i *ast.Import) *obj {
	return start(kindImport, i).
		set("path", i.Path.Value).
		span().
		comments(i.Tokens(nil), zero, zero).done()
}

func (e *enc) collection(c *ast.Collection) *obj {
	return start(kindCollection, c).
		set("name", nameObj(c.Name)).
		set("items", e.list(toNodes(c.Items))).
		span().
		comments(toks(c.Keyword, c.Name), c.LBrace, c.RBrace).done()
}

// params is a request's or a flow's parameter list. Its parentheses and commas
// are layout; an empty list is absent, and Decode writes `()`.
func (e *enc) params(ps *ast.Params) []any {
	if ps == nil {
		return nil
	}
	out := make([]any, 0, len(ps.List))
	for _, p := range ps.List {
		if o := e.node(p); o != nil {
			out = append(out, o)
		}
	}
	return out
}

// param carries no comments of its own: it sits on its declaration's head
// line, whose comment group holds them.
func (e *enc) param(p *ast.Param) *obj {
	return start(kindParam, p).
		set("name", p.Name.Text).
		set("secret", p.Secret.Text != "").
		set("default", e.node(p.Default)).done()
}

func (e *enc) requestDecl(r *ast.RequestDecl) *obj {
	return start(kindRequestDecl, r).
		set("name", r.Name.Text).
		set("params", e.params(r.Params)).
		set("action", e.node(r.Action)).
		set("body", e.list(toNodes(r.Body))).
		span().
		comments(append(toks(r.Keyword, r.Name), nodeToks(r.Params)...), r.LBrace, r.RBrace).done()
}

func (e *enc) flowDecl(f *ast.FlowDecl) *obj {
	return start(kindFlow, f).
		set("name", f.Name.Text).
		set("params", e.params(f.Params)).
		set("body", e.list(toNodes(f.Body))).
		span().
		comments(append(toks(f.Keyword, f.Name), nodeToks(f.Params)...), f.LBrace, f.RBrace).done()
}

func (e *enc) useDecl(u *ast.UseDecl) *obj {
	return start(kindUse, u).
		set("collection", u.Collection.Text).
		set("item", u.Item.Text).
		set("alias", u.Alias.Text).
		set("lines", e.list(toNodes(u.Lines))).
		span().
		comments(toks(u.Keyword, u.Collection, u.Dot, u.Item, u.As, u.Alias), u.LBrace, u.RBrace).done()
}

func (e *enc) bodySet(b *ast.BodySet) *obj {
	return start(kindBodySet, b).
		set("path", b.Keys()).
		set("value", e.node(b.Value)).
		span().
		comments(b.Tokens(nil), zero, zero).done()
}

func (e *enc) drop(d *ast.Drop) *obj {
	return start(kindDrop, d).span().comments(d.Tokens(nil), zero, zero).done()
}

func (e *enc) in(n *ast.In) *obj {
	return start(kindIn, n).
		set("step", nameObj(n.Step)).
		set("lines", e.list(toNodes(n.Lines))).
		span().
		comments(toks(n.Keyword, n.Step), n.LBrace, n.RBrace).done()
}

// importDecl builds `import "path"` from the decoded path, quoting it the way
// the lexer reads a string back.
func importDecl(f fields) (*ast.Import, error) {
	path, err := f.str("path")
	if err != nil {
		return nil, err
	}
	i := &ast.Import{Keyword: syn(token.Ident, "import"), Path: syn(token.String, quoteString(path))}
	return i, plain(f, &i.Keyword)
}

func collection(f fields) (*ast.Collection, error) {
	c := &ast.Collection{
		Keyword: syn(token.Ident, "collection"),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	var err error
	if c.Name, err = nameTok(f, "name"); err != nil {
		return nil, err
	}
	items, err := f.list("items")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		o, err := it.object()
		if err != nil {
			return nil, err
		}
		d, err := collectionItem(o)
		if err != nil {
			return nil, err
		}
		c.Items = append(c.Items, d)
	}
	return c, braced(f, &c.Keyword, &c.LBrace, &c.RBrace)
}

// collectionItem is what a collection holds: a request or a flow.
func collectionItem(f fields) (ast.Decl, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindRequestDecl:
		return requestDecl(f)
	case kindFlow:
		return flowDecl(f)
	}
	return nil, badKind(f, kind, "a collection item")
}

// paramsOf builds the parenthesised list, which is always written: `()` when
// there are no parameters.
func paramsOf(f fields) (*ast.Params, error) {
	ps := &ast.Params{LParen: syn(token.LParen, "("), RParen: syn(token.RParen, ")")}
	items, err := f.list("params")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		o, err := it.object()
		if err != nil {
			return nil, err
		}
		p, err := param(o)
		if err != nil {
			return nil, err
		}
		ps.List = append(ps.List, p)
	}
	return ps, nil
}

func param(f fields) (*ast.Param, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	if kind != kindParam {
		return nil, badKind(f, kind, "a parameter")
	}
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	secret, err := f.flag("secret")
	if err != nil {
		return nil, err
	}
	p := &ast.Param{Name: syn(token.Ident, name)}
	if secret {
		p.Secret = syn(token.Ident, "secret")
	}
	if p.Default, err = exprOf(f, "default", false); err != nil {
		return nil, err
	}
	if p.Default != nil {
		p.Assign = syn(token.Assign, "=")
	}
	return p, nil
}

func requestDecl(f fields) (*ast.RequestDecl, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	r := &ast.RequestDecl{
		Keyword: syn(token.Ident, "request"),
		Name:    syn(token.Ident, name),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	if r.Params, err = paramsOf(f); err != nil {
		return nil, err
	}
	if r.Action, err = action(f); err != nil {
		return nil, err
	}
	body, err := f.list("body")
	if err != nil {
		return nil, err
	}
	if r.Body, err = stmts(body, stepStmt); err != nil {
		return nil, err
	}
	return r, braced(f, &r.Keyword, &r.LBrace, &r.RBrace)
}

func flowDecl(f fields) (*ast.FlowDecl, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	fl := &ast.FlowDecl{
		Keyword: syn(token.Ident, "flow"),
		Name:    syn(token.Ident, name),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	if fl.Params, err = paramsOf(f); err != nil {
		return nil, err
	}
	body, err := f.list("body")
	if err != nil {
		return nil, err
	}
	for _, it := range body {
		o, err := it.object()
		if err != nil {
			return nil, err
		}
		d, err := flowItem(o)
		if err != nil {
			return nil, err
		}
		fl.Body = append(fl.Body, d)
	}
	return fl, braced(f, &fl.Keyword, &fl.LBrace, &fl.RBrace)
}

// flowItem is what a flow body holds: a step or a use.
func flowItem(f fields) (ast.Decl, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindStep:
		return step(f)
	case kindUse:
		return useDecl(f)
	}
	return nil, badKind(f, kind, "a flow item")
}

// useDecl writes a block when there are lines for it, or comments that were
// in one; `use a.b {}` and `use a.b` are the same use.
func useDecl(f fields) (*ast.UseDecl, error) {
	item, err := f.str("item")
	if err != nil {
		return nil, err
	}
	coll, err := f.optStr("collection")
	if err != nil {
		return nil, err
	}
	alias, err := f.optStr("alias")
	if err != nil {
		return nil, err
	}
	u := &ast.UseDecl{Keyword: syn(token.Ident, "use"), Item: syn(token.Ident, item)}
	if coll != "" {
		u.Collection, u.Dot = syn(token.Ident, coll), syn(token.Dot, ".")
	}
	if alias != "" {
		u.As, u.Alias = syn(token.Ident, "as"), syn(token.Ident, alias)
	}
	lines, err := f.list("lines")
	if err != nil {
		return nil, err
	}
	if u.Lines, err = stmts(lines, useLine); err != nil {
		return nil, err
	}
	c, _, err := commentsOf(f)
	if err != nil {
		return nil, err
	}
	if len(u.Lines) > 0 || len(c.open) > 0 || len(c.beforeClose) > 0 || len(c.afterClose) > 0 {
		u.LBrace, u.RBrace = syn(token.LBrace, "{"), syn(token.RBrace, "}")
		return u, braced(f, &u.Keyword, &u.LBrace, &u.RBrace)
	}
	return u, plain(f, &u.Keyword)
}

// useLine is one line of a use or an in block: an argument (a field), a body
// field, a drop, an expect, or an in block.
func useLine(f fields) (ast.Stmt, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindField:
		return field(f)
	case kindBodySet:
		return bodySet(f)
	case kindDrop:
		d := &ast.Drop{Keyword: syn(token.Ident, "drop"), What: syn(token.Ident, "expects")}
		return d, plain(f, &d.Keyword)
	case kindExpect:
		return expect(f)
	case kindIn:
		return in(f)
	}
	return nil, badKind(f, kind, "a use line")
}

func bodySet(f fields) (ast.Stmt, error) {
	path, err := f.strList("path")
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("%s: missing %q; a body field needs at least one name", f.path, "path")
	}
	b := &ast.BodySet{Body: syn(token.Ident, "body"), Assign: syn(token.Assign, "=")}
	for _, name := range path {
		b.Path = append(b.Path, ast.PathPart{Dot: syn(token.Dot, "."), Name: syn(token.Ident, name)})
	}
	if b.Value, err = exprOf(f, "value", true); err != nil {
		return nil, err
	}
	return b, plain(f, &b.Body)
}

func in(f fields) (ast.Stmt, error) {
	n := &ast.In{
		Keyword: syn(token.Ident, "in"),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	var err error
	if n.Step, err = nameTok(f, "step"); err != nil {
		return nil, err
	}
	lines, err := f.list("lines")
	if err != nil {
		return nil, err
	}
	if n.Lines, err = stmts(lines, useLine); err != nil {
		return nil, err
	}
	return n, braced(f, &n.Keyword, &n.LBrace, &n.RBrace)
}

// quoteString is s as a string literal the lexer decodes back to s: the
// escapes it reads, and `\$` only where a `$` would otherwise open `${`.
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '$' && i+1 < len(s) && s[i+1] == '{':
			b.WriteString(`\$`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
	return b.String()
}

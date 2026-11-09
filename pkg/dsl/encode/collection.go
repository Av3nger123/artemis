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
		set("as", optional(i.Name.Text)).
		span().
		comments(i.Tokens(nil), zero, zero).done()
}

func (e *enc) collection(c *ast.Collection) *obj {
	return start(kindCollection, c).
		set("name", nameObj(c.Name)).
		set("label", nameObj(c.Label)).
		set("items", e.list(toNodes(c.Items))).
		span().
		comments(toks(c.Keyword, c.Name, c.LabelAs, c.Label), c.LBrace, c.RBrace).done()
}

// params is a request's parameter list. Its parentheses and commas
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

// requestDecl writes keyword only for a command or a query, so a request
// encodes as it did before items had types. params is present, an empty list
// included, exactly when the item has a parenthesised list: `request r()` and
// `request r` are both items with nothing to pass, and only the key tells
// Decode which one to write.
func (e *enc) requestDecl(r *ast.RequestDecl) *obj {
	o := start(kindRequestDecl, r)
	if r.Keyword.Value != "request" {
		o.set("keyword", r.Keyword.Value)
	}
	o.set("name", r.Name.Text)
	o.set("label", nameObj(r.Label))
	if r.Params != nil {
		o.present("params", e.params(r.Params))
	}
	inputs := make([]ast.Node, 0, len(r.Inputs))
	for _, in := range r.Inputs {
		inputs = append(inputs, in)
	}
	return o.set("inputs", e.list(inputs)).
		set("action", e.node(r.Action)).
		set("body", e.list(toNodes(r.Body))).
		span().
		comments(append(append(toks(r.Keyword, r.Name), nodeToks(r.Params)...), toks(r.LabelAs, r.Label)...), r.LBrace, r.RBrace).done()
}

// useDecl leaves collection out for a bare ref and alias out when
// there is no `as`: absent, not empty, which is obj.set's rule for a missing
// value and what the schema promises.
//
// The step form, `step "log in" = auth.login { ... }`, is the same kind with
// the step's name: a use has no name, so its presence is the form.
func (e *enc) useDecl(u *ast.UseDecl) *obj {
	b := start(kindUse, u)
	if u.Name.Text != "" {
		b.set("name", nameObj(u.Name))
		b.set("label", nameObj(u.Label))
	}
	return b.
		set("collection", optional(u.Collection.Text)).
		set("item", u.Item.Text).
		set("alias", optional(u.Alias.Text)).
		set("lines", e.list(toNodes(u.Lines))).
		span().
		comments(toks(u.Step, u.Name, u.LabelAs, u.Label, u.Assign, u.Keyword, u.Collection, u.Dot, u.Item, u.As, u.Alias), u.LBrace, u.RBrace).done()
}

// present records key even when list is empty, for the one field whose
// presence is itself the information.
func (b *builder) present(key string, list []any) *builder {
	if list == nil {
		list = []any{}
	}
	if _, seen := b.o.vals[key]; !seen {
		b.o.keys = append(b.o.keys, key)
	}
	b.o.vals[key] = list
	return b
}

// optional is s, or nil -- which set drops -- when s is empty.
func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (e *enc) bodySet(b *ast.BodySet) *obj {
	return start(kindBodySet, b).
		set("path", b.Keys()).
		set("value", e.node(b.Value)).
		span().
		comments(b.Tokens(nil), zero, zero).done()
}

func (e *enc) drop(d *ast.Drop) *obj {
	return start(kindDrop, d).set("what", d.What.Value).span().comments(d.Tokens(nil), zero, zero).done()
}

// importDecl builds `import "path" [as name]` from the decoded path, quoting it the way
// the lexer reads a string back.
func importDecl(f fields) (*ast.Import, error) {
	path, err := f.str("path")
	if err != nil {
		return nil, err
	}
	as, err := f.optStr("as")
	if err != nil {
		return nil, err
	}
	i := &ast.Import{Keyword: syn(token.Ident, "import"), Path: syn(token.String, quoteString(path))}
	if as != "" {
		i.As, i.Name = syn(token.Ident, "as"), syn(token.Ident, as)
	}
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
	if c.LabelAs, c.Label, err = labelOf(f); err != nil {
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

// collectionItem is what a collection holds: a request.
func collectionItem(f fields) (ast.Decl, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindRequestDecl:
		return requestDecl(f)
	}
	return nil, badKind(f, kind, "a collection item")
}

// paramsOf builds the parenthesised list, which is written whenever the
// document has the params key: `()` when it is empty.
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
	keyword, err := f.optStr("keyword")
	if err != nil {
		return nil, err
	}
	if keyword == "" {
		keyword = "request"
	}
	if !token.IsCollectionItem(keyword) {
		return nil, fmt.Errorf("%s: %q is %q; want one of %s", f.path, "keyword", keyword, strings.Join(token.CollectionItems, ", "))
	}
	r := &ast.RequestDecl{
		Keyword: syn(token.Ident, keyword),
		Name:    syn(token.Ident, name),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	if r.LabelAs, r.Label, err = labelOf(f); err != nil {
		return nil, err
	}
	if f.has("params") {
		if r.Params, err = paramsOf(f); err != nil {
			return nil, err
		}
	}
	inputs, err := f.list("inputs")
	if err != nil {
		return nil, err
	}
	for _, it := range inputs {
		o, err := it.object()
		if err != nil {
			return nil, err
		}
		v, err := itemInput(o)
		if err != nil {
			return nil, err
		}
		r.Inputs = append(r.Inputs, v)
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

// itemInput is one input of an item: a var.
func itemInput(f fields) (*ast.VarDecl, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	if kind != kindVar {
		return nil, badKind(f, kind, "an item input")
	}
	return varDecl(f)
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
	// A name makes it the step form, which always has a block.
	stepForm := f.has("name")
	if stepForm {
		if u.Name, err = nameTok(f, "name"); err != nil {
			return nil, err
		}
		if u.LabelAs, u.Label, err = labelOf(f); err != nil {
			return nil, err
		}
		u.Keyword, u.Step, u.Assign = token.Token{}, syn(token.Ident, "step"), syn(token.Assign, "=")
	}
	first := &u.Keyword
	if stepForm {
		first = &u.Step
	}
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
	if stepForm || len(u.Lines) > 0 || len(c.open) > 0 || len(c.beforeClose) > 0 || len(c.afterClose) > 0 {
		u.LBrace, u.RBrace = syn(token.LBrace, "{"), syn(token.RBrace, "}")
		return u, braced(f, first, &u.LBrace, &u.RBrace)
	}
	return u, plain(f, first)
}

// useLine is one line of a use block: an argument (a field), a body field, a
// drop, or an expect.
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
		return drop(f)
	case kindExpect:
		return expect(f)
	}
	return nil, badKind(f, kind, "a use line")
}

// drop is `drop expects`, `drop captures` or `drop vars`. what is required: a
// document that left it out would otherwise silently mean one of them.
func drop(f fields) (ast.Stmt, error) {
	what, err := f.str("what")
	if err != nil {
		return nil, err
	}
	if !token.IsDropTarget(what) {
		return nil, fmt.Errorf("%s: %q is %q; want one of %s", f.path, "what", what, strings.Join(token.DropTargets, ", "))
	}
	d := &ast.Drop{Keyword: syn(token.Ident, "drop"), What: syn(token.Ident, what)}
	return d, plain(f, &d.Keyword)
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

// quoteString is s as a string literal the lexer decodes back to s: the
// escapes it reads, and `\$` only where a `$` would otherwise open `${`.
// QuoteString writes s as an Artemis string literal that means exactly s: a
// `${` in it is escaped, so it is never read as an interpolation.
func QuoteString(s string) string { return quoteString(s) }

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

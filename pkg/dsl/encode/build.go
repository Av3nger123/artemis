package encode

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// Building a tree from a document means building its tokens, and every one of
// them is synthetic: print.Synthetic, a zero span, and the exact text it should
// appear as. That is the convention pkg/dsl/print documents for a caller that
// builds nodes, and the zero span is what tells preserving mode this node has
// no bytes behind it.
//
// Punctuation is supplied here rather than carried in the document, because
// canonical layout owns it: a brace, a colon, a dot, a bracket and the `=` are
// written because the grammar needs them there, and a comma is not written at
// all -- canonical mode joins an inline list itself and breaks a long one onto
// separate lines.
//
// What the document *does* carry is every token whose spelling is the author's:
// a name, a method, a field name, an operator, a literal's source text, the
// text between the holes of an interpolated string. Nothing here invents one.

// syn is print.Synthetic under a shorter name, because this file is nothing but
// calls to it.
func syn(kind token.Kind, text string) token.Token { return print.Synthetic(kind, text) }

// decls builds a scenario body or a file's top level.
func decls(items []value) ([]ast.Decl, error) {
	out := make([]ast.Decl, 0, len(items))
	for _, it := range items {
		d, err := decl(it)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// decl builds one declaration: an import, a collection or a scenario at the
// top level, or one of the four things a scenario body holds. Which of them
// is legal where is the grammar's to say, and Decode's reparse says it.
func decl(v value) (ast.Decl, error) {
	f, err := v.object()
	if err != nil {
		return nil, err
	}
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindScenario:
		return scenario(f)
	case kindConfig:
		return config(f)
	case kindVar:
		return varDecl(f)
	case kindStep:
		return step(f)
	case kindImport:
		return importDecl(f)
	case kindCollection:
		return collection(f)
	case kindUse:
		return useDecl(f)
	}
	return nil, badKind(f, kind, "a declaration")
}

func scenario(f fields) (*ast.Scenario, error) {
	s := &ast.Scenario{
		Keyword: syn(token.Ident, "scenario"),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	var err error
	if s.Name, err = nameTok(f, "name"); err != nil {
		return nil, err
	}
	body, err := f.list("body")
	if err != nil {
		return nil, err
	}
	if s.Body, err = decls(body); err != nil {
		return nil, err
	}
	return s, braced(f, &s.Keyword, &s.LBrace, &s.RBrace)
}

func config(f fields) (*ast.ConfigDecl, error) {
	subject, err := f.str("subject")
	if err != nil {
		return nil, err
	}
	d := &ast.ConfigDecl{
		Keyword: syn(token.Ident, "config"),
		Subject: syn(token.Ident, subject),
	}
	if d.Block, err = blockOf(f, "block", true); err != nil {
		return nil, err
	}
	return d, plain(f, &d.Keyword)
}

func varDecl(f fields) (*ast.VarDecl, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	secret, err := f.flag("secret")
	if err != nil {
		return nil, err
	}
	d := &ast.VarDecl{
		Keyword: syn(token.Ident, "var"),
		Name:    syn(token.Ident, name),
		Assign:  syn(token.Assign, "="),
	}
	if secret {
		d.Secret = syn(token.Ident, "secret")
	}
	if d.Value, err = exprOf(f, "value", true); err != nil {
		return nil, err
	}
	return d, plain(f, &d.Keyword)
}

// step ignores stepType and scope. They are the checker's conclusions about the
// step, not input: a client sending a tree has no scope resolver of its own, and
// Decode's reparse-and-recheck recomputes both.
func step(f fields) (*ast.StepDecl, error) {
	s := &ast.StepDecl{
		Keyword: syn(token.Ident, "step"),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	var err error
	if s.Name, err = nameTok(f, "name"); err != nil {
		return nil, err
	}
	if s.Action, err = action(f); err != nil {
		return nil, err
	}
	body, err := f.list("body")
	if err != nil {
		return nil, err
	}
	if s.Body, err = stmts(body, stepStmt); err != nil {
		return nil, err
	}
	return s, braced(f, &s.Keyword, &s.LBrace, &s.RBrace)
}

// action builds the step's action block, which is the whole of the step-type
// inference: a verb makes it api, `run` terminal, `browser` browser.
func action(f fields) (ast.Action, error) {
	a, present, err := f.child("action")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("%s: missing %q; a step with no action is not a complete tree", f.path, "action")
	}
	kind, err := a.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindRequest:
		return request(a)
	case kindRun:
		return run(a)
	case kindBrowser:
		return browser(a)
	}
	return nil, badKind(a, kind, "an action")
}

func request(f fields) (*ast.Request, error) {
	method, err := f.str("method")
	if err != nil {
		return nil, err
	}
	r := &ast.Request{Method: syn(token.Ident, method)}
	if r.URL, err = exprOf(f, "url", true); err != nil {
		return nil, err
	}
	if r.Block, err = blockOf(f, "block", false); err != nil {
		return nil, err
	}
	return r, plain(f, &r.Method)
}

func run(f fields) (*ast.Run, error) {
	r := &ast.Run{Keyword: syn(token.Ident, "run")}
	var err error
	if r.Command, err = exprOf(f, "command", true); err != nil {
		return nil, err
	}
	if r.Block, err = blockOf(f, "block", false); err != nil {
		return nil, err
	}
	return r, plain(f, &r.Keyword)
}

func browser(f fields) (*ast.Browser, error) {
	b := &ast.Browser{
		Keyword: syn(token.Ident, "browser"),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	acts, err := f.list("acts")
	if err != nil {
		return nil, err
	}
	if b.Acts, err = stmts(acts, browserAct); err != nil {
		return nil, err
	}
	return b, braced(f, &b.Keyword, &b.LBrace, &b.RBrace)
}

func browserAct(f fields) (ast.Stmt, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	if kind != kindBrowserAct {
		return nil, badKind(f, kind, "a browser action")
	}
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	a := &ast.BrowserAct{Name: syn(token.Ident, name)}
	if a.Target, err = exprOf(f, "target", true); err != nil {
		return nil, err
	}
	if a.Value, err = exprOf(f, "value", false); err != nil {
		return nil, err
	}
	// The two-argument actions only: `click "x" =` is not a line, so the `=` is
	// written exactly when there is a value for it to introduce.
	if a.Value != nil {
		a.Assign = syn(token.Assign, "=")
	}
	return a, plain(f, &a.Name)
}

// stepStmt builds one statement of a step after its action: an expect, a
// capture, or a field of the step itself (`timeout = "5s"`, `retry { ... }`).
func stepStmt(f fields) (ast.Stmt, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindExpect:
		return expect(f)
	case kindCapture:
		return capture(f)
	case kindField:
		return field(f)
	}
	return nil, badKind(f, kind, "a step statement")
}

// expect ignores class, for the same reason step ignores stepType.
func expect(f fields) (ast.Stmt, error) {
	x := &ast.Expect{Keyword: syn(token.Ident, "expect")}
	var err error
	if x.Value, err = exprOf(f, "value", true); err != nil {
		return nil, err
	}
	if x.Budget, err = exprOf(f, "budget", false); err != nil {
		return nil, err
	}
	if x.Budget != nil {
		x.Within = syn(token.Ident, "within")
	}
	return x, plain(f, &x.Keyword)
}

func capture(f fields) (ast.Stmt, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	secret, err := f.flag("secret")
	if err != nil {
		return nil, err
	}
	c := &ast.Capture{
		Keyword: syn(token.Ident, "capture"),
		Name:    syn(token.Ident, name),
		Assign:  syn(token.Assign, "="),
	}
	if secret {
		c.Secret = syn(token.Ident, "secret")
	}
	if c.Value, err = exprOf(f, "value", true); err != nil {
		return nil, err
	}
	return c, plain(f, &c.Keyword)
}

// field is `name = value`, `name "key" = value` or `name { ... }`. Exactly one
// of value and block, which is the grammar's rule and is checked here because a
// node with both would print as two different lines depending on which the
// printer looked at first.
func field(f fields) (ast.Stmt, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	fl := &ast.Field{Name: syn(token.Ident, name)}
	if fl.Key, err = exprOf(f, "key", false); err != nil {
		return nil, err
	}
	if fl.Value, err = exprOf(f, "value", false); err != nil {
		return nil, err
	}
	if fl.Block, err = blockOf(f, "block", false); err != nil {
		return nil, err
	}
	if (fl.Value == nil) == (fl.Block == nil) {
		return nil, fmt.Errorf("%s: a field needs exactly one of %q and %q", f.path, "value", "block")
	}
	if fl.Value != nil {
		fl.Assign = syn(token.Assign, "=")
	}
	return fl, plain(f, &fl.Name)
}

// blockOf reads a brace-delimited field list. required says whether its absence
// is an error: a `config` declaration must have one, a request need not.
func blockOf(f fields, key string, required bool) (*ast.Block, error) {
	o, present, err := f.child(key)
	if err != nil {
		return nil, err
	}
	if !present {
		if required {
			return nil, fmt.Errorf("%s: missing %q", f.path, key)
		}
		return nil, nil
	}
	kind, err := o.kind()
	if err != nil {
		return nil, err
	}
	if kind != kindBlock {
		return nil, badKind(o, kind, "a block")
	}
	b := &ast.Block{LBrace: syn(token.LBrace, "{"), RBrace: syn(token.RBrace, "}")}
	items, err := o.list("fields")
	if err != nil {
		return nil, err
	}
	if b.Fields, err = stmts(items, blockField); err != nil {
		return nil, err
	}
	return b, braces(o, &b.LBrace, &b.RBrace)
}

func blockField(f fields) (ast.Stmt, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	if kind != kindField {
		return nil, badKind(f, kind, "a block field")
	}
	return field(f)
}

// stmts builds a statement list with one of the three statement builders, so
// that a browser block cannot hold an expect and a request block cannot hold a
// capture: the position decides what is legal, exactly as the grammar does.
func stmts(items []value, build func(fields) (ast.Stmt, error)) ([]ast.Stmt, error) {
	out := make([]ast.Stmt, 0, len(items))
	for _, it := range items {
		f, err := it.object()
		if err != nil {
			return nil, err
		}
		s, err := build(f)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// nameTok is a quoted name -- a scenario's or a step's -- rebuilt from the
// source text the document carries. The text is used and the value ignored:
// text is what the printer writes, and the lexer decodes the value again when
// Decode parses the printed file back.
func nameTok(f fields, key string) (token.Token, error) {
	o, present, err := f.child(key)
	if err != nil {
		return token.Token{}, err
	}
	if !present {
		return token.Token{}, fmt.Errorf("%s: missing %q", f.path, key)
	}
	text, err := o.str("text")
	if err != nil {
		return token.Token{}, err
	}
	return syn(token.String, text), nil
}

// badKind is the error for a kind that is not one this position accepts. It
// names the position, because "unknown node kind" without one leaves a client
// hunting through its own tree for it.
func badKind(f fields, kind, want string) error {
	if kind == kindBad {
		return fmt.Errorf("%s: this node did not parse when it was encoded; "+
			"a tree sent to artemis must be complete", f.path)
	}
	return fmt.Errorf("%s: %q is not %s", f.path, kind, want)
}

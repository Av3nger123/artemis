package check

import (
	"strings"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// valueKind is what a field's value has to be. It is deliberately coarse:
// this stage does not evaluate, so it can only hold a *literal* to account.
type valueKind uint8

const (
	kindAny      valueKind = iota // any expression
	kindArray                     // an array literal
	kindBool                      // true or false
	kindInt                       // a whole number
	kindDuration                  // a duration string
	kindBlock                     // not a value at all: `env { ... }`, `retry { ... }`
)

// fieldSpec is what one field name means in one block: whether it takes a
// `"key"` before its `=`, and what its value has to be.
type fieldSpec struct {
	key  bool // `header "Content-Type" = v` -- a key is required
	kind valueKind
}

// blockSpec is one block's fields plus the words a hint uses to name it. The
// five blocks of the grammar share ast.Block and ast.Field, so this table is
// the only thing that tells them apart -- which is the point of the
// shape/name split: the parser reads one shape, this decides what it meant.
type blockSpec struct {
	what   string // "a request block", "a run block"
	fields map[string]fieldSpec
	names  []string // in grammar order, for the hint and the did-you-mean
}

var (
	requestBlock = blockSpec{
		what: "a request block",
		fields: map[string]fieldSpec{
			"header": {key: true, kind: kindAny},
			"query":  {key: true, kind: kindAny},
			"body":   {kind: kindAny},
		},
		names: token.RequestFields,
	}

	runBlock = blockSpec{
		what: "a run block",
		fields: map[string]fieldSpec{
			"args":  {kind: kindArray},
			"cwd":   {kind: kindAny},
			"stdin": {kind: kindAny},
			"env":   {kind: kindBlock},
		},
		names: token.RunFields,
	}

	retryBlock = blockSpec{
		what: "a retry block",
		fields: map[string]fieldSpec{
			"times": {kind: kindInt},
			"delay": {kind: kindDuration},
		},
		names: token.RetryFields,
	}

	// stepBlock is not a block at all: `timeout = "5s"` and `retry { ... }`
	// are fields of the *step*, which is why ast.Field implements ast.Stmt.
	// They share this table because they share the check.
	stepBlock = blockSpec{
		what: "a step",
		fields: map[string]fieldSpec{
			"timeout": {kind: kindDuration},
			"retry":   {kind: kindBlock},
		},
		names: []string{"timeout", "retry"},
	}

	browserConfigBlock = blockSpec{
		what: "a config browser block",
		fields: map[string]fieldSpec{
			"headless": {kind: kindBool},
			"viewport": {kind: kindAny},
		},
		names: token.BrowserConfigFields,
	}
)

// request checks an api step's action: the URL and the request block's fields.
func (c *checker) request(r *ast.Request, v *view) {
	c.expr(r.URL, v)
	c.block(r.Block, requestBlock, v)
}

// run checks a terminal step's action.
func (c *checker) run(r *ast.Run, v *view) {
	c.expr(r.Command, v)
	c.block(r.Block, runBlock, v)
}

// browser checks a browser step's action: every act's arity and expressions.
func (c *checker) browser(b *ast.Browser, v *view) {
	for _, a := range b.Acts {
		act, ok := a.(*ast.BrowserAct)
		if !ok || act == nil {
			continue
		}
		c.expr(act.Target, v)
		c.expr(act.Value, v)
		c.browserArity(act)
	}
}

// oneArg are the browser actions that take a selector and nothing else; the
// other three -- fill, select, upload -- take a selector and a value.
//
// The parser reads whichever shape is written and reports nothing about it, so
// `click "x" = 1` arrives here as an act with a value and gets an arity
// diagnostic naming `click` rather than a syntax error about an `=`.
var oneArg = map[string]bool{"goto": true, "click": true, "press": true, "hover": true, "wait": true}

// actSignatures show the shape, which is more use in a hint than the count.
var actSignatures = map[string]string{
	"goto":   `goto "/orders"`,
	"click":  `click "text=Sign in"`,
	"press":  `press "Enter"`,
	"hover":  `hover ".menu"`,
	"wait":   `wait "1s"`,
	"fill":   `fill "#email" = "alice@example.com"`,
	"select": `select "#plan" = "pro"`,
	"upload": `upload "#avatar" = "me.png"`,
}

func (c *checker) browserArity(a *ast.BrowserAct) {
	if a.Name.Kind != token.Ident || !token.IsBrowserAction(a.Name.Value) {
		return // the parser already reported an unknown action.
	}
	name := a.Name.Value
	hasValue := a.Value != nil

	switch {
	case oneArg[name] && hasValue:
		c.bag.Error(a.Span(), diag.BadArity, "%q takes a selector and no value", name).
			Hintf("%s", actSignatures[name])
	case !oneArg[name] && !hasValue:
		c.bag.Error(a.Span(), diag.BadArity, "%q takes a selector and a value", name).
			Hintf("%s", actSignatures[name])
	}
}

// config checks `config <subject> { ... }`.
//
// The parser accepts any subject so that a typo gets a did-you-mean rather
// than a syntax error. A subject nobody recognises means the block's field
// names mean nothing either, so only their values are checked -- one
// diagnostic about the subject beats a second one per field inside it.
func (c *checker) config(d *ast.ConfigDecl, sc *scope) {
	v := sc.view(Unknown)

	if d.Subject.Kind != token.Ident {
		return
	}
	if c.reserved(d.Subject, "config subject") {
		return
	}
	if !contains(token.ConfigBlocks, d.Subject.Value) {
		ref := c.bag.Error(d.Subject.Span, diag.UnknownConfig,
			"unknown config subject %q", d.Subject.Value)
		if !ref.DidYouMean(d.Subject.Value, token.ConfigBlocks) {
			ref.Hintf("config configures: %s", strings.Join(token.ConfigBlocks, ", "))
		}
		c.values(d.Block, v)
		return
	}
	c.block(d.Block, browserConfigBlock, v)
}

// stepField checks `timeout = "5s"` or `retry { ... }`, the two fields of a
// step itself.
func (c *checker) stepField(f *ast.Field, v *view) {
	c.field(f, stepBlock, v)
}

// block checks every field in a block against spec.
func (c *checker) block(b *ast.Block, spec blockSpec, v *view) {
	if b == nil {
		return
	}
	for _, s := range b.Fields {
		if f, ok := s.(*ast.Field); ok && f != nil {
			c.field(f, spec, v)
		}
	}
}

// values checks only the expressions in a block, for a block whose field names
// have already been ruled meaningless.
func (c *checker) values(b *ast.Block, v *view) {
	if b == nil {
		return
	}
	for _, s := range b.Fields {
		f, ok := s.(*ast.Field)
		if !ok || f == nil {
			continue
		}
		c.expr(f.Key, v)
		c.expr(f.Value, v)
		c.values(f.Block, v)
	}
}

// field checks one field's name, its key, its shape and its value.
func (c *checker) field(f *ast.Field, spec blockSpec, v *view) {
	c.expr(f.Key, v)
	c.expr(f.Value, v)

	if f.Name.Kind != token.Ident {
		return
	}
	if c.reserved(f.Name, "field") {
		return
	}

	fs, known := spec.fields[f.Name.Value]
	if !known {
		ref := c.bag.Error(f.Name.Span, diag.UnknownField, "unknown field %q", f.Name.Value)
		if !ref.DidYouMean(f.Name.Value, spec.names) {
			ref.Hintf("%s holds: %s", spec.what, strings.Join(spec.names, ", "))
		}
		// The name means nothing, so neither do the names inside it, but its
		// expressions are still expressions and still worth resolving.
		c.values(f.Block, v)
		return
	}

	c.fieldKey(f, fs)
	c.fieldValue(f, fs)
	c.fieldBlock(f, fs, v)
}

// fieldKey checks the `"Content-Type"` in `header "Content-Type" = v`.
func (c *checker) fieldKey(f *ast.Field, fs fieldSpec) {
	switch {
	case fs.key && f.Key == nil:
		c.bag.Error(f.Name.Span, diag.BadValue, "%q needs a name before its value", f.Name.Value).
			Hintf(`%s "Content-Type" = "application/json"`, f.Name.Value)
	case !fs.key && f.Key != nil:
		c.bag.Error(f.Key.Span(), diag.BadValue, "%q takes no name", f.Name.Value).
			Hintf("%s = <value>", f.Name.Value)
	}
}

// fieldBlock checks that a field that takes a block got one, and the other way
// round, and descends into it.
//
// An `env { ... }` block is the one block whose field *names* are not checked
// against a list: they are environment variable names, so any identifier is
// legal and only a reserved word is not.
func (c *checker) fieldBlock(f *ast.Field, fs fieldSpec, v *view) {
	if fs.kind == kindBlock {
		if f.Block == nil {
			c.bag.Error(f.Name.Span, diag.BadValue, "%q is a block, not a value", f.Name.Value).
				Hintf("%s { ... }", f.Name.Value)
			return
		}
		if f.Name.Value == "env" {
			c.settings(f.Block, v)
			return
		}
		c.block(f.Block, retryBlock, v)
		return
	}
	if f.Block != nil {
		c.bag.Error(f.Block.Span(), diag.BadValue, "%q takes a value, not a block", f.Name.Value).
			Hintf("%s = <value>", f.Name.Value)
	}
}

// settings checks an `env { ... }` block: arbitrary names, checked values.
func (c *checker) settings(b *ast.Block, v *view) {
	for _, s := range b.Fields {
		f, ok := s.(*ast.Field)
		if !ok || f == nil {
			continue
		}
		c.expr(f.Key, v)
		c.expr(f.Value, v)
		if f.Name.Kind == token.Ident {
			c.reserved(f.Name, "env setting")
		}
	}
}

// fieldValue checks a value whose kind the field fixes.
//
// Only a literal is reported. `times = n` where n is a var could be anything,
// and this stage does not evaluate: a false "must be a whole number" on a
// correct file is worse than letting the evaluator say it.
func (c *checker) fieldValue(f *ast.Field, fs fieldSpec) {
	if f.Value == nil {
		return
	}
	switch fs.kind {
	case kindArray:
		if lit, ok := f.Value.(*ast.Literal); ok && lit != nil {
			c.bag.Error(lit.Span(), diag.BadValue,
				"%q must be an array, not %s", f.Name.Value, describeLiteral(lit)).
				Hintf(`%s = ["-f", "seed.sql"]`, f.Name.Value)
		}
	case kindBool:
		if lit, ok := f.Value.(*ast.Literal); ok && lit != nil && lit.Kind() != token.Bool {
			c.bag.Error(lit.Span(), diag.BadValue,
				"%q must be true or false, not %s", f.Name.Value, describeLiteral(lit)).
				Hintf("%s = true", f.Name.Value)
		}
	case kindInt:
		if lit, ok := f.Value.(*ast.Literal); ok && lit != nil && !isWholeNumber(lit) {
			c.bag.Error(lit.Span(), diag.BadValue,
				"%q must be a whole number, not %s", f.Name.Value, describeLiteral(lit)).
				Hintf("%s = 3", f.Name.Value)
		}
	case kindDuration:
		c.duration(f.Value)
	}
}

// duration checks a duration position: `timeout`, `retry`'s `delay`, and an
// `expect`'s `within` budget.
//
// This is the other half of what the design moves to compile time. `timeout =
// "5 secs"` is well-formed syntax -- a string on the right of an `=` -- so the
// parser has nothing to say, and today's equivalent YAML fails at run time.
// An interpolated budget (`within "${t}"`) is skipped: its text is not known
// until the run.
func (c *checker) duration(x ast.Expr) {
	lit, ok := x.(*ast.Literal)
	if !ok || lit == nil {
		return
	}
	if lit.Kind() != token.String {
		c.bag.Error(lit.Span(), diag.BadValue,
			"a duration is a string, not %s", describeLiteral(lit)).
			Hintf(`a duration is a number and a unit: "30s", "500ms", "2m"`)
		return
	}
	if _, err := time.ParseDuration(lit.Tok.Value); err != nil {
		c.bag.Error(lit.Span(), diag.InvalidDuration, "%q is not a duration", lit.Tok.Value).
			Hintf(`a duration is a number and a unit: "30s", "500ms", "2m"`)
	}
}

// isWholeNumber reports whether a literal is a number with no fraction or
// exponent in it, which is what `times = 3` means.
func isWholeNumber(l *ast.Literal) bool {
	if l.Kind() != token.Number {
		return false
	}
	return !strings.ContainsAny(l.Tok.Value, ".eE")
}

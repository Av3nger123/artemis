package ast

import "artemis/pkg/dsl/token"

// Request is an api step's action: an HTTP verb, a URL, and an optional block
// of fields.
//
// Method is the verb token (one of token.Methods). Its presence is what makes
// this an api step -- there is no type key anywhere in the language, and the
// checker reads nothing but this node's Go type to know a step binds `status`,
// `body`, `raw` and `headers`.
//
// Block is nil for `get "${url}/orders"` with no braces, which is the common
// case and legal: `Request = Method String [ "{" { ReqField } "}" ]`.
type Request struct {
	Method token.Token
	URL    Expr
	Block  *Block // nil when the verb took no block
}

func (r *Request) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, r.Method)
	dst = appendNode(dst, r.URL)
	return appendNode(dst, r.Block)
}

func (r *Request) Span() token.Span { return spanOf(r) }
func (r *Request) action()          {}

// Run is a terminal step's action: `run "psql" { args = [...] }`.
//
// Command is an Expr, not a string token, because the grammar is `Run = "run"
// Expr`: a command can be interpolated or held in a variable.
type Run struct {
	Keyword token.Token // run
	Command Expr
	Block   *Block // nil when the command took no block
}

func (r *Run) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, r.Keyword)
	dst = appendNode(dst, r.Command)
	return appendNode(dst, r.Block)
}

func (r *Run) Span() token.Span { return spanOf(r) }
func (r *Run) action()          {}

// Browser is a browser step's action: `browser { goto ... click ... }`.
//
// Unlike the other two the block is required, because a browser step with no
// actions does nothing at all.
// Acts is []Stmt rather than []*BrowserAct so that a line inside the block
// that did not parse can sit in it as a *Bad, the same way Block.Fields and
// Scenario.Body hold theirs. A browser block with one unknown action must not
// cost the actions around it.
type Browser struct {
	Keyword token.Token // browser
	LBrace  token.Token
	Acts    []Stmt
	RBrace  token.Token
}

func (b *Browser) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, b.Keyword)
	dst = appendTok(dst, b.LBrace)
	for _, a := range b.Acts {
		dst = appendNode(dst, a)
	}
	return appendTok(dst, b.RBrace)
}

func (b *Browser) Span() token.Span { return spanOf(b) }
func (b *Browser) action()          {}

// BrowserAct is one statement inside a browser block.
//
// One node covers both arities the grammar has. `goto E`, `click E`, `press
// E`, `hover E` and `wait E` take a target only; `fill E = E`, `select E = E`
// and `upload E = E` take a target and a value. So Assign and Value are zero
// for the first group, and which names belong to which group is
// token.BrowserActions plus the checker's arity rule -- the parser reads the
// shape that is written and reports nothing about it, so `click "x" = 1` gets
// an arity diagnostic naming `click` rather than a syntax error about `=`.
//
// Bad holds the tokens of an action whose body did not parse, so recovery
// inside a browser block is still lossless.
type BrowserAct struct {
	Name   token.Token // goto, click, fill, select, press, hover, upload, wait
	Target Expr
	Assign token.Token // zero for the one-argument actions
	Value  Expr        // nil for the one-argument actions
	Comma  token.Token // zero when the separator was a newline
}

func (a *BrowserAct) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, a.Name)
	dst = appendNode(dst, a.Target)
	dst = appendTok(dst, a.Assign)
	dst = appendNode(dst, a.Value)
	return appendTok(dst, a.Comma)
}

func (a *BrowserAct) Span() token.Span { return spanOf(a) }
func (a *BrowserAct) stmt()            {}

// Block is a brace-delimited list of fields. It is the body of `config
// browser`, of a request, of a `run`, of `retry`, and of a `run` block's
// `env`, because those are all the same shape: `"{" [ Field { Sep Field } ]
// "}"` with Sep a comma or a newline.
//
// Fields is []Stmt rather than []*Field so a line that did not parse can sit
// in it as a *Bad.
type Block struct {
	LBrace token.Token
	Fields []Stmt
	RBrace token.Token
}

func (b *Block) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, b.LBrace)
	for _, f := range b.Fields {
		dst = appendNode(dst, f)
	}
	return appendTok(dst, b.RBrace)
}

func (b *Block) Span() token.Span { return spanOf(b) }

// Field is `name = value`, `name "key" = value`, or `name { ... }`.
//
// One node covers eleven of the grammar's productions: ReqField (`header "X" =
// v`, `query "q" = v`, `body = v`), RunField (`args`, `cwd`, `stdin`, and `env
// { ... }`), RetryField (`times`, `delay`), Setting (a config or env setting),
// and the two step statements that are fields of the step itself -- `timeout =
// "5s"` and `retry { ... }`, which is why Field implements Stmt.
//
// The parser is shape-driven and the checker is name-driven. A field position
// accepts any identifier, so `statu = 3` parses and becomes ART-33's
// unknown-field with a did-you-mean hint, where rejecting it here would make
// it a bare syntax error with nothing to suggest. Which names are legal in
// which block, and which of them take a Key, lives in token/tables.go and
// reaches a UI through `artemis grammar --json`.
//
// Exactly one of Value and Block is set. Key is nil except for `header` and
// `query`. Comma is zero when the separator was a newline, so a trailing comma
// belongs to the field before it rather than floating between two.
type Field struct {
	Name   token.Token // header, query, body, args, cwd, stdin, env, times, delay, timeout, retry, headless, viewport
	Key    Expr        // the String in `header "X" = v`; nil otherwise
	Assign token.Token // zero when Block is set
	Value  Expr        // nil when Block is set
	Block  *Block      // `env { ... }`, `retry { ... }`; nil otherwise
	Comma  token.Token // zero when the separator was a newline
}

func (f *Field) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, f.Name)
	dst = appendNode(dst, f.Key)
	dst = appendTok(dst, f.Assign)
	dst = appendNode(dst, f.Value)
	dst = appendNode(dst, f.Block)
	return appendTok(dst, f.Comma)
}

func (f *Field) Span() token.Span { return spanOf(f) }
func (f *Field) stmt()            {}

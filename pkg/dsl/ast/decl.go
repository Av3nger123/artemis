package ast

import "artemis/pkg/dsl/token"

// File is one .art file: zero or more scenarios.
//
// An empty file is a valid File with no scenarios, because the grammar is
// `File = { Scenario }`. EOF is the stream's final token, kept because it
// carries the file's trailing trivia -- the last newline, a closing comment --
// and without it the round trip would lose the end of every file.
//
// Scenarios holds the file's top level in order: imports, collections and
// scenarios (and *Bad). It is []Decl rather than []*Scenario so that a top-level line that
// did not parse can sit in it as a *Bad, which is what keeps Source exact for
// a broken file.
type File struct {
	Scenarios []Decl
	EOF       token.Token
}

func (f *File) Tokens(dst []token.Token) []token.Token {
	for _, d := range f.Scenarios {
		dst = appendNode(dst, d)
	}
	return appendTok(dst, f.EOF)
}

func (f *File) Span() token.Span { return spanOf(f) }

// Scenario is `scenario "name" { ... }`.
type Scenario struct {
	Keyword token.Token // scenario
	Name    token.Token // the String naming it
	LBrace  token.Token
	Body    []Decl // ConfigDecl, VarDecl, StepDecl, or Bad
	RBrace  token.Token
}

func (s *Scenario) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, s.Keyword)
	dst = appendTok(dst, s.Name)
	dst = appendTok(dst, s.LBrace)
	for _, d := range s.Body {
		dst = appendNode(dst, d)
	}
	return appendTok(dst, s.RBrace)
}

func (s *Scenario) Span() token.Span { return spanOf(s) }
func (s *Scenario) decl()            {}

// ConfigDecl is `config browser { headless = true }`.
//
// Subject is the identifier after `config`, which decides what is being
// configured. Only `browser` has settings today (token.ConfigBlocks); the
// parser accepts any identifier and the checker rejects the rest, so a typo
// gets a did-you-mean rather than a syntax error.
type ConfigDecl struct {
	Keyword token.Token // config
	Subject token.Token // browser
	Block   *Block
}

func (c *ConfigDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, c.Keyword)
	dst = appendTok(dst, c.Subject)
	return appendNode(dst, c.Block)
}

func (c *ConfigDecl) Span() token.Span { return spanOf(c) }
func (c *ConfigDecl) decl()            {}

// VarDecl is `var url = env("API_URL")`, or `secret var pw = env("API_PASSWORD")`.
type VarDecl struct {
	// Secret is the `secret` modifier, and the zero token when it is absent.
	//
	// It is a token rather than a bool because pkg/dsl/print reproduces the
	// file from Tokens and spanOf reads the same slice, so a modifier with no
	// span would round-trip as a missing word.
	Secret  token.Token // `secret`, zero when the modifier is absent
	Keyword token.Token // var
	Name    token.Token // the Ident being bound
	Assign  token.Token // =
	Value   Expr
}

func (v *VarDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, v.Secret)
	dst = appendTok(dst, v.Keyword)
	dst = appendTok(dst, v.Name)
	dst = appendTok(dst, v.Assign)
	return appendNode(dst, v.Value)
}

func (v *VarDecl) Span() token.Span { return spanOf(v) }
func (v *VarDecl) decl()            {}

// StepDecl is `step "login" { <action> <stmt>... }`.
//
// This node is why the syntax is block-shaped. ART-1's result tree is run ->
// scenario -> step -> assertion and the console report prints a line per named
// step, so a step's name and its boundary have to be *syntactic*: Name is a
// token in the file, not an inferred label, and ART-37 hands this node
// straight to ART-15's Executor.
//
// Action is nil only in a file that already has a diagnostic -- a step with no
// action block, which the parser reports as missing-action rather than failing
// to parse. Body holds the statements in source order whether they came before
// or after the action; action-not-first is a diagnostic, not a reordering.
type StepDecl struct {
	Keyword token.Token // step
	Name    token.Token // the String naming it
	LBrace  token.Token
	Action  Action // nil on an erroring file
	Body    []Stmt
	RBrace  token.Token
}

// Tokens walks the step's items in source order, which is not the order of the
// struct fields: a statement written above the action block has to be emitted
// above it or Source would reorder the file. Order is the parse order recorded
// in items.
func (s *StepDecl) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, s.Keyword)
	dst = appendTok(dst, s.Name)
	dst = appendTok(dst, s.LBrace)
	for _, it := range s.items() {
		dst = appendNode(dst, it)
	}
	return appendTok(dst, s.RBrace)
}

// items is the action and the statements in the order they appear in the
// source, found by byte offset rather than remembered, so the ordering cannot
// disagree with the spans.
//
// Offsets compare only within one file and one copy. pkg/dsl/expand builds
// steps that mix statements copied out of a collection with lines written at
// the use, elsewhere; so only the items that share File and Via with the
// action (or, with no action, the first located item) are ordered by offset,
// and every other item follows them in slice order. A parsed file's items
// all share both, and order exactly by offset.
func (s *StepDecl) items() []Node {
	out := make([]Node, 0, len(s.Body)+1)
	if !isNil(s.Action) {
		out = append(out, s.Action)
	}
	for _, st := range s.Body {
		out = append(out, st)
	}
	own, foreign := SplitOrigin(out)
	for i := 1; i < len(own); i++ {
		for j := i; j > 0 && own[j].Span().Offset < own[j-1].Span().Offset; j-- {
			own[j], own[j-1] = own[j-1], own[j]
		}
	}
	return append(own, foreign...)
}

// SplitOrigin splits items into those written where the first located one
// was -- same File, same Via -- and the rest, each in slice order. An item
// with a zero span locates nothing and goes with the first group.
func SplitOrigin(items []Node) (own, foreign []Node) {
	var ref token.Span
	for _, it := range items {
		if sp := it.Span(); !sp.IsZero() {
			ref = sp
			break
		}
	}
	own = make([]Node, 0, len(items))
	for _, it := range items {
		sp := it.Span()
		if sp.IsZero() || (sp.File == ref.File && sp.Via == ref.Via) {
			own = append(own, it)
		} else {
			foreign = append(foreign, it)
		}
	}
	return own, foreign
}

func (s *StepDecl) Span() token.Span { return spanOf(s) }
func (s *StepDecl) decl()            {}

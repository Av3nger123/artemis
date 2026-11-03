package ast

import "artemis/pkg/dsl/token"

// Flow is `flow checkout { ... }`: the steps of a test in order, and the vars
// its scenarios may set. A flow file holds one, beside its imports. The body
// is what a scenario's body may hold.
type Flow struct {
	Keyword token.Token // flow
	Name    token.Token // the Ident naming it
	LBrace  token.Token
	Body    []Decl // VarDecl, ConfigDecl, StepDecl, UseDecl, PhaseDecl, BlockDecl, or Bad
	RBrace  token.Token
}

func (f *Flow) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, f.Keyword)
	dst = appendTok(dst, f.Name)
	dst = appendTok(dst, f.LBrace)
	for _, d := range f.Body {
		dst = appendNode(dst, d)
	}
	return appendTok(dst, f.RBrace)
}
func (f *Flow) Span() token.Span { return spanOf(f) }
func (f *Flow) decl()            {}

// FlowRef is `flow "../flows/checkout.art"`, the first line of a scenario
// file: the one flow every scenario in the file runs. The path is relative to
// the scenario file.
type FlowRef struct {
	Keyword token.Token // flow
	Path    token.Token // the String naming the flow file
}

func (r *FlowRef) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, r.Keyword)
	return appendTok(dst, r.Path)
}
func (r *FlowRef) Span() token.Span { return spanOf(r) }
func (r *FlowRef) decl()            {}

// Set is `quantity = 3`, one line of a scenario in a scenario file: the value
// the scenario gives one of its flow's vars. A Value that is the Ident skip
// turns off every expect that reads the var.
type Set struct {
	Name   token.Token // the Ident of the flow var
	Assign token.Token // =
	Value  Expr
}

func (s *Set) Tokens(dst []token.Token) []token.Token {
	dst = appendTok(dst, s.Name)
	dst = appendTok(dst, s.Assign)
	return appendNode(dst, s.Value)
}
func (s *Set) Span() token.Span { return spanOf(s) }
func (s *Set) decl()            {}

// IsSkip reports whether the set's value is skip.
func (s *Set) IsSkip() bool {
	id, ok := s.Value.(*Ident)
	return ok && id.Name() == "skip"
}

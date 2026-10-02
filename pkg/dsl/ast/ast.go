// Package ast is the Artemis DSL's syntax tree. It is a *concrete* tree: every
// node holds the actual token.Token for every token it was built from -- the
// braces, the commas, the `=`, the keyword word -- and not merely a span and a
// copy of the trivia around it.
//
// That is the decision the rest of the package follows from, so it is worth
// saying why. Subsystem D's acceptance test is
//
//	parse(src) -> tree -> print(tree) == src     // byte-identical
//
// and `var pw  = env("X")` has two spaces before its `=`. The only thing in the
// world that knows about those two spaces is that `=` token's leading trivia.
// A tree that recorded spans and re-synthesised punctuation could not be made
// lossless afterwards without being rewritten, which is the same trap
// pkg/dsl/lexer avoided by keeping comments and blank lines.
//
// The invariant that makes it checkable:
//
//	ast.Source(tree) == src
//
// for every input, because every non-marker token in the stream is held by
// exactly one node. Source is concatenation and nothing else; pkg/dsl/print
// owns the two real print modes.
//
// Nodes are built by pkg/dsl/parser and read by pkg/dsl/check, pkg/dsl/print,
// pkg/dsl/lower, pkg/dsl/encode and pkg/codegen. A tree may be *invalid* -- a
// step with no action, a Bad node where a line did not parse -- because the
// parser recovers rather than giving up, so a consumer must tolerate a nil
// Action and a nil Expr anywhere one is optional.
package ast

import "artemis/pkg/dsl/token"

// Node is anything in the tree.
//
// Tokens appends every token this node holds, in source order, which is what
// makes both Span and Source derived rather than stored: there is one place a
// node says what it is made of, so a node that gains a field and forgets to
// report it fails the round-trip test instead of silently losing bytes.
//
// Implementations append their children's tokens too, so the File node's
// Tokens is the whole stream.
type Node interface {
	Tokens(dst []token.Token) []token.Token

	// Span covers the node from its first token to its last.
	Span() token.Span
}

// Decl is a declaration: a scenario, or one of the three things a scenario
// body holds.
type Decl interface {
	Node
	decl()
}

// Stmt is a statement inside a step after its action: expect, capture, or a
// field (`timeout = "5s"`, `retry { ... }`).
type Stmt interface {
	Node
	stmt()
}

// Action is a step's action block, the one thing in a step that decides the
// step's type. There is no `type:` key: *Request is an api step, *Run a
// terminal step, *Browser a browser step, and pkg/dsl/check reads nothing but
// which of the three this is to know.
type Action interface {
	Node
	action()
}

// Expr is an expression.
type Expr interface {
	Node
	expr()
}

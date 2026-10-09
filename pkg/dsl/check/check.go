// Package check resolves names in a parsed .art tree and reports what it
// cannot resolve.
//
// It is the stage where the DSL earns its diagnostics. The parser is
// shape-driven: a field position accepts any identifier, `is <anything>`
// parses, a browser action takes whichever arity is written. Everything that
// is wrong with a *name* rather than with a shape lands here, which is what
// lets `expect statu == 200` be "unknown field, did you mean status" with a
// mechanical fix instead of a bare syntax error with nothing to suggest.
//
// # Step-type inference is first, and that ordering is the whole design
//
// There is no `type:` key anywhere in the language. A step's type is the Go
// type of its action block -- *ast.Request is api, *ast.Run is terminal,
// *ast.Browser is browser -- and TypeOf is the only thing that decides it. The
// checker therefore knows a step's type *before* it resolves any name in it,
// which is what makes per-type scopes possible at all: `status` resolves in an
// api step and is a compile error in a browser one, and the error can name the
// roots that would have worked.
//
// A step with no action, or one whose action did not parse, has no type. Such
// a step is skipped entirely rather than checked against an empty scope: the
// parser has already reported missing-action, and a cascade of
// "status is not in scope" under it would bury the one diagnostic that says
// what to do.
//
// # What comes back
//
//	info, bag := check.Check(tree)
//
// The bag is every diagnostic, in source order. The Info is the two facts the
// rest of the front end would otherwise each re-derive: every step's type, and
// whether every `expect` is form-shaped (Simple) or free text (Complex). They
// are a side table keyed by node pointer rather than fields on the tree
// because pkg/dsl/ast is *concrete* -- every field of every node is source --
// and a Type field with no tokens behind it would be a field ast.Source must
// skip, print must ignore and encode must decide how to read back.
//
// # Two things move to compile time
//
// An unknown name and an uncompilable regex. Both are run-time failures today,
// and both are the class of fault where finding out forty minutes into a suite
// is the actual cost. The file either compiles or it does not.
package check

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// StepType is what a step does, inferred from its action block.
type StepType uint8

const (
	// Unknown is a step whose action block is missing or did not parse. It has
	// no scope and is not checked.
	Unknown StepType = iota

	// API is a step whose action is an HTTP verb.
	API

	// Terminal is a step whose action is `run`.
	Terminal

	// Browser is a step whose action is `browser { ... }`.
	Browser
)

var typeNames = map[StepType]string{
	Unknown:  "unknown",
	API:      "api",
	Terminal: "terminal",
	Browser:  "browser",
}

// String is the name the design document uses, which is also what a
// diagnostic prints and what `artemis grammar --json` will serialise, so the
// three cannot disagree.
func (t StepType) String() string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return "steptype(?)"
}

// article is "an" for api and "a" for the rest, so a message reads
// `"status" is not in scope in a browser step` and `... in an api step`.
func (t StepType) article() string {
	if t == API {
		return "an"
	}
	return "a"
}

// TypeOf is the step-type inference, exported and pure so that ART-37's
// lowerer and ART-43's encoder ask this function rather than re-switching on
// the action's type and risking a fourth answer.
func TypeOf(a ast.Action) StepType {
	switch a := a.(type) {
	case *ast.Request:
		if a != nil {
			return API
		}
	case *ast.Run:
		if a != nil {
			return Terminal
		}
	case *ast.Browser:
		if a != nil {
			return Browser
		}
	}
	return Unknown
}

// Class is whether an `expect` has the shape a form can render as three
// widgets.
type Class uint8

const (
	// Complex is an expression a UI renders as one raw text field with live
	// validation. It is the zero value, so a node nobody classified reads as
	// the safe answer rather than as a promise a form cannot keep.
	Complex Class = iota

	// Simple is `<path> <op> <literal>`, `<path> exists`, `<path> is <type>`,
	// or the `not` form of any of those: a path picker, an operator dropdown
	// and a value field.
	Simple
)

func (c Class) String() string {
	if c == Simple {
		return "simple"
	}
	return "complex"
}

// Info is what the checker concluded, keyed by the nodes it concluded it
// about.
//
// Everything is read through a method so that a nil Info answers rather than
// panicking -- a client that has a tree but never ran the checker gets Unknown
// and Complex, which are both the conservative answer -- and so that the maps
// cannot be written to from outside.
type Info struct {
	steps   map[*ast.StepDecl]StepType
	expects map[*ast.Expect]Class
	scopes  map[*ast.StepDecl][]string
	envs    []EnvNeed
	envSeen map[string]bool
}

// EnvNeed is one env() call whose variable name the checker could read: a
// single string literal, with no interpolation and no default value.
//
// The check before a run reads this list. A call the checker cannot read --
// env(name), env("${prefix}_URL") -- is not here and is reported by pkg/eval
// at the time of use instead, which is the same split this package applies to
// a match() pattern that arrives in a var.
type EnvNeed struct {
	Name string
	Span token.Span
}

// StepType is the type inferred for s.
func (i *Info) StepType(s *ast.StepDecl) StepType {
	if i == nil {
		return Unknown
	}
	return i.steps[s]
}

// Class is e's simple/complex label, the one the JSON encoding carries so that
// no client re-derives it.
func (i *Info) Class(e *ast.Expect) Class {
	if i == nil {
		return Complex
	}
	return i.expects[e]
}

// Scope is every name resolvable in s, in the order the checker offers them as
// did-you-mean candidates: the step type's own roots, then the scenario's
// vars, then the captures of earlier steps. A UI's path picker is this list.
//
// The returned slice is a copy.
func (i *Info) Scope(s *ast.StepDecl) []string {
	if i == nil {
		return nil
	}
	out := make([]string, len(i.scopes[s]))
	copy(out, i.scopes[s])
	return out
}

// Steps is how many steps were typed, which is the cheap "did this run" check
// a test wants.
func (i *Info) Steps() int {
	if i == nil {
		return 0
	}
	return len(i.steps)
}

// EnvNeeds is every environment variable this file names in an env() call the
// checker could read, in source order, each one once.
func (i *Info) EnvNeeds() []EnvNeed {
	if i == nil {
		return nil
	}
	out := make([]EnvNeed, len(i.envs))
	copy(out, i.envs)
	return out
}

// Check resolves every name in tree.
//
// It returns a non-nil Info and a non-nil Bag for every input, including a
// tree full of ast.Bad nodes: the parser recovers rather than giving up, so
// reporting on the parts of a broken file that did parse is strictly more
// useful than reporting nothing, and nothing here may panic on a nil Action, a
// nil Expr or a Bad in any position.
func Check(tree *ast.File) (*Info, *diag.Bag) {
	c := &checker{
		bag: diag.New(),
		info: &Info{
			steps:   map[*ast.StepDecl]StepType{},
			expects: map[*ast.Expect]Class{},
			scopes:  map[*ast.StepDecl][]string{},
			envSeen: map[string]bool{},
		},
	}
	if tree != nil {
		for _, d := range tree.Scenarios {
			if s, ok := d.(*ast.Scenario); ok && s != nil {
				c.scenario(s)
			}
		}
	}
	return c.info, c.bag
}

// checker carries the bag and the Info being built. The scope is passed down
// the descent rather than held here, because a scenario's declarations and a
// step's statements see different ones and a field on the checker would make
// that an ordering bug waiting to happen.
type checker struct {
	bag  *diag.Bag
	info *Info
}

// scenario walks one scenario's body in source order.
//
// Source order is load-bearing twice over. A `var` sees the vars above it and
// no others, and a `capture` is in scope for the steps *after* the one that
// writes it -- so both of those are "what has been seen so far", which is only
// meaningful if the walk is in reading order. ast.Scenario.Body already is.
func (c *checker) scenario(s *ast.Scenario) {
	sc := newScope()
	sc.file = s.Keyword.Span.File

	// Every var of the scenario is a name a capture may not take, whether it
	// is declared above the capture or below: either way the scenario would
	// hold two values under one name. So the vars are collected before the
	// walk, while the walk itself still binds them in order for resolution.
	for _, d := range s.Body {
		if v, ok := d.(*ast.VarDecl); ok && v != nil && v.Name.Kind == token.Ident {
			sc.bind(v.Name, "var")
		}
	}

	for _, d := range s.Body {
		switch d := d.(type) {
		case *ast.ConfigDecl:
			c.config(d, sc)
		case *ast.VarDecl:
			c.varDecl(d, sc)
		case *ast.StepDecl:
			c.step(d, sc)
		}
		// *ast.Bad and anything else: the parser has already reported it.
	}
}

// varDecl checks a `var`'s value and then binds its name.
//
// The value is checked *before* the name is bound, so `var x = x` is an
// unknown name rather than a self-reference that resolves to itself.
func (c *checker) varDecl(v *ast.VarDecl, sc *scope) {
	if v == nil {
		return
	}
	if c.reserved(v.Name, "var") {
		return
	}
	c.expr(v.Value, sc.view(Unknown))
	if v.Name.Kind == token.Ident {
		// The vars were bound before the walk, so the first var of a name is
		// its first binding; any other var of that name is a second value
		// under it, and lower would quietly keep the last for both.
		if first := sc.first[v.Name.Value]; first.tok.Span != v.Name.Span {
			c.clash(v.Name, first, sc)
			return
		}
		sc.addVar(v.Name.Value)
	}
}

// step types the step, builds its scope, checks everything in it, and only
// then banks its captures.
func (c *checker) step(s *ast.StepDecl, sc *scope) {
	t := TypeOf(s.Action)
	c.info.steps[s] = t

	// A step with no usable action has no type, so there is no scope to check
	// against. The parser's missing-action already named the problem; adding
	// "status is not in scope in an unknown step" under it would be a second
	// diagnostic for one mistake, and the misleading one would be second.
	if t == Unknown {
		return
	}

	v := sc.view(t)
	c.info.scopes[s] = v.names()

	// items() is the action and the statements in source order, which is what
	// the parser recorded and not the order of the struct's fields: a
	// statement written above the action block is reported above it.
	for _, it := range ast.Children(s) {
		switch it := it.(type) {
		case *ast.Request:
			c.request(it, v)
		case *ast.Run:
			c.run(it, v)
		case *ast.Browser:
			c.browser(it, v)
		case *ast.Expect:
			c.expect(it, v)
		case *ast.Capture:
			c.capture(it, v)
		case *ast.Field:
			c.stepField(it, v)
		}
	}

	// After the step, not during it: a capture is in scope for the steps that
	// follow, which is the design's "each capture from an *earlier* step".
	for _, st := range s.Body {
		if cap, ok := st.(*ast.Capture); ok && cap != nil && cap.Name.Kind == token.Ident {
			c.rebinds(cap.Name, sc)
			sc.addCapture(cap.Name.Value)
		}
	}
}

// rebinds reports a capture whose name a var or an earlier capture already
// holds -- varDecl reports a var whose name an earlier var holds -- and otherwise records it as the name's first binding. SPEC.md: "a
// capture may not reuse the name of a var or of an earlier capture, because
// silently shadowing a value is how a scenario comes to assert against the
// wrong one."
//
// When either binding was copied in by a use -- its span is stamped with Via,
// or it sits in another file than the scenario -- the fix is usually `as`,
// which prefixes a use's captures, so the hint says so.
func (c *checker) rebinds(name token.Token, sc *scope) {
	if token.IsReserved(name.Value) {
		return // reserved already said what is wrong with this name
	}
	first, ok := sc.first[name.Value]
	if !ok {
		sc.bind(name, "capture")
		return
	}
	c.clash(name, first, sc)
}

// clash reports name, a var or capture, as a second binding of a name first
// bound at first.
func (c *checker) clash(name token.Token, first binding, sc *scope) {
	where := fmt.Sprintf("the %s at line %d", first.kind, first.tok.Span.Line)
	if first.tok.Span.File != name.Span.File {
		where += " of " + first.tok.Span.File
	}
	hint := where + " binds it first"
	if sc.fromUse(first.tok.Span) || sc.fromUse(name.Span) {
		hint += "\nuse ... as <name> to keep both"
	}
	c.bag.Error(name.Span, diag.DuplicateBinding, "%q is already bound", name.Value).Hintf("%s", hint)
}

// expect checks the assertion and records its simple/complex label.
func (c *checker) expect(e *ast.Expect, v *view) {
	if e == nil {
		return
	}
	c.expr(e.Value, v)
	c.info.expects[e] = classify(e)

	if e.Budget != nil {
		c.expr(e.Budget, v)
		c.duration(e.Budget)
	}
}

// capture checks the captured expression and the name it binds. The name is
// not added to the scope here -- step does that, after the whole step -- so a
// capture cannot be read in the step that writes it.
func (c *checker) capture(cap *ast.Capture, v *view) {
	if cap == nil {
		return
	}
	if c.reserved(cap.Name, "capture") {
		return
	}
	c.expr(cap.Value, v)
}

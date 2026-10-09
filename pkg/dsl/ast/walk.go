package ast

// Visitor is called for every node Walk reaches. Returning false stops the
// walk descending into that node's children; the walk continues with its
// siblings.
type Visitor func(Node) bool

// Walk calls v for n and then, if v returned true, for each of n's children in
// source order.
//
// Four packages need this -- the checker resolving names, the printer in
// canonical mode, the lowerer building runtime steps, the encoder writing JSON
// -- so it lives here rather than being written four times with four different
// ideas of child order. Source order is the contract: a checker reporting
// diagnostics as it walks produces them in reading order, which is what
// diag.Bag's sort then only has to confirm.
//
// A nil node is skipped, since a recovered parse leaves nil where an optional
// child was.
func Walk(n Node, v Visitor) {
	if n == nil || !v(n) {
		return
	}
	for _, c := range Children(n) {
		Walk(c, v)
	}
}

// Inspect is Walk with a visitor that cannot stop the descent, for the common
// case of looking at everything.
func Inspect(n Node, f func(Node)) {
	Walk(n, func(n Node) bool {
		f(n)
		return true
	})
}

// Children returns n's child nodes in source order, skipping nil ones.
//
// Tokens is deliberately not reused here: it flattens a whole subtree into
// tokens, and a walk needs the nodes between. The two lists are maintained
// side by side on each type, and a child missing from either shows up -- in
// the round-trip test for Tokens, in a checker that never visits a subtree for
// this.
func Children(n Node) []Node {
	switch n := n.(type) {
	case *File:
		return decls(n.Scenarios)
	case *Scenario:
		return decls(n.Body)
	case *ConfigDecl:
		return nodes(n.Block)
	case *VarDecl:
		return nodes(n.Value)
	case *StepDecl:
		// items, not Action-then-Body, because a statement written above the
		// action block has to be visited above it: a checker walking a step
		// reports in reading order only if the walk is in reading order.
		return n.items()
	case *Request:
		return append(nodes(n.URL), nodes(n.Block)...)
	case *Run:
		return append(nodes(n.Command), nodes(n.Block)...)
	case *Browser:
		out := make([]Node, 0, len(n.Acts))
		for _, a := range n.Acts {
			out = append(out, nodes(a)...)
		}
		return out
	case *BrowserAct:
		return append(nodes(n.Target), nodes(n.Value)...)
	case *Block:
		out := make([]Node, 0, len(n.Fields))
		for _, f := range n.Fields {
			out = append(out, nodes(f)...)
		}
		return out
	case *Field:
		out := nodes(n.Key)
		out = append(out, nodes(n.Value)...)
		return append(out, nodes(n.Block)...)
	case *Expect:
		return append(nodes(n.Value), nodes(n.Budget)...)
	case *Capture:
		return nodes(n.Value)
	case *Binary:
		return append(nodes(n.X), nodes(n.Y)...)
	case *Unary:
		return nodes(n.X)
	case *Exists:
		return nodes(n.X)
	case *IsType:
		return nodes(n.X)
	case *Member:
		return nodes(n.X)
	case *Index:
		return append(nodes(n.X), nodes(n.Index)...)
	case *Call:
		out := nodes(n.Callee)
		for _, a := range n.Args {
			out = append(out, nodes(a.Value)...)
		}
		return out
	case *Object:
		out := make([]Node, 0, 2*len(n.Entries))
		for _, e := range n.Entries {
			out = append(out, nodes(e.Key)...)
			out = append(out, nodes(e.Value)...)
		}
		return out
	case *Array:
		out := make([]Node, 0, len(n.Elems))
		for _, e := range n.Elems {
			out = append(out, nodes(e.Value)...)
		}
		return out
	case *Paren:
		return nodes(n.X)
	case *Interp:
		out := make([]Node, 0, len(n.Segments))
		for _, s := range n.Segments {
			out = append(out, nodes(s.Expr)...)
		}
		return out
	case *Import:
		return nil
	case *Collection:
		return decls(n.Items)
	case *Params:
		out := make([]Node, 0, len(n.List))
		for _, p := range n.List {
			out = append(out, nodes(p)...)
		}
		return out
	case *Param:
		return nodes(n.Default)
	case *RequestDecl:
		return append(nodes(n.Params), n.items()...)
	case *FlowDecl:
		return append(nodes(n.Params), decls(n.Body)...)
	case *UseDecl:
		return stmts(n.Lines)
	case *BodySet:
		return nodes(n.Value)
	case *Drop:
		return nil
	case *In:
		return stmts(n.Lines)
	case *Ident, *Literal, *Bad:
		return nil
	}
	return nil
}

// nodes is the one-element case of a child list, dropping a nil child.
func nodes(n Node) []Node {
	if isNil(n) {
		return nil
	}
	return []Node{n}
}

func decls(ds []Decl) []Node {
	out := make([]Node, 0, len(ds))
	for _, d := range ds {
		out = append(out, nodes(d)...)
	}
	return out
}

func stmts(ss []Stmt) []Node {
	out := make([]Node, 0, len(ss))
	for _, s := range ss {
		out = append(out, nodes(s)...)
	}
	return out
}

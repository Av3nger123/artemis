package lower

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
)

// Node is one item of a scenario's flow: a *Step, or a *Flow holding more.
type Node interface{ flowNode() }

func (*Step) flowNode() {}
func (*Flow) flowNode() {}

// Flow is a lowered control-flow block: parallel, if, switch, try or repeat.
//
// Each kind is branches over bodies of nodes, so the runner has one shape to
// walk. What a branch means is its Keyword:
//
//	parallel   one "branch" per item of the block, all run at once
//	if         "if", "else if", "else": the first whose Cond holds runs
//	switch     "case", "default": the first whose Cond equals Subject runs
//	try        "try", then "catch" when it failed, then "finally" always
//	repeat     one "body", run until Until holds or a bound is reached
type Flow struct {
	Kind     string
	Line     int
	Branches []*Branch

	// Subject is a switch's value, compared with each case's Cond.
	Subject ast.Expr

	// Every, Within and Max are a repeat's pace and bounds, and Until the
	// condition that ends it; UntilText is the condition as written, for a
	// report.
	Every, Within, Max ast.Expr
	Until              ast.Expr
	UntilText          string
}

// Branch is one alternative or arm of a Flow.
type Branch struct {
	Keyword string
	// Cond is an if's condition or a case's value; nil for an else, a
	// default, a parallel branch, and a try's three.
	Cond     ast.Expr
	CondText string
	Body     []Node
	Line     int
}

// Steps are the steps of nodes at every depth, in source order.
func Steps(nodes []Node) []*Step {
	var out []*Step
	for _, n := range nodes {
		switch n := n.(type) {
		case *Step:
			out = append(out, n)
		case *Flow:
			for _, b := range n.Branches {
				out = append(out, Steps(b.Body)...)
			}
		}
	}
	return out
}

// lowerer carries what lowering a scenario's body accumulates: the secret
// names in scope and the captures made so far, both in source order.
type lowerer struct {
	info     *check.Info
	secrets  map[string]bool
	captured map[string]bool
	sc       *Scenario
	file     string
}

// body lowers a list of declarations -- a scenario's own steps, a phase's, a
// block's -- into nodes. A var among them is one a use hoisted, and is bound
// with the scenario's own.
func (l *lowerer) body(decls []ast.Decl) ([]Node, error) {
	var out []Node
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.VarDecl:
			if d.Name.Kind == token.Ident {
				l.sc.Vars = append(l.sc.Vars, &Var{Name: d.Name.Value, Value: d.Value, Line: d.Name.Span.Line})
			}
		case *ast.StepDecl:
			st, err := l.step(d)
			if err != nil {
				return nil, err
			}
			out = append(out, st)
		case *ast.FlowDecl:
			f, err := l.flow(d)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
		// *ast.Bad: already reported by the parser.
	}
	return out, nil
}

// step lowers one step where it is written: its needs are the captures above
// it, and its own captures join the scope below it.
func (l *lowerer) step(d *ast.StepDecl) (*Step, error) {
	st, err := step(d, l.info, l.secrets)
	if err != nil {
		return nil, err
	}
	st.relativeTo(l.file)
	st.Needs = needs(d, l.captured)
	// After the step, never before it: a capture is not in scope for the
	// step that writes it.
	addSecretCaptures(l.secrets, st)
	addCaptured(l.captured, st)
	return st, nil
}

// flow lowers a control-flow block.
func (l *lowerer) flow(d *ast.FlowDecl) (*Flow, error) {
	if len(d.Clauses) == 0 {
		return nil, fmt.Errorf("a block with no clause")
	}
	first := d.Clauses[0]
	f := &Flow{Kind: d.Kind(), Line: first.Keyword.Span.Line}
	switch f.Kind {
	case ast.FlowParallel:
		for _, item := range first.Body {
			body, err := l.body([]ast.Decl{item})
			if err != nil {
				return nil, err
			}
			if len(body) == 0 {
				continue
			}
			f.Branches = append(f.Branches, &Branch{Keyword: "branch", Body: body, Line: item.Span().Line})
		}
	case ast.FlowSwitch:
		f.Subject = first.Expr
		for _, item := range first.Body {
			cl, ok := item.(*ast.Clause)
			if !ok {
				continue
			}
			b, err := l.branch(cl)
			if err != nil {
				return nil, err
			}
			f.Branches = append(f.Branches, b)
		}
	case ast.FlowRepeat:
		for _, o := range first.Options {
			switch o.Name.Value {
			case "every":
				f.Every = o.Value
			case "within":
				f.Within = o.Value
			case "max":
				f.Max = o.Value
			}
		}
		b, err := l.branch(first)
		if err != nil {
			return nil, err
		}
		b.Keyword = "body"
		f.Branches = []*Branch{b}
		f.Until, f.UntilText = d.Cond, source(d.Cond)
	default: // if and try: one branch per clause
		for _, cl := range d.Clauses {
			b, err := l.branch(cl)
			if err != nil {
				return nil, err
			}
			f.Branches = append(f.Branches, b)
		}
	}
	return f, nil
}

// branch lowers one clause into a branch.
func (l *lowerer) branch(cl *ast.Clause) (*Branch, error) {
	kw := cl.Keyword.Value
	if cl.If.Text != "" {
		kw = "else if"
	}
	body, err := l.body(cl.Body)
	if err != nil {
		return nil, err
	}
	return &Branch{Keyword: kw, Cond: cl.Expr, CondText: source(cl.Expr), Body: body, Line: cl.Keyword.Span.Line}, nil
}

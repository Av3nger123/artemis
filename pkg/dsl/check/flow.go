package check

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// items walks a body of declarations in order: steps, the vars a use hoisted,
// and nested blocks.
func (c *checker) items(decls []ast.Decl, sc *scope) {
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.StepDecl:
			c.step(d, sc)
		case *ast.VarDecl:
			c.varDecl(d, sc)
		case *ast.FlowDecl:
			c.flow(d, sc)
		}
	}
}

// flow checks a control-flow block and decides what the scope after it holds.
//
// The rule is the one a sequence already follows -- a capture is in scope
// below the step that makes it -- applied to what a block guarantees:
//
//   - parallel: every branch sees the captures above the block and none of
//     its siblings', which may not exist yet; after the block, all of them.
//   - if and switch: each branch sees the captures above it; after the block,
//     the captures every branch makes. A capture only some make may not exist
//     there, and reading one is an error outside cleanup.
//   - try: the try's captures, as a sequence; a catch's are only one
//     branch's; a finally's, always.
//   - repeat: the body's, as a sequence -- it runs at least once -- and the
//     until condition reads them.
func (c *checker) flow(f *ast.FlowDecl, sc *scope) {
	if len(f.Clauses) == 0 {
		return
	}
	first := f.Clauses[0]
	switch f.Kind() {
	case ast.FlowParallel:
		c.parallel(first, sc)
	case ast.FlowIf:
		for _, cl := range f.Clauses {
			c.expr(cl.Expr, sc.view(Unknown))
		}
		last := f.Clauses[len(f.Clauses)-1]
		exhaustive := last.Keyword.Value == "else" && last.If.Text == ""
		c.branches(f.Clauses, sc, exhaustive, fmt.Sprintf("the if at line %d", first.Keyword.Span.Line))
	case ast.FlowSwitch:
		c.expr(first.Expr, sc.view(Unknown))
		var cases []*ast.Clause
		exhaustive := false
		for _, d := range first.Body {
			if cl, ok := d.(*ast.Clause); ok {
				c.expr(cl.Expr, sc.view(Unknown))
				cases = append(cases, cl)
				exhaustive = exhaustive || cl.Keyword.Value == "default"
			}
		}
		c.branches(cases, sc, exhaustive, fmt.Sprintf("the switch at line %d", first.Keyword.Span.Line))
	case ast.FlowTry:
		c.items(first.Body, sc)
		for _, cl := range f.Clauses[1:] {
			switch cl.Keyword.Value {
			case "catch":
				c.branches([]*ast.Clause{cl}, sc, false, fmt.Sprintf("the catch at line %d", cl.Keyword.Span.Line))
			case "finally":
				was := sc.cleanup
				sc.cleanup = true
				c.items(cl.Body, sc)
				sc.cleanup = was
			}
		}
	case ast.FlowRepeat:
		c.repeat(f, sc)
	}
}

// parallel checks a parallel block: each item of its body is a branch.
func (c *checker) parallel(cl *ast.Clause, sc *scope) {
	made := map[string]token.Token{}
	browsers := 0
	var merged []*scope
	// What each branch captures, read off the tree before any is walked, so
	// a branch that reads a sibling's capture is told so whichever is written
	// first.
	byBranch := make([]map[string]int, len(cl.Body))
	for i, d := range cl.Body {
		byBranch[i] = map[string]int{}
		ast.Inspect(d, func(n ast.Node) {
			if cp, ok := n.(*ast.Capture); ok && cp.Name.Kind == token.Ident {
				byBranch[i][cp.Name.Value] = d.Span().Line
			}
		})
	}
	for i, d := range cl.Body {
		branch := sc.fork()
		for j, names := range byBranch {
			if j == i {
				continue
			}
			for name, line := range names {
				branch.siblings[name] = line
			}
		}
		base := len(branch.captures)
		c.items([]ast.Decl{d}, branch)
		for _, name := range branch.captures[base:] {
			tok := branch.first[name].tok
			if prev, dup := made[name]; dup {
				c.bag.Error(tok.Span, diag.ParallelCaptureClash,
					"%q is captured by two branches of this parallel block, which run at once", name).
					Hintf("the other is at line %d; give one of them another name", prev.Span.Line)
				continue
			}
			made[name] = tok
		}
		if drivesBrowser(d) {
			browsers++
			if browsers == 2 {
				c.bag.Error(d.Span(), diag.ParallelBrowser,
					"a second branch of this parallel block drives the browser, and a scenario has one page").
					Hintf("move the browser steps into one branch, or out of the parallel block")
			}
		}
		branch.siblings = sc.siblings
		merged = append(merged, branch)
	}
	base := len(sc.captures)
	for _, b := range merged {
		c.adopt(sc, b, b.captures[base:])
	}
}

// branches checks alternatives -- an if's, a switch's cases, a catch -- each
// from the scope above the block. After it, the scope holds what every branch
// captures when exhaustive is true, and records as maybe what only some do.
func (c *checker) branches(cls []*ast.Clause, sc *scope, exhaustive bool, where string) {
	count := map[string]int{}
	var order []string
	var forks []*scope
	for _, cl := range cls {
		b := sc.fork()
		base := len(b.captures)
		c.items(cl.Body, b)
		seen := map[string]bool{}
		for _, name := range b.captures[base:] {
			if seen[name] {
				continue
			}
			seen[name] = true
			if count[name] == 0 {
				order = append(order, name)
			}
			count[name]++
		}
		forks = append(forks, b)
	}
	for _, name := range order {
		var from *scope
		for _, b := range forks {
			if _, ok := b.first[name]; ok && contains(b.captures, name) {
				from = b
				break
			}
		}
		if exhaustive && count[name] == len(cls) {
			c.adopt(sc, from, []string{name})
			continue
		}
		sc.maybe[name] = where
		if bind, ok := from.first[name]; ok {
			if _, taken := sc.first[name]; !taken {
				sc.first[name] = bind
			}
		}
	}
}

// adopt brings names a branch captured into the scope after the block.
func (c *checker) adopt(sc, branch *scope, names []string) {
	for _, name := range names {
		if contains(sc.captures, name) {
			continue
		}
		sc.addCapture(name)
		delete(sc.maybe, name)
		if bind, ok := branch.first[name]; ok {
			if _, taken := sc.first[name]; !taken {
				sc.first[name] = bind
			}
		}
	}
}

// repeat checks a repeat block: its pace and bounds, its body as a sequence,
// and its until condition against what the body captured.
func (c *checker) repeat(f *ast.FlowDecl, sc *scope) {
	cl := f.Clauses[0]
	bounded := false
	seen := map[string]bool{}
	for _, o := range cl.Options {
		c.expr(o.Value, sc.view(Unknown))
		name := o.Name.Value
		if seen[name] {
			c.bag.Error(o.Name.Span, diag.BadValue, "this repeat already says %s", name)
		}
		seen[name] = true
		switch name {
		case "every", "within":
			c.duration(o.Value)
		case "max":
			if lit, ok := o.Value.(*ast.Literal); ok && (!isWholeNumber(lit) || lit.Tok.Value == "0") {
				c.bag.Error(lit.Span(), diag.BadValue, "max is how many rounds a repeat may run, a whole number above 0").
					Hintf("max 30")
			}
		}
		bounded = bounded || name == "within" || name == "max"
	}
	if !bounded {
		c.bag.Error(cl.Keyword.Span, diag.RepeatUnbounded,
			"this repeat has no bound, so a condition that never holds would run it forever").
			Hintf(`repeat every "2s" within "2m" { ... } until ...`)
	}
	c.items(cl.Body, sc)
	if f.Cond != nil {
		c.expr(f.Cond, sc.view(Unknown))
	}
}

// drivesBrowser reports whether d holds a browser step at any depth.
func drivesBrowser(d ast.Decl) bool {
	found := false
	ast.Inspect(d, func(n ast.Node) {
		if _, ok := n.(*ast.Browser); ok {
			found = true
		}
	})
	return found
}

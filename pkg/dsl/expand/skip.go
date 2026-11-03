package expand

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// skipState is what a var a scenario sets to skip holds at a point of the
// flow, on the paths that reach it.
type skipState uint8

const (
	skipped skipState = iota // skip on every path: nothing has written it yet
	maybe                    // written on some paths, still skip on others
	written                  // written on every path: an ordinary value from here on
)

// join is the state after paths that end in states: theirs when they agree,
// and maybe when they do not.
func join(states ...skipState) skipState {
	for _, s := range states[1:] {
		if s != states[0] {
			return maybe
		}
	}
	return states[0]
}

// prune applies `name = skip` to an expanded scenario body, walking it in
// order. While name is skipped, each expect that reads it is removed, and
// each object entry whose value is exactly name is left out; any other read
// is skip-read. Once a step writes name -- a capture, or a var after an
// action -- it is an ordinary value, and what reads it after is kept. Where
// only some paths have written it, a read is skip-maybe-written. The var
// itself is dropped from the top of the body.
//
// The body is the scenario's own copy, and is changed in place.
func prune(body []ast.Decl, name string, report func(token.Span, diag.Code, string)) []ast.Decl {
	p := &pruner{name: name, report: report}
	out, _ := p.decls(body, skipped, true)
	return out
}

// pruner is one prune. A dry pruner follows the state through a body and
// changes and reports nothing: what a repeat's second round starts from.
type pruner struct {
	name   string
	report func(token.Span, diag.Code, string)
	dry    bool
}

// decls prunes a list of declarations entered in state st, and returns it
// with the state it leaves. top is the scenario's own body, whose var of the
// name is dropped.
func (p *pruner) decls(body []ast.Decl, st skipState, top bool) ([]ast.Decl, skipState) {
	out := make([]ast.Decl, 0, len(body))
	for _, d := range body {
		switch v := d.(type) {
		case *ast.VarDecl:
			if top && !p.dry && v.Name.Value == p.name {
				continue
			}
			v.Value = p.expr(v.Value, st)
		case *ast.ConfigDecl:
			p.block(v.Block, st)
		case *ast.StepDecl:
			st = p.step(v, st)
		case *ast.PhaseDecl:
			v.Body, st = p.decls(v.Body, st, false)
		case *ast.BlockDecl:
			st = p.blockDecl(v, st)
		}
		out = append(out, d)
	}
	return out, st
}

// step prunes one step: its action and its lines read the state it is
// entered in, and its writes set the state it leaves.
func (p *pruner) step(s *ast.StepDecl, st skipState) skipState {
	switch a := s.Action.(type) {
	case *ast.Request:
		a.URL = p.expr(a.URL, st)
		p.block(a.Block, st)
	case *ast.Run:
		a.Command = p.expr(a.Command, st)
		p.block(a.Block, st)
	case *ast.DB:
		a.Conn = p.expr(a.Conn, st)
		p.block(a.Block, st)
	case *ast.Browser:
		a.Acts = p.stmts(a.Acts, st)
	}
	s.Body = p.stmts(s.Body, st)
	for _, x := range s.Body {
		if c, ok := x.(*ast.Capture); ok && c.Name.Value == p.name {
			return written
		}
	}
	return st
}

// blockDecl prunes a control-flow block entered in state st, and returns the
// state after it.
func (p *pruner) blockDecl(b *ast.BlockDecl, st skipState) skipState {
	if st == written {
		return st
	}
	switch b.Kind() {
	case ast.BlockParallel:
		// Every branch runs, each from the state before the block.
		c := b.Clauses[0]
		after, branches := st, false
		var body []ast.Decl
		for _, d := range c.Body {
			out, end := p.decls([]ast.Decl{d}, st, false)
			body = append(body, out...)
			switch {
			case end == written:
				after = written
			case end == maybe && after != written:
				after = maybe
			}
			branches = true
		}
		if branches {
			c.Body = body
		}
		return after

	case ast.BlockIf:
		// One branch runs; with no else, perhaps none.
		var ends []skipState
		plainElse := false
		for _, c := range b.Clauses {
			c.Expr = p.expr(c.Expr, st)
			var end skipState
			c.Body, end = p.decls(c.Body, st, false)
			ends = append(ends, end)
			if c.Keyword.Value == "else" && c.If.Text == "" {
				plainElse = true
			}
		}
		if !plainElse {
			ends = append(ends, st)
		}
		return join(ends...)

	case ast.BlockSwitch:
		sw := b.Clauses[0]
		sw.Expr = p.expr(sw.Expr, st)
		ends := []skipState{}
		hasDefault := false
		for _, d := range sw.Body {
			c, ok := d.(*ast.Clause)
			if !ok {
				continue
			}
			c.Expr = p.expr(c.Expr, st)
			var end skipState
			c.Body, end = p.decls(c.Body, st, false)
			ends = append(ends, end)
			if c.Keyword.Value == "default" {
				hasDefault = true
			}
		}
		if !hasDefault {
			ends = append(ends, st)
		}
		return join(ends...)

	case ast.BlockTry:
		// A catch runs after part of the try; a finally after either.
		tried, after := st, st
		for _, c := range b.Clauses {
			switch c.Keyword.Value {
			case "try":
				c.Body, tried = p.decls(c.Body, st, false)
				after = tried
			case "catch":
				var end skipState
				c.Body, end = p.decls(c.Body, join(st, tried), false)
				after = join(tried, end)
			case "finally":
				var end skipState
				c.Body, end = p.decls(c.Body, join(st, after), false)
				if end == written {
					after = written
				}
			}
		}
		return after

	case ast.BlockRepeat:
		// A round after the first starts from what the round before left,
		// so a read above the write sees skip only in the first.
		c := b.Clauses[0]
		for _, o := range c.Options {
			o.Value = p.expr(o.Value, st)
		}
		_, end := (&pruner{name: p.name, dry: true}).decls(c.Body, st, false)
		c.Body, end = p.decls(c.Body, join(st, end), false)
		b.Cond = p.expr(b.Cond, end)
		return end
	}

	ends := []skipState{st}
	for _, c := range b.Clauses {
		c.Expr = p.expr(c.Expr, st)
		var end skipState
		c.Body, end = p.decls(c.Body, st, false)
		ends = append(ends, end)
	}
	return join(ends...)
}

// stmts prunes a step's lines, or a block's fields, in state st.
func (p *pruner) stmts(list []ast.Stmt, st skipState) []ast.Stmt {
	if st == written {
		return list
	}
	out := list[:0:0]
	for _, x := range list {
		switch v := x.(type) {
		case *ast.Expect:
			if at, ok := p.firstRead(v.Value, v.Budget); ok {
				if st == skipped && !p.dry {
					continue // an expect on a skipped var is not run
				}
				p.read(at, st)
			}
		case *ast.Capture:
			v.Value = p.expr(v.Value, st)
		case *ast.Field:
			v.Key = p.expr(v.Key, st)
			v.Value = p.expr(v.Value, st)
			p.block(v.Block, st)
		case *ast.BrowserAct:
			v.Target = p.expr(v.Target, st)
			v.Value = p.expr(v.Value, st)
		case *ast.BodySet:
			v.Value = p.expr(v.Value, st)
		}
		out = append(out, x)
	}
	return out
}

func (p *pruner) block(b *ast.Block, st skipState) {
	if b != nil {
		b.Fields = p.stmts(b.Fields, st)
	}
}

// expr prunes one expression in state st: an object entry whose value is
// exactly the skipped var is left out, and any other read is reported.
func (p *pruner) expr(x ast.Expr, st skipState) ast.Expr {
	if st == written || isNil(x) {
		return x
	}
	switch v := x.(type) {
	case *ast.Ident:
		if v.Tok.Value == p.name {
			p.read(v.Tok.Span, st)
		}
	case *ast.Object:
		kept := v.Entries[:0:0]
		for _, en := range v.Entries {
			if id, ok := en.Value.(*ast.Ident); ok && id.Tok.Value == p.name {
				if st == skipped {
					continue // the field is left out
				}
				p.read(id.Tok.Span, st)
			} else {
				en.Value = p.expr(en.Value, st)
			}
			if _, named := en.Key.(*ast.Ident); !named {
				en.Key = p.expr(en.Key, st)
			}
			kept = append(kept, en)
		}
		if !p.dry {
			if len(kept) > 0 && len(kept) < len(v.Entries) {
				// The entry that was last ends the object, without a comma
				// after it unless the source had one.
				kept[len(kept)-1].Comma = v.Entries[len(v.Entries)-1].Comma
			}
			v.Entries = kept
		}
	case *ast.Interp:
		for i := range v.Segments {
			v.Segments[i].Expr = p.expr(v.Segments[i].Expr, st)
		}
	case *ast.Unary:
		v.X = p.expr(v.X, st)
	case *ast.Binary:
		v.X = p.expr(v.X, st)
		v.Y = p.expr(v.Y, st)
	case *ast.Exists:
		v.X = p.expr(v.X, st)
	case *ast.IsType:
		v.X = p.expr(v.X, st)
	case *ast.Member:
		v.X = p.expr(v.X, st)
	case *ast.Index:
		v.X = p.expr(v.X, st)
		v.Index = p.expr(v.Index, st)
	case *ast.Call:
		for i := range v.Args {
			v.Args[i].Value = p.expr(v.Args[i].Value, st)
		}
	case *ast.Array:
		for i := range v.Elems {
			v.Elems[i].Value = p.expr(v.Elems[i].Value, st)
		}
	case *ast.Paren:
		v.X = p.expr(v.X, st)
	}
	return x
}

// firstRead is where the first of xs reads the skipped var, if one does.
func (p *pruner) firstRead(xs ...ast.Expr) (token.Span, bool) {
	var at token.Span
	found := false
	for _, x := range xs {
		reads(x, func(id *ast.Ident) {
			if !found && id.Tok.Value == p.name {
				at, found = id.Tok.Span, true
			}
		})
	}
	return at, found
}

// read reports a read of the skipped var at at, in state st, which is never
// written: there the read is fine.
func (p *pruner) read(at token.Span, st skipState) {
	if p.dry {
		return
	}
	switch st {
	case skipped:
		p.report(at, diag.SkipRead, p.name+" is skip here, and only an expect or an object field can leave it out")
	case maybe:
		p.report(at, diag.SkipMaybeWritten, p.name+" can still be skip here: only some paths above write it")
	}
}

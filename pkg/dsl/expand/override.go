package expand

import (
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// override applies a use's override lines to the steps the use expanded to.
// use is the use line and via its number in the use table; steps are what a
// request expanded to, and flowSteps a flow's directly written steps by the
// name the flow gave them, for in "<step>" { ... }.
//
// Every expression an override places -- a header's value, a body field's,
// an appended expect -- was written at the use and is not stamped. A token
// there was never any source for (a brace, an object key) is placed at the
// override line and carries via, so a diagnostic on it names the use.
func (e *expander) override(use *ast.UseDecl, via int, steps []*ast.StepDecl, lines []ast.Stmt, isFlow bool, flowSteps map[string]*ast.StepDecl) {
	var own []ast.Stmt
	for _, l := range lines {
		switch v := l.(type) {
		case *ast.In:
			e.in(use, via, v, isFlow, flowSteps)
		case *ast.Field:
			if token.IsUseLine(v.Name.Value) {
				own = append(own, l)
			}
		case *ast.BodySet, *ast.Drop, *ast.Expect:
			own = append(own, l)
		}
	}
	if isFlow {
		names := stepNames(flowSteps)
		for _, l := range own {
			r := e.bag.Error(l.Span(), diag.BadOverride, "a flow has several steps; aim this at one with in \"<step>\" { ... }")
			if len(names) > 0 {
				r.Hintf("%s has steps %s", use.Ref(), strings.Join(names, ", "))
			}
		}
		return
	}
	for _, s := range steps {
		e.apply(via, s, own, false)
	}
}

// in applies one in block to the flow step it names.
func (e *expander) in(use *ast.UseDecl, via int, n *ast.In, isFlow bool, flowSteps map[string]*ast.StepDecl) {
	if !isFlow {
		e.bag.Error(n.Span(), diag.BadOverride, "in targets a step of a flow; %s is a request", use.Ref())
		return
	}
	name := n.Step.Value
	s := flowSteps[name]
	if s == nil {
		names := stepNames(flowSteps)
		r := e.bag.Error(n.Step.Span, diag.UnknownFlowStep, "%s has no step %q", use.Ref(), name)
		if !r.DidYouMean(name, names) {
			if len(names) == 0 {
				r.Hintf("%s writes no steps of its own", use.Ref())
			} else {
				r.Hintf("%s has steps %s", use.Ref(), strings.Join(names, ", "))
			}
		}
		return
	}
	e.apply(via, s, n.Lines, true)
}

// stepNames is a flow's step names, sorted so diagnostics are stable.
func stepNames(m map[string]*ast.StepDecl) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// apply applies override lines to one step, in order. inBlock is whether the
// lines are an in block's, where an argument or another in is out of place.
func (e *expander) apply(via int, s *ast.StepDecl, lines []ast.Stmt, inBlock bool) {
	seenExpect := false
	for _, l := range lines {
		switch v := l.(type) {
		case *ast.Field:
			if !token.IsUseLine(v.Name.Value) {
				if inBlock {
					e.bag.Error(v.Span(), diag.BadOverride, "an in block holds overrides, not arguments").
						Hintf("pass %s to the use itself", v.Name.Value)
				}
				continue
			}
			req := e.request(s, v.Name)
			if req == nil {
				continue
			}
			switch v.Name.Value {
			case "header", "query":
				e.keyed(via, req, v)
			case "body":
				e.wholeBody(via, s, req, v)
			}
		case *ast.BodySet:
			if req := e.request(s, v.Body); req != nil {
				e.setBodyField(via, s, req, v)
			}
		case *ast.Drop:
			// drop captures removes the template's captures. A use adds no
			// capture of its own, so unlike drop expects it has no place it
			// must come before.
			if v.What.Value == "captures" {
				s.Body = without[*ast.Capture](s.Body)
				continue
			}
			if seenExpect {
				e.bag.Error(v.Span(), diag.DropAfterExpect, "drop expects must come before this use's own expect lines")
				continue
			}
			s.Body = without[*ast.Expect](s.Body)
		case *ast.Expect:
			seenExpect = true
			x := ast.Clone(v, identity)
			x.Comma = token.Token{}
			s.Body = append(s.Body, x)
		case *ast.In:
			e.bag.Error(v.Span(), diag.BadOverride, "an in block cannot hold another in; it already aims at one step")
		}
	}
}

// without is body less every statement of type T, in a fresh slice: the
// template's own slice is never written through.
func without[T ast.Stmt](body []ast.Stmt) []ast.Stmt {
	kept := body[:0:0]
	for _, st := range body {
		if _, ok := st.(T); !ok {
			kept = append(kept, st)
		}
	}
	return kept
}

// request is s's api action, or nil -- with a diagnostic at word -- when s is
// some other kind of step.
func (e *expander) request(s *ast.StepDecl, word token.Token) *ast.Request {
	switch a := s.Action.(type) {
	case *ast.Request:
		return a
	case *ast.Run:
		e.bag.Error(word.Span, diag.BadOverride, "%s overrides an api step; this is a terminal step", word.Value)
	case *ast.Browser:
		e.bag.Error(word.Span, diag.BadOverride, "%s overrides an api step; this is a browser step", word.Value)
	}
	return nil
}

// keyed is a header or query override: replace the value of the field with
// the same key -- a header's compared case-insensitively -- or add one.
func (e *expander) keyed(via int, req *ast.Request, f *ast.Field) {
	b := block(via, req, f.Span())
	for _, x := range b.Fields {
		g, ok := x.(*ast.Field)
		if ok && g.Name.Value == f.Name.Value && sameKey(f.Name.Value == "header", g.Key, f.Key) {
			g.Value = ast.Clone(f.Value, identity)
			return
		}
	}
	b.Fields = append(b.Fields, placed(f))
}

// sameKey reports whether two field keys are the same string literal. A key
// that is not a literal matches nothing, so its override always adds.
func sameKey(fold bool, a, b ast.Expr) bool {
	x, ok1 := a.(*ast.Literal)
	y, ok2 := b.(*ast.Literal)
	if !ok1 || !ok2 || x.Kind() != token.String || y.Kind() != token.String {
		return false
	}
	if fold {
		return strings.EqualFold(x.Tok.Value, y.Tok.Value)
	}
	return x.Tok.Value == y.Tok.Value
}

// wholeBody is `body = v`: replace the body, or add one.
func (e *expander) wholeBody(via int, s *ast.StepDecl, req *ast.Request, f *ast.Field) {
	_, obj := f.Value.(*ast.Object)
	e.literalBody(s, &obj)
	if g := bodyField(req); g != nil {
		g.Value = ast.Clone(f.Value, identity)
		return
	}
	b := block(via, req, f.Span())
	b.Fields = append(b.Fields, placed(f))
}

// bodyField is the request's `body = ...` field, nil when it has none.
func bodyField(req *ast.Request) *ast.Field {
	if req.Block == nil {
		return nil
	}
	for _, x := range req.Block.Fields {
		if g, ok := x.(*ast.Field); ok && g.Name.Value == "body" && g.Key == nil && g.Block == nil {
			return g
		}
	}
	return nil
}

// setBodyField is `body.a.b = v`: set one field of a body the template wrote as
// an object literal, adding the objects on the way that are missing.
func (e *expander) setBodyField(via int, s *ast.StepDecl, req *ast.Request, set *ast.BodySet) {
	g := bodyField(req)
	var obj *ast.Object
	if g != nil {
		obj, _ = g.Value.(*ast.Object)
	}
	if obj == nil || !e.literalBody(s, nil) {
		e.bag.Error(set.Span(), diag.BadOverride, "body is not an object literal; override it whole with body = ...")
		return
	}
	pos := at(via, set.Span())
	keys := set.Keys()
	for i, k := range keys {
		last := i == len(keys)-1
		j := entry(obj, k)
		if j < 0 {
			var v ast.Expr
			if last {
				v = ast.Clone(set.Value, identity)
			} else {
				v = &ast.Object{LBrace: synth(token.LBrace, "{", "", pos), RBrace: synth(token.RBrace, "}", "", pos)}
			}
			obj.Entries = append(obj.Entries, ast.Entry{
				Key:   &ast.Literal{Tok: synth(token.String, quote(k), k, pos)},
				Colon: synth(token.Colon, ":", "", pos),
				Value: v,
			})
			if last {
				return
			}
			obj = v.(*ast.Object)
			continue
		}
		if last {
			obj.Entries[j].Value = ast.Clone(set.Value, identity)
			return
		}
		next, ok := obj.Entries[j].Value.(*ast.Object)
		if !ok {
			e.bag.Error(set.Span(), diag.BadOverride, "body.%s is not an object", strings.Join(keys[:i+1], ".")).
				Hintf("set body.%s whole, or override the body with body = ...", strings.Join(keys[:i+1], "."))
			return
		}
		obj = next
	}
}

// entry is the index of obj's entry whose key is the string k, or -1.
func entry(obj *ast.Object, k string) int {
	for i, en := range obj.Entries {
		if l, ok := en.Key.(*ast.Literal); ok && l.Kind() == token.String && l.Tok.Value == k {
			return i
		}
	}
	return -1
}

// literalBody reports whether s's body was written as an object literal --
// by the template, or by a `body = {...}` override since. set, when not nil,
// records a new answer first. A body that is an object only because an
// argument or a default made it one is not literal: the template wrote a
// name there, and the override would depend on what was passed.
func (e *expander) literalBody(s *ast.StepDecl, set *bool) bool {
	if set != nil {
		e.literal[s] = *set
	}
	return e.literal[s]
}

// writtenLiteral records whether the template action a, as written before
// any argument was substituted, has an object literal for its body.
func (e *expander) writtenLiteral(s *ast.StepDecl, a ast.Action) {
	req, ok := a.(*ast.Request)
	if !ok {
		return
	}
	if g := bodyField(req); g != nil {
		if _, ok := g.Value.(*ast.Object); ok {
			e.literal[s] = true
		}
	}
}

// block is req's field block, made at the override line when it had none.
func block(via int, req *ast.Request, line token.Span) *ast.Block {
	if req.Block == nil {
		open := at(via, line)
		shut := open
		shut.Line, shut.Col = open.EndLine, open.EndCol
		req.Block = &ast.Block{LBrace: synth(token.LBrace, "{", "", open), RBrace: synth(token.RBrace, "}", "", shut)}
	}
	return req.Block
}

// at is the span of an override line, marked as brought in by use via.
func at(via int, line token.Span) token.Span {
	line.Via = via
	return line
}

// placed is a copy of an override line to put in a block: unstamped, and
// without the comma that separated it in the use block.
func placed(f *ast.Field) *ast.Field {
	g := ast.Clone(f, identity)
	g.Comma = token.Token{}
	return g
}

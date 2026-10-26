package expand

import (
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// override applies a use's override lines to the step its request expanded
// to; via is the use's number in the use table.
//
// Every expression an override places -- a header's value, a body field's,
// an appended expect -- was written at the use and is not stamped. A token
// there was never any source for (a brace, an object key) is placed at the
// override line and carries via, so a diagnostic on it names the use.
func (e *expander) override(via int, s *ast.StepDecl, lines []ast.Stmt) {
	var own []ast.Stmt
	for _, l := range lines {
		switch v := l.(type) {
		case *ast.Field:
			if token.IsUseLine(v.Name.Value) {
				own = append(own, l)
			}
		case *ast.BodySet, *ast.Drop, *ast.Expect:
			own = append(own, l)
		}
	}
	e.apply(via, s, own)
}

// apply applies override lines to one step, in order.
func (e *expander) apply(via int, s *ast.StepDecl, lines []ast.Stmt) {
	seenExpect := false
	for _, l := range lines {
		switch v := l.(type) {
		case *ast.Field:
			if !token.IsUseLine(v.Name.Value) {
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
		e.bag.Error(word.Span, diag.BadOverride, "%s overrides an api step; this is a terminal step", word.Value).
			Hintf("only an api step takes an override; make the value a parameter of the request and pass it as an argument")
	case *ast.Browser:
		e.bag.Error(word.Span, diag.BadOverride, "%s overrides an api step; this is a browser step", word.Value).
			Hintf("only an api step takes an override; make the value a parameter of the request and pass it as an argument")
	case *ast.DB:
		e.bag.Error(word.Span, diag.BadOverride, "%s overrides an api step; this is a db step", word.Value).
			Hintf("only an api step takes an override; make the value a parameter of the request and pass it as an argument")
	}
	return nil
}

// keyed is a header or query override: replace the value of the field with
// the same key -- a header's compared case-insensitively -- or add one.
func (e *expander) keyed(via int, req *ast.Request, f *ast.Field) {
	b := block(req)
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
	b := block(req)
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

// block is req's field block, made at the end of the request when it had
// none: at the override line instead, in a collection declared in the use's
// own file, it would stretch the request's span down to the use.
func block(req *ast.Request) *ast.Block {
	if req.Block == nil {
		end := req.Span()
		open := end
		open.Line, open.Col = end.EndLine, end.EndCol
		shut := open
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

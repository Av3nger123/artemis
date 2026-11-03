package expand

import (
	"path"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// scenarios is a scenario file made plain: one *ast.Scenario for each of its
// data scenarios. Each is the flow's body, cloned and stamped with the Via of
// a use whose span is the data scenario's name, so a diagnostic in the flow
// chains to the scenario that ran it. Each parameter's value is the one the
// scenario sets, or else its default; the scenario's tags are carried; the
// flow's item calls are expanded in the flow file's own terms, against its
// own imports; and then each var the scenario sets to skip is pruned.
//
// The flow is checked once, on its own, before any scenario is made of it,
// as a collection is: its faults are reported once, chained to the header,
// and a broken flow makes no scenario.
func (e *expander) scenarios(root *unit, ref *ast.FlowRef) []ast.Decl {
	if !root.ok {
		return nil
	}
	fu := e.loadFlow(root, ref)
	if fu == nil {
		return nil
	}
	f := e.flowOf(fu, ref)
	if f == nil {
		return nil
	}
	e.res.Uses = append(e.res.Uses, Use{Span: ref.Span(), Ref: f.Name.Value, Item: f.Name.Span})
	sound := e.checkFlow(fu, f, len(e.res.Uses))
	ps := params(f)

	var out []ast.Decl
	for _, d := range root.tree.Scenarios {
		s, ok := d.(*ast.Scenario)
		if !ok {
			continue
		}
		values, skipped := e.sets(s, f, ps)
		if !sound {
			continue
		}
		if sc := e.scenario(fu, f, s, values, skipped); sc != nil {
			out = append(out, sc)
		}
	}
	return out
}

// scenario is one data scenario s made a plain scenario of flow f.
func (e *expander) scenario(fu *unit, f *ast.Flow, s *ast.Scenario, values map[string]ast.Expr, skipped []string) *ast.Scenario {
	e.res.Uses = append(e.res.Uses, Use{Span: s.Name.Span, Ref: f.Name.Value, Item: f.Name.Span})
	stamp := stampVia(len(e.res.Uses))

	var body []ast.Decl
	for _, d := range s.Body {
		if t, ok := d.(*ast.TagsDecl); ok {
			body = append(body, t)
		}
	}
	body = append(body, e.body(fu, flowBody(f, values, stamp))...)

	bad := false
	for _, name := range skipped {
		body = prune(body, name, func(at token.Span, code diag.Code, msg string) {
			bad = true
			r := e.bag.Error(at, code, "%s", msg)
			switch code {
			case diag.SkipRead:
				r.Hintf("scenario %q sets %s = skip; give it a value instead, or read it only in an expect or as an object field's value", s.Name.Value, name)
			case diag.SkipMaybeWritten:
				r.Hintf("write %s on every path above this, or give it a value in scenario %q", name, s.Name.Value)
			}
		})
	}
	if bad {
		// Its fault is reported; the reads left in it would only cascade.
		return nil
	}
	return &ast.Scenario{
		Keyword: s.Keyword,
		Name:    s.Name,
		LBrace:  s.LBrace,
		Body:    body,
		RBrace:  s.RBrace,
		Flow:    f.Name.Value,
	}
}

// flowRef is tree's flow header, the first, or nil when tree is not a
// scenario file.
func flowRef(tree *ast.File) *ast.FlowRef {
	for _, d := range tree.Scenarios {
		if r, ok := d.(*ast.FlowRef); ok {
			return r
		}
	}
	return nil
}

// loadFlow reads, parses and indexes the flow file ref names, relative to the
// scenario file, and the files the flow imports, relative to the flow file.
func (e *expander) loadFlow(root *unit, ref *ast.FlowRef) *unit {
	if e.l == nil {
		e.bag.Error(ref.Path.Span, diag.ImportNeedsFile, "a flow header needs a file on disk").
			Hintf("run the file with artemis run, which reads the flow from disk")
		return nil
	}
	name, src, err := e.l.Load(root.name, ref.Path.Value)
	if err != nil {
		e.bag.Error(ref.Path.Span, diag.FlowNotFound, "cannot read flow %s: %v", ref.Path.Value, err).
			Hintf("the path is relative to this file, %s", base(root.name))
		return nil
	}
	if name == root.name {
		e.bag.Error(ref.Path.Span, diag.NotAFlowFile, "%s is this scenario file, not a flow", ref.Path.Value).
			Hintf("name the flow file the scenarios run: flow \"../flows/<name>.art\"")
		return nil
	}
	if u, ok := e.loaded[name]; ok {
		return u
	}
	return e.loadSource(name, src)
}

// flowOf is the flow fu declares, or nil, reported, when it declares none.
func (e *expander) flowOf(fu *unit, ref *ast.FlowRef) *ast.Flow {
	what := "holds no flow"
	for _, d := range fu.tree.Scenarios {
		switch v := d.(type) {
		case *ast.Flow:
			return v
		case *ast.Collection:
			what = "is a collection file"
		case *ast.FlowRef:
			what = "is a scenario file"
		}
	}
	if !fu.ok {
		return nil // the parser has said what is wrong with it
	}
	e.bag.Error(ref.Path.Span, diag.NotAFlowFile, "%s %s", ref.Path.Value, what).
		Hintf("a scenario file names a flow file, one that holds flow <name> { ... }")
	return nil
}

// params is f's parameters: the vars at its top, above its first setup, step
// or block. Only these can be set by a scenario.
func params(f *ast.Flow) []*ast.VarDecl {
	var out []*ast.VarDecl
	for _, d := range f.Body[:top(f)] {
		if v, ok := d.(*ast.VarDecl); ok {
			out = append(out, v)
		}
	}
	return out
}

// top is the index of f's first setup, step or block: where its parameters
// end.
func top(f *ast.Flow) int {
	for i, d := range f.Body {
		switch d.(type) {
		case *ast.StepDecl, *ast.UseDecl, *ast.PhaseDecl, *ast.BlockDecl:
			return i
		}
	}
	return len(f.Body)
}

// flowShape reports the vars f declares where a scenario cannot set them,
// and a var named tags, and reports whether it found none.
func (e *expander) flowShape(f *ast.Flow) bool {
	ok := true
	for i, d := range f.Body {
		v, isVar := d.(*ast.VarDecl)
		if !isVar {
			continue
		}
		if v.Name.Value == "tags" {
			ok = false
			e.bag.Error(v.Name.Span, diag.ReservedTags, "a flow cannot declare var tags").
				Hintf("tags is each scenario's own list, tags = [...]; rename the var")
		}
		if i > top(f) {
			ok = false
			e.bag.Error(v.Name.Span, diag.VarAfterStep, "var %s is below the flow's first step", v.Name.Value).
				Hintf("move it to the top of the flow, where a scenario can set it, or write it after the action of a step: var %s = ...", v.Name.Value)
		}
	}
	return ok
}

// checkFlow checks f on its own, once: its body with each parameter at its
// default, a required one at null, its item calls expanded, through
// check.Check. Every fault is reported, chained to the header via is the use
// of. It reports whether f is sound; a broken flow is never expanded, so a
// fault in it is not reported again for each scenario.
func (e *expander) checkFlow(fu *unit, f *ast.Flow, via int) bool {
	switch e.flows[f] {
	case sound, checking:
		return true
	case broken:
		return false
	}
	if !fu.ok {
		// The parser has reported it; checking what it recovered would only
		// cascade.
		e.flows[f] = broken
		return false
	}
	e.flows[f] = checking

	bag := e.bag
	e.bag = diag.New()
	file := e.flowCheckTree(fu, f, stampVia(via))
	if file != nil {
		_, cb := check.CheckChained(file, e.res.Chain)
		e.bag.Merge(cb)
	}
	ok := file != nil && !e.bag.HasErrors()
	scratch := e.bag
	e.bag = bag
	e.bag.Merge(scratch)

	e.flows[f] = broken
	if ok {
		e.flows[f] = sound
	}
	return ok
}

// flowFile is the tree the checker reads for a flow file compiled on its own:
// the check a scenario file's flow gets, with the flow's tokens where they are
// written. Its steps are the flow's own nodes, so the checker's Info about
// them is about the file as written, which `artemis ast` encodes. It is nil
// when the file did not parse or a call in the flow did not expand, each of
// which has been reported.
func (e *expander) flowFile(u *unit, f *ast.Flow) *ast.File {
	if !u.ok {
		return nil
	}
	return e.flowCheckTree(u, f, nil)
}

// flowCheckTree reports what is wrong with f's shape and returns its body as
// one scenario for the checker: each parameter at its default, a required one
// at null, its item calls expanded. stamp marks every token copied; nil copies
// none but the parameters, which are given a value. It is nil when an item
// call did not expand, which has been reported: a check of what is left would
// only cascade.
func (e *expander) flowCheckTree(u *unit, f *ast.Flow, stamp func(token.Token) token.Token) *ast.File {
	e.flowShape(f)
	bag, silent := e.bag, e.silent
	e.bag = diag.New()
	body := e.body(u, flowBody(f, nil, stamp))
	expanded := !e.bag.HasErrors() && e.silent == silent
	scratch := e.bag
	e.bag, e.silent = bag, silent
	e.bag.Merge(scratch)
	if !expanded {
		return nil
	}
	return &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
		Keyword: synth(token.Ident, "scenario", "scenario", token.Span{}),
		Name:    synth(token.String, quote(f.Name.Value), f.Name.Value, token.Span{}),
		Body:    body,
		Flow:    f.Name.Value,
	}}}
}

// flowBody is f's body through stamp, with each parameter given the value
// values holds for it, or else its default, or else null. The vars come out
// plain: what a scenario runs has no private var and none without a value.
// With a stamp every declaration is a clone; with none, only the parameters
// are copied, and the rest is f's own.
func flowBody(f *ast.Flow, values map[string]ast.Expr, stamp func(token.Token) token.Token) []ast.Decl {
	n := top(f)
	out := make([]ast.Decl, 0, len(f.Body))
	for i, d := range f.Body {
		if stamp != nil {
			d = ast.Clone(d, stamp)
		}
		if v, ok := d.(*ast.VarDecl); ok && i < n {
			cp := *v
			v = &cp
			if v.Private.Text != "" {
				v.Keyword.Leading = append(append([]token.Trivia(nil), v.Private.Leading...), v.Keyword.Leading...)
				v.Private = token.Token{}
			}
			if x, set := values[v.Name.Value]; set {
				v.Value = x
			}
			if isNil(v.Value) {
				v.Value = &ast.Literal{Tok: synth(token.Null, "null", "", v.Name.Span)}
			}
			if v.Assign.Text == "" {
				v.Assign = synth(token.Assign, "=", "", v.Name.Span)
			}
			d = v
		}
		out = append(out, d)
	}
	return out
}

// sets reads data scenario s's lines against flow f's parameters ps: the value
// each one sets, and the names it sets to skip, in order. A line that sets
// nothing a scenario can set is reported and left out, so the parameter
// keeps its default; a required parameter s does not set is reported.
func (e *expander) sets(s *ast.Scenario, f *ast.Flow, ps []*ast.VarDecl) (map[string]ast.Expr, []string) {
	byName := map[string]*ast.VarDecl{}
	var public []string
	for _, p := range ps {
		byName[p.Name.Value] = p
		if p.Private.Text == "" {
			public = append(public, p.Name.Value)
		}
	}
	values := map[string]ast.Expr{}
	var skipped []string
	seen := map[string]bool{}
	for _, d := range s.Body {
		set, ok := d.(*ast.Set)
		if !ok {
			continue
		}
		name := set.Name.Value
		p := byName[name]
		switch {
		case p == nil:
			r := e.bag.Error(set.Name.Span, diag.UnknownParam, "flow %s has no var %s a scenario can set", f.Name.Value, name)
			if !r.DidYouMean(name, public) {
				if len(public) == 0 {
					r.Hintf("flow %s has no var at its top, where a scenario's values go", f.Name.Value)
				} else {
					r.Hintf("flow %s's vars are %s", f.Name.Value, strings.Join(public, ", "))
				}
			}
			continue
		case p.Private.Text != "":
			e.bag.Error(set.Name.Span, diag.PrivateParam, "%s is private to flow %s", name, f.Name.Value).
				Hintf("remove it; a private var takes only its own default")
			continue
		case seen[name]:
			e.bag.Error(set.Name.Span, diag.DuplicateArgument, "%s is set twice", name)
			continue
		}
		seen[name] = true
		if set.IsSkip() {
			skipped = append(skipped, name)
			continue
		}
		if e.readsName(set.Value) {
			continue
		}
		values[name] = ast.Clone(set.Value, identity)
	}
	for _, p := range ps {
		if p.Required() && p.Private.Text == "" && !seen[p.Name.Value] {
			e.bag.Error(s.Name.Span, diag.MissingParam, "scenario %s does not set %s, which flow %s requires", s.Name.Text, p.Name.Value, f.Name.Value).
				Hintf("set it: %s = ...", p.Name.Value)
		}
	}
	return values, skipped
}

// readsName reports each name a scenario's value reads, and whether there
// was one. A value is a literal, an array or object of them, or a builtin:
// what it would read is a flow var, which has no value yet where a
// scenario's values are given.
func (e *expander) readsName(x ast.Expr) bool {
	found := false
	reads(x, func(id *ast.Ident) {
		found = true
		e.bag.Error(id.Tok.Span, diag.ScenarioValueReadsName, "a scenario's value cannot read %s", id.Tok.Value).
			Hintf("write the value out: a scenario's values are literals and builtins such as env(\"X\"), and read no name")
	})
	return found
}

// reads calls f with each identifier n reads as a value: every *ast.Ident
// that is not a call's callee or an object's key. A member's name is a token,
// not an Ident, so it is never one.
func reads(n ast.Node, f func(*ast.Ident)) {
	if isNil(n) {
		return
	}
	skip := map[*ast.Ident]bool{}
	ast.Inspect(n, func(x ast.Node) {
		switch v := x.(type) {
		case *ast.Call:
			skip[v.Callee] = true
		case *ast.Object:
			for _, en := range v.Entries {
				if id, ok := en.Key.(*ast.Ident); ok {
					skip[id] = true
				}
			}
		case *ast.Ident:
			if !skip[v] {
				f(v)
			}
		}
	})
}

// base is name's last element, as a person reading a message wants it.
func base(name string) string { return path.Base(filepath.ToSlash(name)) }

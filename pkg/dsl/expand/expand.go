// Package expand turns a file that imports collections and uses their
// requests and flows into a plain file: no import, no collection and no use,
// only scenarios whose steps are written out. Everything after it -- the
// checker, lowering, the runner -- sees a file it already understands.
//
// A copied token keeps the span it had where it was written, in the
// collection file, and is stamped with Via: one plus the index of the use
// that copied it in Result.Uses. Result.Chain walks that table to say which
// use lines brought a span in.
package expand

import (
	"slices"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// Use is one expanded use line.
type Use struct {
	Span   token.Span // the use line, in the file it was written in
	Parent int        // 1 + index of the enclosing use, 0 at the top
	Ref    string     // "orders.create": qualified, even for a bare sibling ref, once resolved
	Item   token.Span // the name of the request or flow it expanded; zero when it resolved to nothing
}

// Result is an expansion.
type Result struct {
	File    *ast.File         // only scenarios, vars, configs and steps
	Uses    []Use             // what Span.Via indexes (Via-1)
	Sources map[string]string // every file read, by name, for rendering diagnostics
}

// Chain is the use lines that brought a span in, innermost first. A span
// written in place has none.
func (r *Result) Chain(s token.Span) []token.Span {
	var out []token.Span
	for i := s.Via; i > 0 && i <= len(r.Uses); i = r.Uses[i-1].Parent {
		out = append(out, r.Uses[i-1].Span)
	}
	return out
}

// expander holds one expansion: the files read, the collections they
// declare, and the use table being built.
type expander struct {
	l       Loader
	bag     *diag.Bag
	res     *Result
	loaded  map[string]*unit // by canonical name
	loading []string         // the import stack, for cycle messages
	owner   map[*ast.Collection]*unit
	literal map[*ast.StepDecl]bool // an expanded step whose body was written as an object literal

	standing map[*ast.Collection]standing // each collection's standalone check
	silent   int                          // uses refused without a diagnostic of their own
}

// unit is one parsed file and the collections it can see by name.
type unit struct {
	name  string
	tree  *ast.File
	deps  []*unit                    // the units this file imports directly
	own   map[string]*ast.Collection // collections declared in this file
	scope map[string]*ast.Collection // own plus every imported one
	ok    bool                       // parsed without error
	lost  bool                       // an import failed, so a collection may be missing from scope
}

// Expand resolves tree's imports through l and replaces every use in its
// scenarios with the steps it stands for. l may be nil, for a file that was
// never on disk; an import is then an error.
//
// A file with no import, no collection and no use is returned as is -- the
// same pointer -- so a caller that never meets the feature sees no change.
func Expand(tree *ast.File, l Loader) (*Result, *diag.Bag) {
	bag := diag.New()
	res := &Result{File: tree, Sources: map[string]string{}}
	if !needsExpansion(tree) {
		return res, bag
	}
	name := fileName(tree)
	e := &expander{
		l:       l,
		bag:     bag,
		res:     res,
		loaded:  map[string]*unit{},
		loading: []string{name},
		owner:   map[*ast.Collection]*unit{},
		literal: map[*ast.StepDecl]bool{},

		standing: map[*ast.Collection]standing{},
	}
	root := &unit{name: name, tree: tree, ok: true}
	e.imports(root)
	e.index(root)

	out := &ast.File{EOF: tree.EOF}
	for _, d := range tree.Scenarios {
		switch v := d.(type) {
		case *ast.Import, *ast.Collection:
		case *ast.Scenario:
			cp := *v
			cp.Body = e.body(root, v.Body)
			out.Scenarios = append(out.Scenarios, &cp)
		default:
			out.Scenarios = append(out.Scenarios, d)
		}
	}
	res.File = out

	// A diagnostic on a token a use copied in -- an override error on a
	// synthesised token, a bind error inside a nested use -- names the use
	// lines that brought it in, like the checker's do in pkg/dsl/front.
	chained := diag.New()
	for _, d := range bag.All() {
		d.UsedFrom = res.Chain(d.Span)
		chained.Add(d)
	}
	return res, chained
}

// needsExpansion reports whether tree holds anything Expand rewrites.
func needsExpansion(tree *ast.File) bool {
	for _, d := range tree.Scenarios {
		switch v := d.(type) {
		case *ast.Import, *ast.Collection:
			return true
		case *ast.Scenario:
			for _, b := range v.Body {
				if _, ok := b.(*ast.UseDecl); ok {
					return true
				}
			}
		}
	}
	return false
}

// fileName is the name tree was parsed under.
func fileName(tree *ast.File) string {
	for _, t := range tree.Tokens(nil) {
		if t.Span.File != "" {
			return t.Span.File
		}
	}
	if tree.EOF.Span.File != "" {
		return tree.EOF.Span.File
	}
	return "main.art"
}

// body is a scenario body with each use replaced by its steps. The secret
// vars a use hoisted -- its nested uses' included -- sit directly above its
// steps, in use order: there they see the vars the use's arguments see, and
// the checker binds them for the steps that read them.
func (e *expander) body(u *unit, decls []ast.Decl) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	sc := newScope(decls)
	for _, d := range decls {
		if use, ok := d.(*ast.UseDecl); ok {
			steps := e.expandUse(u, nil, use, 0, nil, sc.at(nil, out))
			out = append(out, *sc.hoisted...)
			*sc.hoisted = (*sc.hoisted)[:0]
			out = append(out, steps...)
			continue
		}
		out = append(out, d)
	}
	return out
}

// imports loads every file u imports.
func (e *expander) imports(u *unit) {
	for _, d := range u.tree.Scenarios {
		imp, ok := d.(*ast.Import)
		if !ok {
			continue
		}
		if e.l == nil {
			u.lost = true
			e.bag.Error(imp.Span(), diag.ImportNeedsFile, "import needs a file on disk").
				Hintf("run the file with artemis run, which reads imports from disk")
			continue
		}
		if dep := e.load(u.name, imp); dep != nil {
			u.deps = append(u.deps, dep)
		} else {
			u.lost = true
		}
	}
}

// load reads, parses and indexes the file imp names, and the files it
// imports in turn. A file imported twice is read once.
func (e *expander) load(from string, imp *ast.Import) *unit {
	name, src, err := e.l.Load(from, imp.Path.Value)
	if err != nil {
		e.bag.Error(imp.Path.Span, diag.ImportNotFound, "cannot read %s: %v", imp.Path.Value, err)
		return nil
	}
	if i := slices.Index(e.loading, name); i >= 0 {
		chain := append(slices.Clone(e.loading[i:]), name)
		e.bag.Error(imp.Path.Span, diag.ImportCycle, "import cycle: %s", strings.Join(chain, " -> "))
		return nil
	}
	if u, ok := e.loaded[name]; ok {
		return u
	}
	tree, pb := parser.Parse(name, src)
	e.bag.Merge(pb)
	e.res.Sources[name] = src
	u := &unit{name: name, tree: tree, ok: !pb.HasErrors()}
	e.loaded[name] = u

	e.loading = append(e.loading, name)
	e.imports(u)
	e.loading = e.loading[:len(e.loading)-1]
	e.index(u)
	return u
}

// index fills u's own collections and its scope: its own plus those of every
// file it imports directly. Imports are not transitive. Then it checks each of
// u's own collections on its own; an imported one was checked when its file
// was indexed.
func (e *expander) index(u *unit) {
	u.own = map[string]*ast.Collection{}
	u.scope = map[string]*ast.Collection{}
	add := func(c *ast.Collection) bool {
		name := c.Name.Value
		prev, dup := u.scope[name]
		if prev == c {
			return false
		}
		if dup {
			e.bag.Error(c.Name.Span, diag.DuplicateCollection, "collection %q is declared twice", name).
				Hintf("the first is at %s:%d", prev.Name.Span.File, prev.Name.Span.Line)
			return false
		}
		u.scope[name] = c
		return true
	}
	for _, d := range u.tree.Scenarios {
		if c, ok := d.(*ast.Collection); ok && add(c) {
			u.own[c.Name.Value] = c
			e.owner[c] = u
		}
	}
	for _, dep := range u.deps {
		for _, c := range sortedColls(dep) {
			add(c)
		}
	}
	for _, c := range sortedColls(u) {
		e.checkCollection(u, c)
	}
}

// sortedColls is u's own collections in source order, so diagnostics come
// out the same on every run.
func sortedColls(u *unit) []*ast.Collection {
	out := make([]*ast.Collection, 0, len(u.own))
	for _, c := range u.own {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name.Span.Offset < out[j].Name.Span.Offset })
	return out
}

// resolve finds the collection and item a use names. c is the collection the
// use is written in, nil in a scenario; a bare ref is legal only there.
func (e *expander) resolve(u *unit, c *ast.Collection, use *ast.UseDecl) (owner *unit, coll *ast.Collection, item ast.Decl, ok bool) {
	coll = c
	if use.Collection.Text != "" {
		name := use.Collection.Value
		coll = u.scope[name]
		if coll == nil {
			if u.lost {
				// The failed import has been reported, and is most likely
				// where the collection was.
				e.silent++
				return nil, nil, nil, false
			}
			r := e.bag.Error(use.Collection.Span, diag.UnknownCollection, "no collection %q is in scope here", name)
			if !r.DidYouMean(name, keys(u.scope)) {
				r.Hintf("import the file that declares it")
			}
			return nil, nil, nil, false
		}
	} else if c == nil {
		e.bag.Error(use.Item.Span, diag.UnknownItem,
			"a bare name is only legal inside a collection; write <collection>.%s", use.Item.Value)
		return nil, nil, nil, false
	}
	owner = e.owner[coll]
	if owner == nil || !owner.ok || !e.checkCollection(owner, coll) {
		// Its own errors have been reported once, where it is written.
		e.silent++
		return nil, nil, nil, false
	}
	var names []string
	for _, d := range coll.Items {
		n := itemName(d)
		if n == "" {
			continue
		}
		if n == use.Item.Value {
			return owner, coll, d, true
		}
		names = append(names, n)
	}
	r := e.bag.Error(use.Item.Span, diag.UnknownItem, "collection %q has no request or flow %q", coll.Name.Value, use.Item.Value)
	if !r.DidYouMean(use.Item.Value, names) && len(names) > 0 {
		r.Hintf("%s has %s", coll.Name.Value, strings.Join(names, ", "))
	}
	return nil, nil, nil, false
}

// itemName is a request's or a flow's name, "" for anything else.
func itemName(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.RequestDecl:
		return v.Name.Value
	case *ast.FlowDecl:
		return v.Name.Value
	}
	return ""
}

// itemSpan is the span of d's name, where a reader goes to find it.
func itemSpan(d ast.Decl) token.Span {
	switch v := d.(type) {
	case *ast.RequestDecl:
		return v.Name.Span
	case *ast.FlowDecl:
		return v.Name.Span
	}
	return token.Span{}
}

func keys(m map[string]*ast.Collection) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// expandUse returns the steps one use stands for. u is the file the use is
// written in, coll the collection around it (nil in a scenario), parent the
// Via of the use that copied this one in (0 at the top), and stack the items
// being expanded around it, for cycle detection. sc is what the use's
// arguments can read, for its secret parameters.
//
// Overrides apply before `as`, so an in block names a step as the flow wrote
// it; `as` then renames the steps and captures.
func (e *expander) expandUse(u *unit, coll *ast.Collection, use *ast.UseDecl, parent int, stack []string, sc *scope) []ast.Decl {
	e.res.Uses = append(e.res.Uses, Use{Span: use.Span(), Parent: parent, Ref: use.Ref()})
	via := len(e.res.Uses)

	owner, c, item, ok := e.resolve(u, coll, use)
	if !ok {
		return nil
	}
	e.res.Uses[via-1].Item = itemSpan(item)
	key := c.Name.Value + "." + itemName(item)
	// Qualified even for a bare sibling ref, as the step it names is.
	e.res.Uses[via-1].Ref = key
	if i := slices.Index(stack, key); i >= 0 {
		chain := append(slices.Clone(stack[i:]), key)
		e.bag.Error(use.Span(), diag.UseCycle, "use cycle: %s", strings.Join(chain, " -> "))
		return nil
	}
	stack = append(slices.Clone(stack), key)
	stamp := stampVia(via)

	switch it := item.(type) {
	case *ast.RequestDecl:
		env, ok := e.bind(use, it, it.Params, sc, varPrefix(sc, use, it.Name.Value), key, stamp)
		if !ok {
			return nil
		}
		s := requestStep(it, prefix(use, c, it.Name.Value), env, stamp)
		e.writtenLiteral(s, it.Action)
		e.override(use, via, []*ast.StepDecl{s}, use.Lines, false, nil)
		if use.Alias.Text != "" {
			e.renameCaptures([]*ast.StepDecl{s}, use.Alias.Value, via)
		}
		return []ast.Decl{s}
	case *ast.FlowDecl:
		env, ok := e.bind(use, it, it.Params, sc, varPrefix(sc, use, it.Name.Value), key, stamp)
		if !ok {
			return nil
		}
		pre := prefix(use, c, it.Name.Value)
		var out []ast.Decl
		var steps []*ast.StepDecl
		named := map[*ast.StepDecl]string{} // each step's name before the prefix
		own := map[string]*ast.StepDecl{}
		for _, d := range it.Body {
			switch v := d.(type) {
			case *ast.StepDecl:
				s := ast.Clone(v, stamp)
				e.writtenLiteral(s, v.Action)
				substStep(s, env)
				if _, dup := own[v.Name.Value]; !dup {
					own[v.Name.Value] = s
				}
				named[s] = v.Name.Value
				out = append(out, s)
				steps = append(steps, s)
			case *ast.UseDecl:
				inner := ast.Clone(v, stamp)
				substLines(inner.Lines, env)
				for _, x := range e.expandUse(owner, c, inner, via, stack, sc.inside(use).at(sc.secrets, out)) {
					if s, ok := x.(*ast.StepDecl); ok {
						named[s] = s.Name.Value
						steps = append(steps, s)
					}
					out = append(out, x)
				}
			}
		}
		e.override(use, via, nil, use.Lines, true, own)
		for _, s := range steps {
			rename(s, stepName(pre, named[s]))
		}
		if use.Alias.Text != "" {
			e.renameCaptures(steps, use.Alias.Value, via)
		}
		return out
	}
	return nil
}

// requestStep is r written out as a step called name, with its parameters
// replaced by their values at this use.
func requestStep(r *ast.RequestDecl, name string, env map[string]ast.Expr, stamp func(token.Token) token.Token) *ast.StepDecl {
	cl := ast.Clone(r, stamp)
	s := &ast.StepDecl{
		Keyword: synth(token.Ident, "step", "step", cl.Keyword.Span),
		Name:    synth(token.String, quote(name), name, cl.Name.Span),
		LBrace:  cl.LBrace,
		Action:  cl.Action,
		Body:    cl.Body,
		RBrace:  cl.RBrace,
	}
	substStep(s, env)
	return s
}

// synth is a token that was never written, placed at at so a diagnostic on it
// still points somewhere real.
func synth(kind token.Kind, text, value string, at token.Span) token.Token {
	return token.Token{Kind: kind, Text: text, Value: value, Span: at}
}

// quote is name as a string literal's source.
func quote(name string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + r.Replace(name) + `"`
}

// stampVia marks a copied token as brought in by use number via. A token
// that already carries a Via -- copied in by a nested use expanded first --
// keeps it: it is more specific, and Use.Parent links it to this use.
func stampVia(via int) func(token.Token) token.Token {
	return func(t token.Token) token.Token {
		if !t.Span.IsZero() && t.Span.Via == 0 {
			t.Span.Via = via
		}
		return t
	}
}

func identity(t token.Token) token.Token { return t }

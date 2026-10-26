// Package expand turns a file that imports collections and uses their
// requests into a plain file: no import, no collection and no use,
// only scenarios whose steps are written out. Everything after it -- the
// checker, lowering, the runner -- sees a file it already understands.
//
// A copied token keeps the span it had where it was written, in the
// collection file, and is stamped with Via: one plus the index of the use
// that copied it in Result.Uses. Result.Chain says which use line brought a
// span in.
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
	Span token.Span // the use line, in the scenario file
	Ref  string     // "orders.create"
	Item token.Span // the name of the request it expanded; zero when it resolved to nothing
}

// Result is an expansion.
type Result struct {
	File    *ast.File         // only scenarios, vars, configs and steps
	Uses    []Use             // what Span.Via indexes (Via-1)
	Sources map[string]string // every file read, by name, for rendering diagnostics
}

// Chain is the use line that brought a span in. A span written in place has
// none. A use is only ever written in a scenario, so there is at most one.
func (r *Result) Chain(s token.Span) []token.Span {
	if s.Via <= 0 || s.Via > len(r.Uses) {
		return nil
	}
	return []token.Span{r.Uses[s.Via-1].Span}
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
	deps  []dep                      // the files this file imports directly
	own   []*ast.Collection          // collections declared in this file, in source order
	scope map[string]*ast.Collection // the collection each import names, by that name
	ok    bool                       // parsed without error
	lost  bool                       // an import failed, so a collection may be missing from scope
}

// dep is one import and the file it loaded.
type dep struct {
	imp  *ast.Import
	unit *unit
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
			if cp.For != nil {
				// After the uses, so each row is a copy of the scenario as
				// it runs; a scenario whose rows could not be made is
				// dropped, its fault already reported.
				out.Scenarios = append(out.Scenarios, e.rows(&cp)...)
				continue
			}
			out.Scenarios = append(out.Scenarios, &cp)
		default:
			out.Scenarios = append(out.Scenarios, d)
		}
	}
	res.File = out

	// A diagnostic on a token a use copied in -- an override error on a
	// synthesised token -- names the use
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
			if v.For != nil {
				return true
			}
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
// vars a use hoisted sit directly above its
// steps, in use order: there they see the vars the use's arguments see, and
// the checker binds them for the steps that read them.
func (e *expander) body(u *unit, decls []ast.Decl) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	sc := newScope(decls)
	for _, d := range decls {
		if use, ok := d.(*ast.UseDecl); ok {
			steps := e.expandUse(u, use, sc.at(nil, out))
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
		if loaded := e.load(u.name, imp); loaded != nil {
			u.deps = append(u.deps, dep{imp: imp, unit: loaded})
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

// index checks u's shape (shape.go), then fills u's own collections and its
// scope: the collection of each file it imports directly, by the import's
// `as` or else by the file's name (parser.ImportName). A collection's own
// name is a title, and no use reads it. Imports are not transitive. Then it
// checks each of u's own collections on its own; an imported one was checked
// when its file was indexed.
func (e *expander) index(u *unit) {
	e.checkShape(u)
	for _, d := range u.tree.Scenarios {
		if c, ok := d.(*ast.Collection); ok {
			u.own = append(u.own, c)
			e.owner[c] = u
		}
	}
	u.scope = map[string]*ast.Collection{}
	named := map[string]*ast.Import{}
	for _, d := range u.deps {
		name, at := d.imp.Name.Value, d.imp.Name.Span
		if d.imp.Name.Text == "" {
			var ok bool
			name, ok = parser.ImportName(d.imp.Path.Value)
			at = d.imp.Path.Span
			if !ok {
				u.lost = true
				e.bag.Error(at, diag.ImportNeedsName, "a use reads an import by its file's name, and %q cannot be one", name).
					Hintf("name it: import %s as %s", d.imp.Path.Text, parser.IdentLike(name))
				continue
			}
		}
		if len(d.unit.own) == 0 {
			u.lost = true
			// A file that did not parse has said why already.
			if d.unit.ok {
				e.bag.Error(d.imp.Path.Span, diag.ImportNoCollection, "%s holds no collection to use", d.imp.Path.Value).
					Hintf("import a collection file: a scenario file is run, never imported")
			}
			continue
		}
		if prev, dup := named[name]; dup {
			e.bag.Error(at, diag.DuplicateImportName, "two imports are named %s", name).
				Hintf("the first is on line %d; name one of them another way with as", prev.Span().Line)
			continue
		}
		named[name] = d.imp
		u.scope[name] = d.unit.own[0]
	}
	for _, c := range u.own {
		e.checkCollection(u, c)
	}
}

// resolve finds the collection and request a use names.
func (e *expander) resolve(u *unit, use *ast.UseDecl) (coll *ast.Collection, item ast.Decl, ok bool) {
	if use.Collection.Text != "" {
		name := use.Collection.Value
		coll = u.scope[name]
		if coll == nil {
			if u.lost {
				// The failed import has been reported, and is most likely
				// where the collection was.
				e.silent++
				return nil, nil, false
			}
			r := e.bag.Error(use.Collection.Span, diag.UnknownCollection, "no collection %q is in scope here", name)
			if !r.DidYouMean(name, keys(u.scope)) {
				r.Hintf("import the file that declares it")
			}
			return nil, nil, false
		}
	} else {
		e.bag.Error(use.Item.Span, diag.UnknownItem,
			"a use names a collection's request: write <collection>.%s", use.Item.Value)
		return nil, nil, false
	}
	owner := e.owner[coll]
	if owner == nil || !owner.ok || !e.checkCollection(owner, coll) {
		// Its own errors have been reported once, where it is written.
		e.silent++
		return nil, nil, false
	}
	var names []string
	for _, d := range coll.Items {
		n := itemName(d)
		if n == "" {
			continue
		}
		if n == use.Item.Value {
			return coll, d, true
		}
		names = append(names, n)
	}
	r := e.bag.Error(use.Item.Span, diag.UnknownItem, "%s has no request %q", use.Collection.Value, use.Item.Value)
	if !r.DidYouMean(use.Item.Value, names) && len(names) > 0 {
		r.Hintf("%s has %s", use.Collection.Value, strings.Join(names, ", "))
	}
	return nil, nil, false
}

// itemName is a request's name, "" for anything else.
func itemName(d ast.Decl) string {
	if r, ok := d.(*ast.RequestDecl); ok {
		return r.Name.Value
	}
	return ""
}

// itemSpan is the span of d's name, where a reader goes to find it.
func itemSpan(d ast.Decl) token.Span {
	if r, ok := d.(*ast.RequestDecl); ok {
		return r.Name.Span
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

// expandUse returns the step one use stands for: its request written out,
// its parameters bound to the use's arguments, then its override lines
// applied, then `as` renaming the step's captures. u is the scenario's file,
// and sc what the use's arguments can read, for secret parameters.
func (e *expander) expandUse(u *unit, use *ast.UseDecl, sc *scope) []ast.Decl {
	e.res.Uses = append(e.res.Uses, Use{Span: use.Span(), Ref: use.Ref()})
	via := len(e.res.Uses)

	_, item, ok := e.resolve(u, use)
	if !ok {
		return nil
	}
	e.res.Uses[via-1].Item = itemSpan(item)
	// By the name the use reads the collection by, never its title.
	key := use.Collection.Value + "." + itemName(item)
	e.res.Uses[via-1].Ref = key
	stamp := stampVia(via)

	switch it := item.(type) {
	case *ast.RequestDecl:
		env, ok := e.bind(use, it, it.Params, sc, varPrefix(use, it.Name.Value), key, stamp)
		if !ok {
			return nil
		}
		s := requestStep(it, prefix(use, it.Name.Value), env, stamp)
		e.writtenLiteral(s, it.Action)
		e.override(via, s, use.Lines)
		if use.Alias.Text != "" {
			e.renameCaptures([]*ast.StepDecl{s}, use.Alias.Value, via)
		}
		return []ast.Decl{s}
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

// quote is name as a string literal's source: the escapes the lexer reads
// back, so a row's text -- which may hold a quote, a dollar or a newline --
// round-trips through `artemis expand`.
func quote(name string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(name) + `"`
}

// stampVia marks a copied token as brought in by use number via.
func stampVia(via int) func(token.Token) token.Token {
	return func(t token.Token) token.Token {
		if !t.Span.IsZero() && t.Span.Via == 0 {
			t.Span.Via = via
		}
		return t
	}
}

func identity(t token.Token) token.Token { return t }

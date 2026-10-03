package check

import (
	"strings"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// The roots each step type binds. This is the table the issue is about, and it
// is the only word list in this package that is not already in
// pkg/dsl/token -- because which names a *step type* binds is an ART-33
// concept and nothing lexical.
//
// `page` is a single root with a closed member set rather than two roots
// spelled `page.url` and `page.title`, because `page` is what an expression
// resolves and `url` is a member of it. memberRoots below holds what it may
// hold.
var roots = map[StepType][]string{
	API:      {"status", "body", "raw", "headers"},
	Terminal: {"exit_code", "stdout", "stderr"},
	Browser:  {"page"},
}

// memberRoots are the roots whose members are a closed set, so a typo in one
// is catchable. `body.data.count` is not here and never will be: a response's
// shape is a run-time fact, and checking it would mean rejecting correct
// files.
var memberRoots = map[string][]string{
	"page": {"url", "title"},
}

// elementFns are the browser-scope element functions. They are callables, not
// roots: `text` alone is not an expression, and `text(sel)` is legal only in a
// browser step.
var elementFns = []string{"text", "value", "attr", "count", "visible"}

// param is one parameter of a builtin: what it wants, in the words a
// diagnostic uses, and which literal kinds satisfy it.
//
// It exists because `match`'s second argument is a regex and every other
// builtin's argument is a string, so "every parameter is a string" stopped
// being true. Only *literals* are judged -- an identifier or an
// interpolation could hold anything and this stage does not evaluate -- so a
// param is a sentence plus a small set of token kinds.
type param struct {
	// want is the phrase after "must be": "a string", "a regular
	// expression". The existing builtins' wording is unchanged by this
	// table, which is what keeps the invalid-file corpus's goldens intact.
	want string

	// kinds are the literal kinds that satisfy it. A literal of any other
	// kind is reported; a non-literal is left to run time.
	kinds []token.Kind
}

// aString is the parameter every builtin but `match`'s pattern takes: a
// variable name, a selector, an attribute name, the text to search.
var aString = param{want: "a string", kinds: []token.Kind{token.String}}

// aPattern is `match`'s second argument. A string is allowed as well as a
// regex literal, which is the rule `matches` already follows, so a pattern
// can live in a var.
var aPattern = param{
	want:  "a regular expression",
	kinds: []token.Kind{token.Regex, token.String},
}

// params is what each builtin takes, in order. env() and match() are
// everywhere; the rest are browser-only and rejected elsewhere by scope, not
// by this table. Its length is the builtin's arity, so the two cannot
// disagree.
var params = map[string][]param{
	"env":     {aString},
	"match":   {aString, aPattern},
	"text":    {aString},
	"value":   {aString},
	"attr":    {aString, aString},
	"count":   {aString},
	"visible": {aString},
}

// arity is how many arguments each builtin takes, derived from params so that
// adding a builtin is one line rather than two that can drift.
var arity = func() map[string]int {
	m := make(map[string]int, len(params))
	for name, ps := range params {
		m[name] = len(ps)
	}
	return m
}()

// freeFns are the builtins callable in every scope, as against the browser
// element functions. env() reads the process environment and match() reads a
// string, and neither needs a page or a response to be meaningful.
var freeFns = []string{"env", "match"}

// scopeHints is the second line of a not-in-scope diagnostic: the roots that
// *are* available, which is the whole reason the fault has its own code. The
// browser line is broken by hand because the renderer never wraps -- where a
// hint breaks is the producer's decision -- and this is the break the design
// document prints.
var scopeHints = map[StepType]string{
	API:      "an api step binds status, body, raw, headers",
	Terminal: "a terminal step binds exit_code, stdout, stderr",
	Browser: "a browser step binds page.url, page.title, text(), value(),\n" +
		"attr(), count(), visible()",
	Unknown: `a var's value may use env("NAME") and the vars above it`,
}

// scopeWhere is the position a not-in-scope message names, for the one
// position that is not a step. `page.url` in a var's value is as much a scope
// violation as `page.url` in a terminal step, but "in a unknown step" is not a
// place an author can look for: the type is Unknown because a var's value has
// no step at all.
//
// It is here and not on StepType because StepType.String() is also the wire
// name -- `artemis ast`'s `type` on every step, `artemis grammar --json`'s
// stepType choice point -- where "unknown" is right and "a var's value" would
// be a lie.
var scopeWhere = map[StepType]string{
	Unknown: "a var's value",
}

// where is the position this view is of, in the words a not-in-scope message
// uses: "an api step", "a browser step", "a var's value". It is the message
// line's half of the sentence whose second half is hint().
func (v *view) where() string {
	if w, ok := scopeWhere[v.typ]; ok {
		return w
	}
	return v.typ.article() + " " + v.typ.String() + " step"
}

// Roots returns the identifier roots a step of type t binds, for
// `artemis grammar --json` and a UI's path picker. The slice is a copy.
func Roots(t StepType) []string {
	out := make([]string, len(roots[t]))
	copy(out, roots[t])
	return out
}

// Functions returns the builtins callable in a step of type t: env() and
// match() always, plus the element functions in a browser step.
func Functions(t StepType) []string {
	out := append([]string{}, freeFns...)
	if t == Browser {
		out = append(out, elementFns...)
	}
	return out
}

// Members returns the closed member set of a root, and whether it has one. A
// root with no entry here -- `body`, `headers`, `stdout` -- accepts any
// member, because its shape is only known at run time.
func Members(root string) ([]string, bool) {
	m, ok := memberRoots[root]
	if !ok {
		return nil, false
	}
	out := make([]string, len(m))
	copy(out, m)
	return out, true
}

// allRoots is every root of every step type, used to tell a name that is in
// the language but not here (not-in-scope) from a name that is nowhere
// (unknown-identifier).
var allRoots = func() map[string]StepType {
	m := map[string]StepType{}
	for t, rs := range roots {
		for _, r := range rs {
			m[r] = t
		}
	}
	return m
}()

// elementFnOf reports whether word is a browser element function.
var elementFnOf = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range elementFns {
		m[f] = true
	}
	return m
}()

// freeFnOf reports whether word is a builtin callable in every scope.
var freeFnOf = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range freeFns {
		m[f] = true
	}
	return m
}()

// scope accumulates what a scenario has declared so far: its vars, and the
// captures of the steps already walked.
//
// Both are "what has been seen", which is the whole mechanism behind the two
// ordering rules -- a var sees the vars above it, and a capture is in scope
// for the steps *after* the one that writes it -- and it only works because
// the walk is in source order.
type scope struct {
	vars     []string
	captures []string
}

func newScope() *scope { return &scope{} }

func (s *scope) addVar(name string)     { s.vars = append(s.vars, name) }
func (s *scope) addCapture(name string) { s.captures = append(s.captures, name) }

// view is the scope as it looks from one position: a step of a given type, or
// a var declaration (type Unknown, no roots).
//
// It is a value rather than a pointer into the scope because a step's view is
// fixed for the whole step: a capture written halfway down must not become
// visible to the lines below it.
type view struct {
	typ      StepType
	roots    []string
	vars     []string
	captures []string
}

// view builds the resolution view for a position of type t. Unknown means a
// var's value, which binds no roots at all -- only env() and the vars above.
func (s *scope) view(t StepType) *view {
	v := &view{typ: t, roots: roots[t]}
	v.vars = append(v.vars, s.vars...)
	v.captures = append(v.captures, s.captures...)
	return v
}

// names is every resolvable name, in candidate order: the step's own roots
// first, then the scenario's vars, then the earlier captures.
//
// The order is not cosmetic. diag.Nearest documents that the first of an
// equal-distance tie wins, so putting the roots first is what makes `statu`
// resolve to `status` rather than to a var that happens to be one edit away --
// and it is also the order a UI's path picker should list them in.
func (v *view) names() []string {
	out := make([]string, 0, len(v.roots)+len(v.vars)+len(v.captures))
	out = append(out, v.roots...)
	out = append(out, v.vars...)
	return append(out, v.captures...)
}

// has reports whether name resolves in this view.
func (v *view) has(name string) bool {
	return contains(v.roots, name) || contains(v.vars, name) || contains(v.captures, name)
}

func contains(words []string, name string) bool {
	for _, w := range words {
		if w == name {
			return true
		}
	}
	return false
}

// isRoot reports whether name is one of this position's own roots, which is
// what decides unknown-field from unknown-identifier.
func (v *view) isRoot(name string) bool { return contains(v.roots, name) }

// hint is the "what is available here" line.
func (v *view) hint() string { return scopeHints[v.typ] }

// notFound reports an unresolvable name at span, choosing between the three
// codes a name can fail with.
//
// The order is the order of usefulness. A name bound by *another* step type is
// not-in-scope, because the useful thing to say is which roots this type does
// bind -- that is what tells the author to write `page.title` instead of
// `status`. A name bound nowhere gets a did-you-mean, and the code depends on
// what it nearly was: nearly one of this step's own roots means the author
// misspelled a field of the step's result (unknown-field, the design's
// `statu` example); anything else means they named something that does not
// exist (unknown-identifier).
func (c *checker) notFound(span token.Span, name string, v *view) {
	if t, ok := allRoots[name]; ok && t != v.typ {
		c.bag.Error(span, diag.NotInScope,
			"%q is not in scope in %s", name, v.where()).
			Hintf("%s", v.hint())
		return
	}

	candidates := v.names()
	best, near := diag.Nearest(name, candidates)

	code := diag.UnknownIdentifier
	what := "unknown name"
	if near && v.isRoot(best) {
		code, what = diag.UnknownField, "unknown field"
	}

	ref := c.bag.Error(span, code, "%s %q", what, name)
	if near {
		ref.Hintf("did you mean %q?", best).Suggest(best)
		return
	}
	ref.Hintf("%s", inScope(candidates, v))
}

// inScope is the hint for a name with no near miss: what is actually
// available, which is more use than a guess. The step's roots come from the
// type's own sentence so that `page.url` reads as a path rather than as a bare
// `page`, and the vars and captures are appended because those are the names
// an author is most likely to have meant.
func inScope(candidates []string, v *view) string {
	extra := append(append([]string{}, v.vars...), v.captures...)
	if len(extra) == 0 {
		return v.hint()
	}
	return v.hint() + "\nalso in scope: " + strings.Join(extra, ", ")
}

// notInScopeFn reports a browser element function used outside a browser step.
// It is not-in-scope rather than unknown-identifier because the function
// exists; it is this step type that cannot use it.
func (c *checker) notInScopeFn(span token.Span, name string, v *view) {
	c.bag.Error(span, diag.NotInScope,
		"%q is not in scope in %s", name+"()", v.where()).
		Hintf("%s", v.hint())
}

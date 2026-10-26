// Package builtin is the one table of the functions an .art expression can
// call: each one's name, what each of its parameters takes, how many arguments
// it accepts, where it is callable, and what a call looks like for a hint.
//
// Before this package the same facts were kept four times -- token.Builtins
// for the names, pkg/dsl/check's params and minArgs for the checker, pkg/eval's
// arity for Eval, which must be total over a tree that was never checked, and
// pkg/codegen's builtinArity for `artemis build` -- with a test in each package
// comparing its copy to the checker's. The comparing worked until it did not:
// codegen's copy still said env() took exactly one argument after the other
// three had learned about its default, and that was found by hand. Every
// package now reads this table, so there is nothing left to compare.
//
// What is not here is what each builtin *does*. Evaluating one is pkg/eval's
// switch and emitting one is pkg/codegen's, and each of those packages has a
// test that every name in this table reaches a case of its own. Keeping the
// behaviour out is also what keeps this package's imports to token alone, so
// pkg/dsl/check can read it and still depend on nothing that runs.
package builtin

import "artemis/pkg/dsl/token"

// Scope is where a builtin is callable.
type Scope int

const (
	// Everywhere is callable in every scope, a var's value included: env()
	// reads the process environment and match() reads a string, and neither
	// needs a page or a response to mean something.
	Everywhere Scope = iota
	// Page is a browser element function, callable in a browser step only
	// because it reads the page that step left open.
	Page
)

// Param is one parameter: what it wants, in the words a diagnostic uses, and
// which literal kinds satisfy it.
//
// Only a *literal* is judged at compile time -- an identifier or an
// interpolation could hold anything and the checker does not evaluate -- so a
// parameter is a phrase plus a small set of token kinds.
type Param struct {
	// Want is the phrase after "must be": "a string", "a regular expression".
	Want string
	// Kinds are the literal kinds that satisfy it. A literal of any other kind
	// is reported; a non-literal is left to run time.
	Kinds []token.Kind
}

// Accepts reports whether a literal of kind k satisfies the parameter.
func (p Param) Accepts(k token.Kind) bool {
	for _, want := range p.Kinds {
		if k == want {
			return true
		}
	}
	return false
}

var (
	// String is a variable name, a selector, an attribute name, the text to
	// search: every parameter but match()'s pattern.
	String = Param{Want: "a string", Kinds: []token.Kind{token.String}}

	// Pattern is match()'s second argument. A string is allowed as well as a
	// regex literal, which is the rule `matches` already follows, so a pattern
	// can live in a var.
	Pattern = Param{Want: "a regular expression", Kinds: []token.Kind{token.Regex, token.String}}
)

// Fn is one builtin.
type Fn struct {
	Name string
	// Params is every parameter in order, so its length is the most arguments
	// a call may pass.
	Params []Param
	// Min is the fewest. It is below len(Params) only for a builtin whose
	// trailing parameters are optional -- env()'s default value -- and equal
	// to it for every other.
	Min   int
	Scope Scope
	// Signature is what a call looks like, for the hint under an arity or a
	// wrong-kind diagnostic.
	Signature string
}

// Max is the most arguments a call may pass.
func (f Fn) Max() int { return len(f.Params) }

// table is every builtin, in the order a reader meets them: the two callable
// everywhere, then the five that read a page. Names returns this order, and
// `artemis grammar --json` lists it, so it is not alphabetical on purpose.
var table = []Fn{
	{Name: "env", Params: []Param{String, String}, Min: 1, Scope: Everywhere,
		Signature: `env("API_URL") or env("PORT", "8080")`},
	{Name: "match", Params: []Param{String, Pattern}, Min: 2, Scope: Everywhere,
		Signature: `match(raw, /id=([0-9]+)/)`},
	{Name: "text", Params: []Param{String}, Min: 1, Scope: Page,
		Signature: `text("[role=status]")`},
	{Name: "value", Params: []Param{String}, Min: 1, Scope: Page,
		Signature: `value("#email")`},
	{Name: "attr", Params: []Param{String, String}, Min: 2, Scope: Page,
		Signature: `attr("#link", "href")`},
	{Name: "count", Params: []Param{String}, Min: 1, Scope: Page,
		Signature: `count(".invoice")`},
	{Name: "visible", Params: []Param{String}, Min: 1, Scope: Page,
		Signature: `visible(".modal")`},
}

// byName is table keyed by name, built once so a lookup and the table cannot
// disagree.
var byName = func() map[string]Fn {
	m := make(map[string]Fn, len(table))
	for _, f := range table {
		m[f.Name] = f
	}
	return m
}()

// Lookup returns the builtin called name, and whether there is one.
func Lookup(name string) (Fn, bool) {
	f, ok := byName[name]
	return f, ok
}

// Is reports whether name is a builtin, callable somewhere.
func Is(name string) bool {
	_, ok := byName[name]
	return ok
}

// Arity is how many arguments name accepts -- the fewest and the most -- and
// whether it is a builtin at all.
func Arity(name string) (minimum, maximum int, ok bool) {
	f, ok := byName[name]
	if !ok {
		return 0, 0, false
	}
	return f.Min, f.Max(), true
}

// Names is every builtin's name, in table order. The slice is a copy.
func Names() []string {
	out := make([]string, len(table))
	for i, f := range table {
		out[i] = f.Name
	}
	return out
}

// In is the names of the builtins callable in scope s, in table order.
func In(s Scope) []string {
	var out []string
	for _, f := range table {
		if f.Scope == s {
			out = append(out, f.Name)
		}
	}
	return out
}

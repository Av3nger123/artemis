// Package builtin is the one table of the functions an .art expression can
// call: each one's name, what each of its parameters takes, how many arguments
// it accepts, where it is callable, and what a call looks like for a hint.
//
// Before this package the same facts were kept in several places -- token
// for the names, pkg/dsl/check's params and minArgs for the checker, pkg/eval's
// arity for Eval, which must be total over a tree that was never checked --
// with a test in each package comparing its copy to the checker's. Every
// package now reads this table, so there is nothing left to compare.
//
// What is not here is what each builtin *does*. Evaluating one is pkg/eval's
// switch, which has a test that every name in this table reaches a case of
// its own. Keeping the
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

// Type is what the evaluator converts an argument to before a builtin sees it.
type Type int

const (
	// TypeString is text. Anything else is a step error naming the argument.
	TypeString Type = iota
	// TypeNumber is a number, or a string that reads as one -- the rule the
	// ordering operators already follow.
	TypeNumber
	// TypePattern is a regular expression, or a string compiled as one.
	TypePattern
	// TypeAny is any value, passed through as it is.
	TypeAny
	// TypeBytes is text, or a file() whose contents are read in its place:
	// what base64() and the hashes take.
	TypeBytes
)

// Param is one parameter: what it wants, in the words a diagnostic uses, which
// literal kinds satisfy it at compile time, and what it is at run time.
//
// Only a *literal* is judged at compile time -- an identifier or an
// interpolation could hold anything and the checker does not evaluate -- so the
// compile-time half of a parameter is a phrase plus a small set of token kinds.
type Param struct {
	// Want is the phrase after "must be": "a string", "a regular expression".
	Want string
	// Kinds are the literal kinds that satisfy it. A literal of any other kind
	// is reported; a non-literal is left to run time.
	Kinds []token.Kind
	// Type is what the evaluator converts the argument to.
	Type Type
	// OneOf, when it is not empty, is every string the parameter accepts: a
	// literal outside it is a compile error with a did-you-mean, and a value
	// outside it is a step error.
	OneOf []string
	// Whole is a number that must have no fractional part: a count, a length,
	// a bound.
	Whole bool
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

// TimeFormats are what now() can answer in: a timestamp, a date, or seconds or
// milliseconds since the Unix epoch.
var TimeFormats = []string{"rfc3339", "date", "unix", "unix_ms"}

var (
	// String is a variable name, a selector, an attribute name, the text to
	// search or to encode.
	String = Param{Want: "a string", Kinds: []token.Kind{token.String}, Type: TypeString}

	// Pattern is match()'s second argument. A string is allowed as well as a
	// regex literal, which is the rule `matches` already follows, so a pattern
	// can live in a var.
	Pattern = Param{Want: "a regular expression", Kinds: []token.Kind{token.Regex, token.String}, Type: TypePattern}

	// WholeNumber is a count or a bound: random_int()'s, random_string()'s.
	WholeNumber = Param{Want: "a whole number", Kinds: []token.Kind{token.Number}, Type: TypeNumber, Whole: true}

	// Value is anything an expression can produce but a regular expression,
	// which is a pattern and not a value: len(), to_json(), number(), string().
	Value = Param{Want: "a value", Kinds: []token.Kind{token.String, token.Number, token.Bool, token.Null}, Type: TypeAny}

	// Format is now()'s format name.
	Format = Param{Want: "a time format", Kinds: []token.Kind{token.String}, Type: TypeString, OneOf: TimeFormats}

	// Bytes is text, or a file whose contents stand in for it.
	Bytes = Param{Want: "a string or a file", Kinds: []token.Kind{token.String}, Type: TypeBytes}

	// Path is a file's path, relative to the file the call is written in.
	Path = Param{Want: "a path", Kinds: []token.Kind{token.String}, Type: TypeString}
)

// Fn is one builtin.
type Fn struct {
	Name string
	// Params is every parameter in order, so its length is the most arguments
	// a call may pass.
	Params []Param
	// Min is the fewest. It is below len(Params) only for a builtin whose
	// trailing parameters are optional -- env()'s default value, now()'s
	// format -- and equal to it for every other.
	Min   int
	Scope Scope
	// Signature is what a call looks like, for the hint under an arity or a
	// wrong-kind diagnostic.
	Signature string
	// Static is a builtin that reads a file the scenario's own repository
	// holds, so a literal path is checked when the file compiles -- it exists,
	// and parses -- rather than when a step reaches it.
	Static bool
}

// Max is the most arguments a call may pass.
func (f Fn) Max() int { return len(f.Params) }

// table is every builtin, in the order a reader meets them: the ones callable
// everywhere, then the five that read a page. Names returns this order, and
// `artemis grammar --json` lists it, so it is not alphabetical on purpose.
var table = []Fn{
	{Name: "env", Params: []Param{String, String}, Min: 1, Scope: Everywhere,
		Signature: `env("API_URL") or env("PORT", "8080")`},
	{Name: "match", Params: []Param{String, Pattern}, Min: 2, Scope: Everywhere,
		Signature: `match(raw, /id=([0-9]+)/)`},

	// Test data: a value a run makes up rather than reads. The random ones draw
	// from the scenario's seeded generator, so --seed reproduces them.
	{Name: "uuid", Scope: Everywhere, Signature: `uuid()`},
	{Name: "now", Params: []Param{Format}, Min: 0, Scope: Everywhere,
		Signature: `now() or now("unix_ms")`},
	{Name: "random_int", Params: []Param{WholeNumber, WholeNumber}, Min: 2, Scope: Everywhere,
		Signature: `random_int(1, 100)`},
	{Name: "random_string", Params: []Param{WholeNumber}, Min: 1, Scope: Everywhere,
		Signature: `random_string(12)`},

	// Encoding and hashing, for a header an API wants built rather than typed.
	{Name: "base64", Params: []Param{Bytes}, Min: 1, Scope: Everywhere,
		Signature: `base64("user:password") or base64(file("logo.png"))`},
	{Name: "base64_decode", Params: []Param{String}, Min: 1, Scope: Everywhere,
		Signature: `base64_decode(body.content)`},
	{Name: "url_encode", Params: []Param{String}, Min: 1, Scope: Everywhere,
		Signature: `url_encode("a b&c")`},
	{Name: "sha256", Params: []Param{Bytes}, Min: 1, Scope: Everywhere,
		Signature: `sha256(raw)`},
	{Name: "hmac_sha256", Params: []Param{String, Bytes}, Min: 2, Scope: Everywhere,
		Signature: `hmac_sha256(key, raw)`},
	{Name: "jwt_claims", Params: []Param{String}, Min: 1, Scope: Everywhere,
		Signature: `jwt_claims(token)`},

	// Values: between JSON text and a value, and between types.
	{Name: "to_json", Params: []Param{Value}, Min: 1, Scope: Everywhere,
		Signature: `to_json({"id": 1})`},
	{Name: "from_json", Params: []Param{String}, Min: 1, Scope: Everywhere,
		Signature: `from_json(body.payload)`},
	{Name: "len", Params: []Param{Value}, Min: 1, Scope: Everywhere,
		Signature: `len(body.items)`},
	{Name: "number", Params: []Param{Value}, Min: 1, Scope: Everywhere,
		Signature: `number(row.quantity)`},
	{Name: "string", Params: []Param{Value}, Min: 1, Scope: Everywhere,
		Signature: `string(body.id)`},

	// Data and files, each found relative to the file the call is written in.
	{Name: "json", Params: []Param{Path}, Min: 1, Scope: Everywhere, Static: true,
		Signature: `json("data/users.json")`},
	{Name: "csv", Params: []Param{Path}, Min: 1, Scope: Everywhere, Static: true,
		Signature: `csv("data/users.csv")`},
	{Name: "file", Params: []Param{Path}, Min: 1, Scope: Everywhere, Static: true,
		Signature: `file("fixtures/avatar.png")`},

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

// Random is whether name draws from the scenario's random generator, which is
// what decides whether a run's seed is worth reporting.
func Random(name string) bool {
	return name == "uuid" || name == "random_int" || name == "random_string"
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

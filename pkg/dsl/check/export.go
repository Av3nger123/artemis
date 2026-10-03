package check

import "artemis/pkg/dsl/token"

// The checker's tables, readable from outside.
//
// `artemis grammar --json` ships the enumerable choice points of the language
// so that a UI's dropdowns come from the binary rather than from a hardcoded
// list that drifts. Most of those sets are token's word tables, which are
// already exported; the rest are here, because they are the checker's
// conclusions and not the lexer's vocabulary:
//
//   - what a field's value has to be, which is fieldSpec.kind
//   - whether a browser action takes a value, which is browserArity's rule
//   - what a reserved word is reserved for, which is reserved.go's purposes
//
// Every accessor below projects an existing table rather than restating one.
// Adding a field to a blockSpec or an action to token.BrowserActions changes
// what these return with no edit here, which is the property export_test.go
// and pkg/dsl/grammar's tests exist to keep.

// FieldInfo is one field of one block: its name, what its value has to be, and
// whether a name comes before that value the way `header "Accept" = v` has one.
type FieldInfo struct {
	Name      string
	ValueKind string
	NeedsKey  bool
}

// ActInfo is one browser action: whether it takes a value as well as a
// selector, and the shape a hint shows.
type ActInfo struct {
	Name       string
	TakesValue bool
	Example    string
}

// ReservedInfo is one reserved word and what it is held for.
type ReservedInfo struct {
	Word    string
	Purpose string
}

// blockSpecs names the five blocks whose fields are a closed set. The names
// are the contract -- they appear in `artemis grammar --json` -- so they are
// spelled the way the grammar spells the block rather than the way the Go
// variable is spelled.
var blockSpecs = map[string]blockSpec{
	"request":       requestBlock,
	"run":           runBlock,
	"retry":         retryBlock,
	"step":          stepBlock,
	"configBrowser": browserConfigBlock,
}

// BlockNames returns the blocks BlockFields knows, in grammar order: the three
// action-and-statement blocks, the step's own fields, then config's.
func BlockNames() []string {
	return []string{"request", "run", "retry", "step", "configBrowser"}
}

// BlockFields returns block's fields in the order its token table lists them,
// and whether block is one BlockNames named.
//
// The order comes from blockSpec.names, which is the token table, so a form
// renders `header, query, body` rather than whatever order a map ranged in.
func BlockFields(block string) ([]FieldInfo, bool) {
	spec, ok := blockSpecs[block]
	if !ok {
		return nil, false
	}
	out := make([]FieldInfo, 0, len(spec.names))
	for _, name := range spec.names {
		fs, known := spec.fields[name]
		if !known {
			// A name in the token table with no spec is a bug in field.go,
			// caught by TestEveryBlockFieldHasASpec rather than papered over.
			continue
		}
		out = append(out, FieldInfo{Name: name, ValueKind: fs.kind.String(), NeedsKey: fs.key})
	}
	return out, true
}

// BrowserActs returns every browser action with its arity, in the order
// token.BrowserActions lists them.
//
// TakesValue is the negation of oneArg, which is the rule browserArity
// enforces, so the second widget a form renders for `fill` and withholds from
// `click` is decided by the same table that rejects `click "x" = 1`.
func BrowserActs() []ActInfo {
	out := make([]ActInfo, 0, len(token.BrowserActions))
	for _, name := range token.BrowserActions {
		out = append(out, ActInfo{
			Name:       name,
			TakesValue: !oneArg[name],
			Example:    actSignatures[name],
		})
	}
	return out
}

// ReservedWords returns every reserved word with its purpose, in the order
// token.Reserved lists them -- which groups them by the feature they are held
// for, and is worth more in a list than alphabetical order.
func ReservedWords() []ReservedInfo {
	out := make([]ReservedInfo, 0, len(token.Reserved))
	for _, w := range token.Reserved {
		out = append(out, ReservedInfo{Word: w, Purpose: purposes[w]})
	}
	return out
}

// StepTypes returns the three step types a file can hold. Unknown is not one
// of them: it is what the checker uses for a step whose action did not parse
// and for a var's initialiser, and it is not a type anybody writes.
func StepTypes() []StepType { return []StepType{API, Terminal, Browser} }

// Classes returns the two labels an expect carries, simple first, because that
// is the one a form renders as three widgets and the interesting one to read.
func Classes() []Class { return []Class{Simple, Complex} }

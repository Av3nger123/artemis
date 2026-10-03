package migrate

// The operator names a YAML check may give, and the JSON type names a body
// check's `type:` may name.
//
// They were pkg/shared/assert's, which was the YAML surface's assertion engine
// until ART-40 deleted it: pkg/eval subsumes every one of these comparisons,
// and spells them as operators rather than as names. Translating the names is
// the only thing left that needs them, so they live here, next to the code that
// translates them -- see expect.go for what each one lowers to.
const (
	OpEquals   = "equals"
	OpContains = "contains"
	OpMatches  = "matches"
	OpExists   = "exists"
	OpType     = "type"
	OpGt       = "gt"
	OpGte      = "gte"
	OpLt       = "lt"
	OpLte      = "lte"

	// OpEmpty is an operator only a text check may name: the stream held
	// nothing but whitespace. The DSL has no `empty`, so it migrates to the
	// pattern that says the same thing.
	OpEmpty = "empty"
)

// Operators are every operator a body check may name.
var Operators = []string{OpEquals, OpContains, OpMatches, OpExists, OpType, OpGt, OpGte, OpLt, OpLte}

// TextOperators are the operators a text check may name. It is a shorter list
// on purpose: ordering and type operators have nothing to say about a stream of
// bytes.
var TextOperators = []string{OpContains, OpEquals, OpMatches, OpEmpty}

// TypeNames are the type names a body check's `type:` may give, and the
// right-hand side `is` lowers to. They are token.TypeNames in the same order;
// the lists are compared in pkg/shared/spec_test.go.
var TypeNames = []string{"string", "number", "boolean", "object", "array", "null"}

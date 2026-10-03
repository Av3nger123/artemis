package token

// The enumerable word sets of the grammar, in one place.
//
// The lexer classifies almost none of these. Every identifier-shaped word in a
// file lexes to Ident -- only true, false and null get kinds of their own --
// because in this grammar every other word is positional. `scenario`, `get`,
// `body`, `header`, `contains` and `within` are all legal names: `capture body
// = ...` and `var status = env("S")` are files someone will write, and a lexer
// that promoted words to keywords would make them unlexable. So the parser
// matches on Text and consults these tables.
//
// Keeping them here rather than in the parser is what stops them being
// duplicated. The checker reads Reserved to reject a reserved word, and
// `artemis grammar --json` serialises all of them so a UI's dropdowns come from
// the binary instead of a hardcoded list that drifts.
var (
	// Methods are the HTTP verbs that open an api step's action block. A step's
	// type comes from its action, so this list *is* the api-step predicate.
	Methods = []string{"get", "post", "put", "patch", "delete", "head", "options"}

	// Actions are the three action-block openers, one per step type. `run` is
	// terminal, `browser` is browser, and an HTTP verb from Methods is api.
	Actions = []string{"run", "browser"}

	// BrowserActions are the statements legal inside a `browser` block.
	BrowserActions = []string{"goto", "click", "fill", "select", "press", "hover", "upload", "wait"}

	// TypeNames are the right-hand side of `is`.
	TypeNames = []string{"string", "number", "boolean", "object", "array", "null"}

	// WordOperators are the operators spelled as words. `and`, `or` and `not`
	// are logical; `contains` and `matches` are binary comparisons alongside
	// ==, !=, <, <=, > and >=; `exists` and `is` are postfix predicates.
	WordOperators = []string{"and", "or", "not", "contains", "matches", "exists", "is"}

	// Comparisons are the grammar's BinOp, symbols and words together, which is
	// the operator dropdown a form needs for a simple assertion.
	Comparisons = []string{"==", "!=", "<", "<=", ">", ">=", "contains", "matches"}

	// Blocks are the declarations a scenario body holds, plus `scenario`
	// itself.
	Blocks = []string{"scenario", "config", "var", "step"}

	// StepStatements are the statements legal in a step after its action.
	StepStatements = []string{"expect", "capture", "retry", "timeout", "within"}

	// RequestFields are the fields of an api step's action block.
	RequestFields = []string{"header", "query", "body"}

	// RunFields are the fields of a `run` block.
	RunFields = []string{"args", "cwd", "stdin", "env"}

	// RetryFields are the fields of a `retry` block.
	RetryFields = []string{"times", "delay"}

	// ConfigBlocks are the subjects of a `config` declaration. Only `browser`
	// has settings today.
	ConfigBlocks = []string{"browser"}

	// BrowserConfigFields are what `config browser` accepts. Deliberately tiny.
	BrowserConfigFields = []string{"headless", "viewport"}

	// Builtins are the functions callable in any expression scope. env() and
	// match() are the two that are everywhere; the rest are browser-scope
	// element functions and the checker rejects them elsewhere.
	Builtins = []string{"env", "match", "text", "value", "attr", "count", "visible"}

	// Reserved words are a parse error wherever a name is expected, so that a
	// later tier can add control flow, functions or agentic assertions without
	// a breaking change. The lexer does not enforce this -- using one lexes to
	// an ordinary Ident -- because the error wants to name what the word is
	// reserved for, and that is the checker's vocabulary.
	Reserved = []string{
		"if", "else", "for", "in", "while", "parallel", "group",
		"fn", "return", "import", "use", "let", "setup", "teardown", "ai",
	}
)

// Lookups, built once from the slices above so there is exactly one source for
// each set and a slice and its lookup cannot disagree.
var (
	methods        = set(Methods)
	actions        = set(Actions)
	browserActions = set(BrowserActions)
	typeNames      = set(TypeNames)
	wordOperators  = set(WordOperators)
	builtins       = set(Builtins)
	reserved       = set(Reserved)
)

func set(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// IsMethod reports whether word is an HTTP verb, and so opens an api step.
func IsMethod(word string) bool { return methods[word] }

// IsAction reports whether word opens a non-api action block.
func IsAction(word string) bool { return actions[word] }

// IsBrowserAction reports whether word is a statement inside a browser block.
func IsBrowserAction(word string) bool { return browserActions[word] }

// IsTypeName reports whether word is a type name, the right-hand side of `is`.
func IsTypeName(word string) bool { return typeNames[word] }

// IsWordOperator reports whether word is an operator spelled as a word.
func IsWordOperator(word string) bool { return wordOperators[word] }

// IsBuiltin reports whether word names a callable builtin.
func IsBuiltin(word string) bool { return builtins[word] }

// IsReserved reports whether word is reserved for a later tier of the language.
func IsReserved(word string) bool { return reserved[word] }

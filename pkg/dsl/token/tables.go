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

	// Blocks are the top-level declarations of a file -- `import`,
	// `collection` and `scenario` -- and the declarations a scenario body
	// holds, `use` among them.
	Blocks = []string{"import", "collection", "scenario", "config", "var", "step", "use"}

	// StepStatements are the statements legal in a step after its action.
	StepStatements = []string{"expect", "capture", "retry", "timeout", "within"}

	// RequestFields are the fields of an api step's action block.
	RequestFields = []string{"header", "query", "body"}

	// RunFields are the fields of a `run` block.
	RunFields = []string{"args", "cwd", "stdin", "env"}

	// RetryFields are the fields of a `retry` block.
	RetryFields = []string{"times", "delay"}

	// StepFields are the fields of a step itself, as opposed to of its action
	// block: `timeout = "5s"` and `retry { ... }`. They are listed here rather
	// than in the checker so that every field set of the grammar is in one
	// place and `artemis grammar --json` has nowhere else to look.
	StepFields = []string{"timeout", "retry"}

	// CollectionItems are what a collection body holds.
	CollectionItems = []string{"request", "flow"}

	// UseLines are the words that open an override line in a use block. Any
	// other identifier opening a line there is an argument, which is why a
	// parameter may not be named one of these.
	UseLines = []string{"header", "query", "body", "drop", "expect", "in"}

	// DropTargets are what a use's `drop` line can remove from the template:
	// `drop expects` its expects, `drop captures` its captures.
	DropTargets = []string{"expects", "captures"}

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
	useLines       = set(UseLines)
	dropTargets    = set(DropTargets)

	// comparisonKinds and comparisonWords are Comparisons split by how a token
	// spells each entry: the six symbols arrive with a Kind of their own, the
	// two words arrive as Ident. Both halves are built from that one slice, so
	// an operator added there is recognised by IsComparison with no second
	// edit -- and an entry that is neither a known symbol nor a word operator
	// is caught by TestComparisonsAreSymbolsOrWordOperators rather than by
	// quietly never matching.
	comparisonKinds, comparisonWords = splitComparisons()
)

func splitComparisons() (map[Kind]bool, map[string]bool) {
	kindSet, wordSet := map[Kind]bool{}, map[string]bool{}
	for _, c := range Comparisons {
		if k, ok := kindOf(c); ok {
			kindSet[k] = true
			continue
		}
		wordSet[c] = true
	}
	return kindSet, wordSet
}

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

// IsUseLine reports whether word opens an override line in a use block.
func IsUseLine(word string) bool { return useLines[word] }

// IsDropTarget reports whether word may follow `drop` in a use block.
func IsDropTarget(word string) bool { return dropTargets[word] }

// IsBuiltin reports whether word names a callable builtin.
func IsBuiltin(word string) bool { return builtins[word] }

// IsReserved reports whether word is reserved for a later tier of the language.
func IsReserved(word string) bool { return reserved[word] }

// IsComparison reports whether t is one of the grammar's BinOp -- the six
// symbol comparisons and the two word ones.
//
// It takes a whole Token rather than a string because the two halves of
// Comparisons are not spelled alike: `==` is a Kind and carries no Value,
// while `contains` is an Ident like every other word in this grammar. The
// parser's Cmp rule and the checker's simple/complex classifier both ask this,
// so the operator dropdown a UI renders and the operators the compiler accepts
// are one set by construction.
func IsComparison(t Token) bool {
	if t.Kind == Ident {
		return comparisonWords[t.Value]
	}
	return comparisonKinds[t.Kind]
}

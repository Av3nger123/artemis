package grammar

import (
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// SchemaVersion is the version of the choice-point document. It goes up when a
// client that already parses one would read the next one wrongly: a field
// removed or renamed, a meaning changed. A new choice point or a new value in
// an existing one does not move it -- the whole purpose of this document is
// that those arrive without a client shipping.
//
// It is deliberately not encode.SchemaVersion. The two describe different
// documents and will move for different reasons, and sharing a number would
// force a UI to re-read the tree encoding because an operator was added.
const SchemaVersion = 1

// ChoicePoints is what `artemis grammar --json` emits.
type ChoicePoints struct {
	SchemaVersion int               `json:"schemaVersion"`
	Choices       map[string]Choice `json:"choices"`
}

// Choice is one enumerable choice point: one dropdown's worth of options.
type Choice struct {
	Doc    string  `json:"doc"`
	Values []Value `json:"values"`
}

// Value is one option.
//
// Every set's values are objects, not bare strings, even the sets with
// nothing to say beyond the word. Half of them carry a second fact -- a
// browser action's arity, a field's value kind, a reserved word's purpose, a
// step type's scope, a diagnostic code's severity -- and a client that has to
// special-case "this one set is strings" is a client that will get it wrong.
// The extras are omitted where there are none.
type Value struct {
	Value string `json:"value"`
	Doc   string `json:"doc,omitempty"`

	// ValueKind is what a field's value has to be: any, array, boolean,
	// integer, duration, or block. From check's fieldSpec.
	ValueKind string `json:"valueKind,omitempty"`

	// NeedsKey is a field written `header "Accept" = v`, with a name between
	// the field and its value.
	NeedsKey bool `json:"needsKey,omitempty"`

	// TakesValue is a browser action written `fill "#email" = v`, as against
	// `click "x"`. It is the arity the checker enforces, so a form renders a
	// second widget exactly where artemis expects a second operand.
	TakesValue bool `json:"takesValue,omitempty"`

	// Example is a browser action's shape, the same string its arity
	// diagnostic hints with.
	Example string `json:"example,omitempty"`

	// Purpose is what a reserved word is held for.
	Purpose string `json:"purpose,omitempty"`

	// Severity is a diagnostic code's default severity.
	Severity string `json:"severity,omitempty"`

	// Roots, Functions and Members are a step type's scope: the identifier
	// roots it binds, the builtins callable in it, and the closed member sets
	// of the roots that have one. This is a path picker's whole input.
	Roots     []string            `json:"roots,omitempty"`
	Functions []string            `json:"functions,omitempty"`
	Members   map[string][]string `json:"members,omitempty"`
}

// Choices is every enumerable choice point in the language.
//
// Every list below is read from the table that owns it, at call time:
// pkg/dsl/token's word tables, pkg/dsl/check's block specs and scope
// accessors, pkg/dsl/diag's code registry. A word added to one of those
// appears here with no edit to this file, which is the property choices_test.go
// asserts -- including by parsing token/tables.go and failing on a table that
// reaches no choice point.
func Choices() ChoicePoints {
	return ChoicePoints{
		SchemaVersion: SchemaVersion,
		Choices: map[string]Choice{
			"declaration": words(
				"The declarations a scenario body holds, plus scenario itself.",
				token.Blocks),
			"stepType": stepTypes(),
			"method":   words("The HTTP verbs. A step whose action opens with one is an api step.", token.Methods),
			"action":   words("The action-block openers that are not an HTTP verb: run is a terminal step, browser is a browser step.", token.Actions),
			"stepStatement": words(
				"The statements legal in a step after its action block.",
				token.StepStatements),
			"browserAction":      browserActions(),
			"comparison":         words("The grammar's BinOp: the operator dropdown of a simple assertion.", token.Comparisons),
			"wordOperator":       words("The operators spelled as words: and, or and not are logical, contains and matches are comparisons, exists and is are postfix predicates.", token.WordOperators),
			"typeName":           words("The right-hand side of is.", token.TypeNames),
			"builtin":            words("The functions callable in an expression. Which are in scope depends on the step type; see stepType.", token.Builtins),
			"configSubject":      words("The subjects of a config declaration.", token.ConfigBlocks),
			"collectionItem":     words("What a collection body holds: a request or a flow.", token.CollectionItems),
			"useLine":            words("The words that open an override line in a use block. Any other identifier opening a line there is an argument.", token.UseLines),
			"requestField":       fields("The fields of an api step's action block.", "request"),
			"runField":           fields("The fields of a run block.", "run"),
			"retryField":         fields("The fields of a retry block.", "retry"),
			"stepField":          fields("The fields of a step itself, as against of its action block.", "step"),
			"configBrowserField": fields("The settings of a config browser block.", "configBrowser"),
			"expectClass":        expectClasses(),
			"reservedWord":       reservedWords(),
			"diagnosticCode":     diagnosticCodes(),
			"severity":           severities(),
		},
	}
}

// words is the common case: a token table, in its own order, with nothing to
// say about each entry beyond the word.
func words(doc string, table []string) Choice {
	values := make([]Value, 0, len(table))
	for _, w := range table {
		values = append(values, Value{Value: w})
	}
	return Choice{Doc: doc, Values: values}
}

// fields projects one of check's block specs, so the value kind a form
// renders a widget for is the one the checker enforces.
func fields(doc, block string) Choice {
	fs, ok := check.BlockFields(block)
	if !ok {
		// Unreachable: the block names are constants here and
		// TestEveryBlockIsAChoicePoint pins them against check.BlockNames.
		return Choice{Doc: doc}
	}
	values := make([]Value, 0, len(fs))
	for _, f := range fs {
		values = append(values, Value{Value: f.Name, ValueKind: f.ValueKind, NeedsKey: f.NeedsKey})
	}
	return Choice{Doc: doc, Values: values}
}

func browserActions() Choice {
	acts := check.BrowserActs()
	values := make([]Value, 0, len(acts))
	for _, a := range acts {
		values = append(values, Value{Value: a.Name, TakesValue: a.TakesValue, Example: a.Example})
	}
	return Choice{
		Doc:    "The statements legal inside a browser block. takesValue is whether the action takes a value as well as a selector.",
		Values: values,
	}
}

// stepTypes carries each type's scope with it, because a path picker's
// options are per step type and a client holding the three lists separately
// would have to join them itself.
func stepTypes() Choice {
	types := check.StepTypes()
	values := make([]Value, 0, len(types))
	for _, t := range types {
		v := Value{
			Value:     t.String(),
			Roots:     check.Roots(t),
			Functions: check.Functions(t),
		}
		for _, root := range v.Roots {
			members, closed := check.Members(root)
			if !closed {
				continue
			}
			if v.Members == nil {
				v.Members = map[string][]string{}
			}
			v.Members[root] = members
		}
		values = append(values, v)
	}
	return Choice{
		Doc:    "What a step does, inferred from its action block, with the names it binds. A root absent from members has no closed member set: its shape is a run-time fact.",
		Values: values,
	}
}

func expectClasses() Choice {
	classes := check.Classes()
	values := make([]Value, 0, len(classes))
	for _, c := range classes {
		values = append(values, Value{Value: c.String()})
	}
	return Choice{
		Doc:    "The label the checker puts on every expect. simple is <path> <op> <literal>, <path> exists, <path> is <type>, or the not of one of those, and renders as three widgets; complex renders as one raw expression field.",
		Values: values,
	}
}

func reservedWords() Choice {
	reserved := check.ReservedWords()
	values := make([]Value, 0, len(reserved))
	for _, r := range reserved {
		values = append(values, Value{Value: r.Word, Purpose: r.Purpose})
	}
	return Choice{
		Doc:    "Words a later tier of the language is holding. Using one as a name is an error, so a form must refuse them.",
		Values: values,
	}
}

func diagnosticCodes() Choice {
	codes := diag.Codes()
	values := make([]Value, 0, len(codes))
	for _, c := range codes {
		values = append(values, Value{
			Value:    string(c.Code),
			Doc:      c.Description,
			Severity: c.Severity.String(),
		})
	}
	return Choice{
		Doc:    "Every code artemis parse --json can emit. These are stable independently of message text: key behaviour off the code, never off the message.",
		Values: values,
	}
}

func severities() Choice {
	all := diag.Severities()
	values := make([]Value, 0, len(all))
	for _, s := range all {
		values = append(values, Value{Value: s.String()})
	}
	return Choice{
		Doc:    "A diagnostic's severity. An error means the file does not compile.",
		Values: values,
	}
}

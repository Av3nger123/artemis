package diag

import "sort"

// Code is a diagnostic's stable identifier.
//
// This is the invariant the whole file exists to protect: a code never changes
// because a message did. A UI keys behaviour off codes -- which quick fix to
// offer, which field to focus, which help page to link -- so rewording
// `unknown field "statu"` for clarity must not break a client, and renaming
// `unknown-field` must be a visible, deliberate break. Codes are kebab-case and
// read as a noun phrase naming the fault, not the fix.
//
// This file is the one place the list lives. Codes() returns it, so `artemis
// grammar --json` and SPEC.md read the registry rather than restating it, and
// codes_test.go pins the whole table to testdata/codes.golden -- which makes a
// rename or a deletion a diff someone has to approve.
type Code string

// The lexical codes. These are produced by the lexer, which sets them as plain
// strings on Invalid tokens so that pkg/dsl/token depends on nothing; the
// strings here are the other half of that contract and TestLexerCodesRegistered
// is what stops the two halves drifting.
const (
	// UnterminatedString is a string literal with no closing quote. It
	// swallows the rest of the line by design, so it is usually the last
	// useful diagnostic in a file.
	UnterminatedString Code = "unterminated-string"

	// UnterminatedRegex is a regex literal with no closing slash.
	UnterminatedRegex Code = "unterminated-regex"

	// UnclosedInterpolation is a `${` inside a string with no closing `}`.
	UnclosedInterpolation Code = "unclosed-interpolation"

	// InvalidEscape is a backslash escape a string literal does not define.
	InvalidEscape Code = "invalid-escape"

	// UnexpectedCharacter is a byte that starts no token.
	UnexpectedCharacter Code = "unexpected-character"
)

// The parser's codes. Everything here is a *shape* fault -- a token where
// another was wanted, a block left open, a step with no action -- as against
// the checker's codes below, which are name faults. The split is what decides
// which stage reports a mistake: a field position accepts any identifier, so
// `statu = 3` is UnknownField with a did-you-mean rather than a syntax error
// with nothing to suggest.
const (
	// UnexpectedToken is the catch-all: a token where the grammar wanted
	// something else. Its message names both what was found and what was
	// expected, because "unexpected token" on its own tells an author nothing.
	UnexpectedToken Code = "unexpected-token"

	// MissingSeparator is two statements with nothing between them. Statements
	// separate by newline and there are no semicolons, so this is almost
	// always a line that ran on.
	MissingSeparator Code = "missing-separator"

	// UnclosedBlock is a "{", "(" or "[" the file ends without closing. The
	// span points at the opener, not at end of file, because the opener is
	// what the author has to go and look at.
	UnclosedBlock Code = "unclosed-block"

	// MissingAction is a step block with no action block in it. A step's type
	// comes from its action, so a step without one has no type and nothing to
	// run.
	MissingAction Code = "missing-action"

	// DuplicateAction is a second action block in one step. The span points at
	// the second one, and the hint points back at the first.
	DuplicateAction Code = "duplicate-action"

	// ActionNotFirst is a step statement above the action block. The step
	// still parses -- one precise diagnostic beats a cascade of unexpected
	// tokens -- and this says what to move.
	ActionNotFirst Code = "action-not-first"

	// NonAssociativeOperator is a comparison chained with another, `a == b ==
	// c`. The grammar permits at most one per expression, so this is reported
	// rather than silently folded one way or the other.
	NonAssociativeOperator Code = "non-associative-operator"

	// NestingTooDeep is a file nested past the parser's limit. It is a
	// resource bound rather than a grammar rule: recursive descent costs a
	// stack frame per level, and without a limit a file of 100,000 open
	// parentheses is a stack overflow -- which is a fatal runtime error, not
	// a panic a caller can recover, so it would break the front end's promise
	// that a malformed file yields diagnostics and never a stack trace.
	NestingTooDeep Code = "nesting-too-deep"

	// UnclosedInterpolationExpr is a `${` whose expression is followed by
	// neither a `}` nor more string. It is the parser's counterpart to the
	// lexer's UnclosedInterpolation: the lexer catches a `${` that reaches end
	// of string, this catches one whose contents stop making sense first.
	UnclosedInterpolationExpr Code = "unclosed-interpolation-expr"

	// ImportPlacement is an import below a collection or a scenario. Imports
	// come first so a reader sees every dependency of a file at its top.
	ImportPlacement Code = "import-placement"

	// ReservedParam is a parameter named like a use-block override word, which
	// no use could ever pass.
	ReservedParam Code = "reserved-param"

	// BadCollectionName is a collection whose name is not an identifier. A
	// use names a collection as an identifier, so `collection "my-coll"`
	// would declare a collection no use could reach.
	BadCollectionName Code = "bad-collection-name"
)

// The checker's codes, named in docs/artemis-dsl-design.md and emitted by
// pkg/dsl/check. Everything here is a *name* fault, as against the parser's
// shape faults above: the word in the file is spelled the way the grammar
// wants and means nothing, or means something that is not available here.
//
// The split between UnknownField and UnknownIdentifier is by the *near miss*,
// not by position. An unresolvable name whose nearest in-scope candidate is
// one of the step type's own roots is UnknownField -- the author was reaching
// for a field of the step's result and misspelled it, which is the design
// document's worked example, `expect statu == 200`. Any other unresolvable
// name is UnknownIdentifier. A UI can therefore offer a field picker for the
// first and a name picker for the second.
const (
	// UnknownField is an identifier that is not a field of anything in scope
	// -- the design's worked example, `expect statu == 200`. It carries a
	// did-you-mean hint and a suggestion when a near name exists.
	UnknownField Code = "unknown-field"

	// NotInScope is a name that exists in the language but not in this step's
	// type: `status` in a browser step. The hint lists the roots that are.
	NotInScope Code = "not-in-scope"

	// UnknownIdentifier is a name bound nowhere -- not a root of any step
	// type, not a `var`, not a `capture` from an earlier step -- and not a
	// near miss of one of this step's own roots. `bse` for `base` is one, and
	// so is a callee that names no builtin.
	UnknownIdentifier Code = "unknown-identifier"

	// ReservedWord is one of token.Reserved used where a name was expected.
	// The hint names what that specific word is held for, because "reserved"
	// alone does not tell an author whether to wait for it or rename around
	// it.
	ReservedWord Code = "reserved-word"

	// InvalidRegex is a regex literal that does not compile. The message
	// carries Go's own compile error, so the author does not have to guess
	// which parenthesis. This is one of the two faults the design moves from
	// run time to compile time.
	InvalidRegex Code = "invalid-regex"

	// InvalidDuration is a string in a duration position -- `timeout`,
	// `retry`'s `delay`, an `expect`'s `within` budget -- that
	// time.ParseDuration rejects.
	InvalidDuration Code = "invalid-duration"

	// UnknownConfig is a `config` whose subject names nothing the language
	// configures. Only `browser` does today.
	UnknownConfig Code = "unknown-config"

	// UnknownType is the right-hand side of `is` when it is not one of the six
	// names in token.TypeNames.
	UnknownType Code = "unknown-type"

	// BadArity is a builtin call or a browser action given the wrong number of
	// arguments: `attr("#a")` wants two, `click "x" = 1` wants one.
	BadArity Code = "bad-arity"

	// BadValue is a value in a position whose kind is fixed by the grammar or
	// by the field's meaning: `env(42)`, `times = "3"`, `headless = "yes"`, or
	// a `header` with no name before its `=`.
	BadValue Code = "bad-value"

	// DuplicateBinding is a capture that reuses the name of a var or of an
	// earlier capture in the same scenario. The span is the capture's name;
	// the hint says where the name was bound first.
	DuplicateBinding Code = "duplicate-binding"
)

// The expander's codes: what is wrong with an import or a use.
const (
	ImportNeedsFile     Code = "import-needs-file"
	ImportNotFound      Code = "import-not-found"
	ImportCycle         Code = "import-cycle"
	DuplicateCollection Code = "duplicate-collection"
	UnknownCollection   Code = "unknown-collection"
	UnknownItem         Code = "unknown-item"
	UnknownArgument     Code = "unknown-argument"
	MissingArgument     Code = "missing-argument"
	DuplicateArgument   Code = "duplicate-argument"
	UseCycle            Code = "use-cycle"
	BadOverride         Code = "bad-override"
	UnknownFlowStep     Code = "unknown-flow-step"
	DropAfterExpect     Code = "drop-after-expect"
	SecretArgument      Code = "secret-argument"
	DroppedCapture      Code = "dropped-capture"
)

// CodeInfo is what the registry knows about a code: its default severity and
// one line a person can read. Description is documentation, not a message --
// a diagnostic's Message says what went wrong here, a description says what
// the code means in general.
type CodeInfo struct {
	Code        Code
	Severity    Severity
	Description string
}

// registry is the table. Adding a code means adding a line here; a code used
// without one fails TestEveryCodeRegistered.
var registry = []CodeInfo{
	{UnterminatedString, Error, "a string literal has no closing quote"},
	{UnterminatedRegex, Error, "a regex literal has no closing slash"},
	{UnclosedInterpolation, Error, `a "${" in a string has no closing "}"`},
	{InvalidEscape, Error, "a string literal contains an escape the language does not define"},
	{UnexpectedCharacter, Error, "a byte in the source starts no token"},
	{UnexpectedToken, Error, "a token appears where the grammar wanted another"},
	{MissingSeparator, Error, "two statements run together with no newline or comma between them"},
	{UnclosedBlock, Error, `a "{", "(" or "[" is never closed`},
	{MissingAction, Error, "a step block contains no action block, so the step has no type"},
	{DuplicateAction, Error, "a step block contains more than one action block"},
	{ActionNotFirst, Error, "a step statement appears above the step's action block"},
	{NonAssociativeOperator, Error, "a comparison operator is chained with another"},
	{NestingTooDeep, Error, "a file nests deeper than the parser will descend"},
	{UnclosedInterpolationExpr, Error, `a "${" expression is followed by neither "}" nor more string`},
	{ImportPlacement, Error, "an import appears below a collection or a scenario"},
	{ReservedParam, Error, "a parameter is named like a use-block override word, so no use can pass it"},
	{BadCollectionName, Error, "a collection's name is not an identifier, so no use can name it"},
	{UnknownField, Error, "an identifier names no field of anything in scope"},
	{NotInScope, Error, "a name is not bound in this step's type"},
	{UnknownIdentifier, Error, "a name is bound nowhere in the file"},
	{ReservedWord, Error, "a word reserved for a later tier is used as a name"},
	{InvalidRegex, Error, "a regex literal does not compile"},
	{InvalidDuration, Error, "a string in a duration position is not a duration"},
	{UnknownConfig, Error, "a \"config\" names a subject the language does not configure"},
	{UnknownType, Error, "the right-hand side of \"is\" is not a type name"},
	{BadArity, Error, "a call or a browser action has the wrong number of arguments"},
	{BadValue, Error, "a value is the wrong kind for the position it is in"},
	{DuplicateBinding, Error, "a capture reuses the name of a var or of an earlier capture"},
	{ImportNeedsFile, Error, "a file read from no disk imports another file"},
	{ImportNotFound, Error, "an imported file cannot be read"},
	{ImportCycle, Error, "a file imports itself, directly or through other files"},
	{DuplicateCollection, Error, "two collections in scope have the same name"},
	{UnknownCollection, Error, "a use names a collection that is not in scope"},
	{UnknownItem, Error, "a use names a request or flow its collection does not declare"},
	{UnknownArgument, Error, "a use passes an argument the item has no parameter for"},
	{MissingArgument, Error, "a use does not pass a parameter that has no default"},
	{DuplicateArgument, Error, "a use passes the same argument twice"},
	{UseCycle, Error, "a flow uses itself, directly or through other flows"},
	{BadOverride, Error, "a line in a use block is not an argument or an override that applies"},
	{UnknownFlowStep, Error, "an in block names a step the flow does not have"},
	{DropAfterExpect, Error, "a drop expects line comes after an expect line in the same use block"},
	{SecretArgument, Error, "an argument to a secret parameter is not itself secret"},
	{DroppedCapture, Error, "a step a use brought in reads a capture that drop captures removed"},
}

// byCode indexes the registry, built once at init so Lookup is a map read.
var byCode = func() map[Code]CodeInfo {
	m := make(map[Code]CodeInfo, len(registry))
	for _, c := range registry {
		m[c.Code] = c
	}
	return m
}()

// Codes returns every registered code, sorted by code, as the single source for
// anything that wants to list them -- `artemis grammar --json`, the SPEC, a
// UI's help index. The slice is a copy.
func Codes() []CodeInfo {
	out := make([]CodeInfo, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Lookup returns what the registry knows about a code.
func Lookup(c Code) (CodeInfo, bool) {
	info, ok := byCode[c]
	return info, ok
}

// Registered reports whether c is in the registry. A diagnostic carrying an
// unregistered code is a programming error, not a user error, which is why this
// is asserted in tests rather than checked at run time: failing to render a
// real diagnostic because its code was never registered would hide the user's
// actual problem behind ours.
func Registered(c Code) bool {
	_, ok := byCode[c]
	return ok
}

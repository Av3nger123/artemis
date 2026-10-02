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

// The checker's codes, named in docs/artemis-dsl-design.md. The stages that
// emit them land in ART-31 and ART-33; the codes are declared here because the
// registry is the contract, not the call site.
const (
	// UnknownField is an identifier that is not a field of anything in scope
	// -- the design's worked example, `expect statu == 200`. It carries a
	// did-you-mean hint and a suggestion when a near name exists.
	UnknownField Code = "unknown-field"

	// NotInScope is a name that exists in the language but not in this step's
	// type: `status` in a browser step. The hint lists the roots that are.
	NotInScope Code = "not-in-scope"
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
	{UnknownField, Error, "an identifier names no field of anything in scope"},
	{NotInScope, Error, "a name is not bound in this step's type"},
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

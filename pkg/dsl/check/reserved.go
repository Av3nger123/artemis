package check

import (
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// purposes is what each reserved word is held for.
//
// The table is the whole point of reserving them here rather than in the
// lexer. "`let` is reserved" does not tell an author whether to wait for the
// feature or rename around it; "reserved for a future local-binding form"
// does, and knowing which tier each word belongs to is the checker's
// vocabulary, not the lexer's.
//
// Every word in token.Reserved must have an entry; TestEveryReservedWordHasAPurpose
// is what stops a word being added to the language's reserved list and
// arriving here as an empty sentence.
var purposes = map[string]string{
	"if":       "reserved for a future conditional form",
	"else":     "reserved for a future conditional form",
	"for":      "reserved for a future loop form",
	"in":       "used in a use block to target one step of a flow, and reserved for a future loop form",
	"while":    "reserved for a future loop form",
	"parallel": "reserved for a future parallel-execution form",
	"group":    "reserved for a future step-grouping form",
	"fn":       "reserved for a future function form",
	"return":   "reserved for a future function form",
	"import":   "imports a collection file: import \"collections/auth.art\"",
	"use":      "expands a collection's request or flow in place: use auth.login { ... }",
	"let":      "reserved for a future local-binding form",
	"setup":    "reserved for a future setup and teardown form",
	"teardown": "reserved for a future setup and teardown form",

	// `ai` is the one word reserved for something the design explicitly
	// declines to build. See notes.
	"ai": "reserved for a possible future agentic assertion",
}

// notes are extra hint lines for a word whose purpose needs more than naming.
//
// Only `ai` has one, because it is the only word reserved for a feature the
// design argues *against*: `expect ai "the error explains the card was
// declined"` is deliberately not in scope, and an author who writes `ai` has
// guessed at the feature and deserves the answer rather than a placeholder.
var notes = map[string]string{
	"ai": "it is deliberately not in scope: a CI gate's most valuable property\n" +
		"is determinism, and an assertion that flakes is worse than a missing one",
}

// reserved reports a reserved word used as a name and returns whether it did.
//
// position is what to rename -- "var", "capture", "config subject", "field",
// "env setting", "name" -- and goes in the hint, because an author reading
// `"import" is a reserved word` next to three identifiers wants to be told
// which one.
//
// A zero or non-identifier token is not a name at all (a declaration that did
// not parse leaves one), so it is silently skipped: the parser reported it.
func (c *checker) reserved(t token.Token, position string) bool {
	if t.Kind != token.Ident || !token.IsReserved(t.Value) {
		return false
	}
	hint := purposes[t.Value] + "; rename this " + position
	if n, ok := notes[t.Value]; ok {
		hint += "\n" + n
	}
	c.bag.Error(t.Span, diag.ReservedWord, "%q is a reserved word", t.Value).Hintf("%s", hint)
	return true
}

// Package grammar is the language, described to a machine.
//
// Two documents come out of it, and they answer two different clients:
//
//   - EBNF and Document are for something that writes .art source -- a person
//     reading it once, or a model that has never seen this syntax and is given
//     the whole language in a prompt. Document is deliberately self-contained:
//     productions, the lexical tokens the productions treat as atoms, the
//     precedence facts, the rules the checker enforces after parsing, the
//     scopes, and one worked example.
//
//   - Choices is for something that builds .art source through a form. It is
//     every enumerable choice point in the language -- the dropdowns -- and
//     the point of it is that it is read out of pkg/dsl/token's tables,
//     pkg/dsl/check's specs and pkg/dsl/diag's registry at run time. Nothing
//     in this package holds a word of the language.
//
// The EBNF file is the one thing here that is not derived, because a grammar
// cannot be projected from a recursive-descent parser. So it is reconciled
// instead: ebnf_test.go extracts every quoted terminal from it and fails if a
// word in a token table is missing, or if a terminal belongs to no table.
package grammar

import (
	_ "embed"
	"strings"

	"artemis/pkg/dsl/check"
)

//go:embed grammar.ebnf
var ebnf string

//go:embed notes.txt
var notes string

//go:embed example.art
var example string

// EBNF is the production rules alone.
func EBNF() string { return ebnf }

// Document is the whole language in one piece of text: what `artemis grammar`
// prints, and what is meant to be pasted into a prompt.
//
// The scopes section is generated rather than written, because which names a
// step type binds is check's answer and a copy of it here would be a second
// answer. Everything else is the three embedded files.
func Document() string {
	var b strings.Builder
	b.WriteString("ARTEMIS SCENARIO LANGUAGE\n\n")
	b.WriteString("GRAMMAR\n\n")
	writeIndented(&b, ebnf)
	b.WriteString("\n")
	b.WriteString(notes)
	b.WriteString("\nSCOPES\n\n")
	writeIndented(&b, scopes())
	b.WriteString("\nEXAMPLE\n\n")
	writeIndented(&b, example)
	return b.String()
}

// scopes is the names each step type binds, from check -- the same answer the
// checker gives when it rejects `stdout` in an api step.
func scopes() string {
	var b strings.Builder
	for _, t := range check.StepTypes() {
		b.WriteString(t.String())
		b.WriteString(" step\n")
		roots := check.Roots(t)
		b.WriteString("  binds      ")
		b.WriteString(strings.Join(roots, ", "))
		b.WriteString("\n")
		for _, r := range roots {
			members, closed := check.Members(r)
			if !closed {
				continue
			}
			b.WriteString("  " + r + " holds  ")
			b.WriteString(strings.Join(members, ", "))
			b.WriteString("\n")
		}
		b.WriteString("  may call   ")
		b.WriteString(strings.Join(check.Functions(t), "(), ") + "()")
		b.WriteString("\n\n")
	}
	b.WriteString("A var's initialiser is in no step, so it may use env() and the vars\n")
	b.WriteString("above it and nothing else. A capture is visible to every step below the\n")
	b.WriteString("one that made it, whatever the two step types are.\n")
	return b.String()
}

// writeIndented puts two spaces in front of every non-empty line, so a
// section's body is visibly inside its heading when the whole document is
// read as one wall of text.
func writeIndented(b *strings.Builder, s string) {
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line != "" {
			b.WriteString("  ")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
}

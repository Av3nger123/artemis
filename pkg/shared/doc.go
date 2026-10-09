// Package shared is what is left of the YAML-era front end: the documentation
// tests that hold README.md and SPEC.md to the code, and nothing else.
//
// Both documents are written in the DSL, so both are compiled: every fenced
// `art` block goes through the real front end -- pkg/dsl/front, which parses,
// expands uses against SPEC.md's documented collections, and checks -- and a
// whole file has to name-check as well as parse. The
// one YAML block left is `artemis migrate`'s documented input, and it is loaded
// through pkg/shared/migrate so that it stays an input the command can read.
//
// Everything that was code moved out. The scenario loader and the YAML model
// are in pkg/shared/migrate, with `artemis migrate` as their only caller
// (ART-40); the Postman reader is in pkg/shared/postman, with `artemis
// generate` as its only caller (ART-41). The `{{}}` substituter is gone,
// because interpolation is a lexer concern in the DSL and pkg/eval owns the
// rules its renderValue had.
package shared

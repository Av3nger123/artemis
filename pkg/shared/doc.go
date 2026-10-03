// Package shared is what is left of the YAML-era front end: the documentation
// tests that hold README.md and SPEC.md to the code, and nothing else.
//
// Everything that was code moved out. The scenario loader and the YAML model
// are in pkg/shared/migrate, with `artemis migrate` as their only caller
// (ART-40); the Postman reader is in pkg/shared/postman, with `artemis
// generate` as its only caller (ART-41). The `{{}}` substituter is gone,
// because interpolation is a lexer concern in the DSL and pkg/eval owns the
// rules its renderValue had.
package shared

package codegen

import (
	"path/filepath"
	"strings"
	"unicode"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
)

// Identifiers for the JavaScript target, the mirror of names.go.
//
// The two things that happen here are the two that happen there. A *reference*
// is resolved the way eval.Env.lookup resolves it -- roots before vars, using
// the step type -- and a *binding* is renamed when its DSL name would collide
// with something the target emits. The word lists differ because the languages
// do; the rules do not, which is why this file reads like its neighbour.

// jsKeywords are JavaScript's reserved words, the ones reserved only in strict
// mode, and the two names that are not reserved and cannot be a binding anyway.
//
// A module is strict by definition, so `implements`, `package` and the rest of
// the future-reserved list are as unusable as `const` is. `arguments` and `eval`
// cannot be assigned in strict mode, which is what a var or a capture does.
var jsKeywords = words(
	"await", "break", "case", "catch", "class", "const", "continue", "debugger",
	"default", "delete", "do", "else", "enum", "export", "extends", "false",
	"finally", "for", "function", "if", "import", "in", "instanceof",
	"implements", "interface", "let", "new", "null", "package", "private",
	"protected", "public", "return", "static", "super", "switch", "this",
	"throw", "true", "try", "typeof", "var", "void", "while", "with", "yield",
	"arguments", "eval",
)

// jsEmitted are the names the JavaScript target itself puts in a test's scope:
// the step roots, the locals a step binds, the imported bindings, and the
// globals the generated body reaches for. A var or a capture with one of these
// names is renamed.
//
// The roots come from check.Roots over check.StepTypes() rather than a list, so
// a root added to the checker is a name this target renames around without a
// second edit -- the same arrangement names.go has.
var jsEmitted = func() map[string]bool {
	m := words(
		// locals a step binds beside its roots
		"resp", "proc",
		// what the module imports
		"test", "expect", "onTestFinished", "chromium", "spawnSync",
		// the globals the generated body calls
		"fetch", "process", "JSON", "Headers", "AbortSignal", "URL", "RegExp",
		"Number", "String", "Boolean", "Object", "Array", "Math", "Error",
		"TypeError", "Promise", "setTimeout", "globalThis", "undefined",
		"NaN", "Infinity", "performance",
	)
	for _, t := range check.StepTypes() {
		for _, r := range check.Roots(t) {
			m[r] = true
		}
	}
	return m
}()

// jsLocal is the JavaScript name a DSL binding -- a var or a capture -- is
// emitted under.
//
// A trailing underscore, which is what names.go does for Python: `capture class`
// becomes `class_` and reads as what it is. `$` would be the other JavaScript
// convention and would make the two targets' goldens differ for no reason.
func jsLocal(name string) string {
	if jsKeywords[name] || jsEmitted[name] || strings.HasPrefix(name, helperPrefix) {
		return name + "_"
	}
	return name
}

// jsRootsOf is the set of roots a step of the given registry key binds, keyed
// the way lower.Step carries it. See rootsOf in names.go, which is the same
// table for the same reason.
var jsRootsOf = rootsOf

// jsRootList is the roots a step of the given registry key binds, in
// check.Roots' own order rather than a map's.
//
// The order is what the single `let` line at the top of a test is written in, so
// it has to be stable: a declaration list that reordered between runs would make
// `artemis build` churn the diff, which is the one thing the header promises it
// does not do.
var jsRootList = func() map[string][]string {
	m := map[string][]string{}
	for _, t := range check.StepTypes() {
		key, ok := lower.TypeKey(t)
		if !ok {
			continue
		}
		m[key] = check.Roots(t)
	}
	return m
}()

// jsStepLocal is the extra local a step of this type binds beside its roots:
// the response object for an api step and the process result for a terminal
// one. Empty for a step type that binds none.
func jsStepLocal(key string) string {
	switch key {
	case apiKey:
		return "resp"
	case terminalKey:
		return "proc"
	}
	return ""
}

// jsReference is the JavaScript name an identifier in a step of type stepType
// refers to.
//
// Roots first, then bindings: eval.Env.lookup's order, so a scenario with `var
// status` behaves the same way under the interpreter, under the generated pytest
// and under the generated vitest. stepType is "" for an identifier outside any
// step, which is a `var`'s own value: a var sees the vars above it and no roots.
func jsReference(name, stepType string) string {
	if jsRootsOf[stepType][name] {
		return name
	}
	return jsLocal(name)
}

// jsFileName is the generated file's name for a .art file at path.
//
// <stem>.test.js, so vitest's default `include` collects it with no
// configuration -- the same reasoning that made the Python target emit
// test_<stem>.py for pytest's default discovery.
func jsFileName(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	s := jsSlug(stem)
	if s == "" {
		s = "artemis"
	}
	return s + ".test.js"
}

// jsSlug is a file-name-safe rendering of a .art file's stem.
//
// Lower-cased with every run of anything that is not a letter or a digit
// collapsed to one hyphen, because a JavaScript test file is kebab-cased where a
// Python module is snake-cased. A leading digit needs no prefix: this is a file
// name and not an identifier, which is the whole difference from slug().
func jsSlug(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			gap = false
			b.WriteRune(r)
		default:
			gap = true
		}
	}
	return b.String()
}

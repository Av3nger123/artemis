package codegen

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
)

// Identifiers, in one file, because getting a name wrong is the one codegen bug
// that produces a file which imports cleanly and then does the wrong thing.
//
// Two things happen here. A *reference* is resolved the way eval.Env.lookup
// resolves it -- roots before vars -- using the step type, so `status` inside an
// api step is the response's status and inside a terminal step is whatever `var
// status` bound. And a *binding* is renamed when its DSL name would collide with
// something the target emits, which is what makes `capture class = body.x` and
// `var status = env("S")` exportable rather than refused.

// pyKeywords are Python 3's reserved words, plus the soft keywords. A name among
// them is a syntax error where a local goes, so it is renamed.
//
// The soft keywords (`match`, `case`, `type`, `_`) are not reserved and a local
// called `match` is legal Python -- they are renamed anyway, because a local
// named `match` beside a `re.match` call is the kind of file a reader has to
// slow down for, and `match` is a name a .art file actually uses.
var pyKeywords = words(
	"False", "None", "True", "and", "as", "assert", "async", "await",
	"break", "class", "continue", "def", "del", "elif", "else", "except",
	"finally", "for", "from", "global", "if", "import", "in", "is",
	"lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try",
	"while", "with", "yield",
	"match", "case", "type", "_",
)

// emitted are the names the Python target itself puts in a test function's
// scope: the step roots, the locals a step binds, the fixture, and the modules
// the file imports. A var or a capture with one of these names is renamed.
//
// The roots are not listed by hand -- they come from check.Roots over
// check.StepTypes(), so a root added to the checker is a name this package
// renames around without a second edit. `page` is in there as the browser step's
// root.
var emitted = func() map[string]bool {
	m := words(
		// locals a step binds beside its roots
		"resp", "proc", "attempt",
		// the one python builtin a generated test body calls, for an
		// expression-valued `retry { times }` or `within`
		"int",
		// the fixture and what Playwright's own expect is imported as
		"browser", "expect", "sync_playwright",
		// the modules the generated file imports
		"os", "re", "json", "time", "requests", "subprocess", "pytest",
	)
	for _, t := range check.StepTypes() {
		for _, r := range check.Roots(t) {
			m[r] = true
		}
	}
	return m
}()

// helperPrefix is what every emitted helper's name starts with. A user name
// cannot collide with one by accident, and a reader can see at a glance which
// calls are artemis's and which are the library's.
const helperPrefix = "art_"

// local is the Python name a DSL binding -- a var or a capture -- is emitted
// under.
//
// The rename is a trailing underscore, which is PEP 8's own answer to a name
// that clashes with a keyword, so `capture class` becomes `class_` and reads as
// what it is.
func local(name string) string {
	if pyKeywords[name] || emitted[name] || strings.HasPrefix(name, helperPrefix) {
		return name + "_"
	}
	return name
}

// rootsOf is the set of roots a step of the given registry key binds, which is
// what decides whether a reference is a root or a binding.
//
// It is keyed by the registry key on lower.Step rather than by check.StepType,
// because that is what the lowered step carries; lower.TypeKey is the one
// mapping between the two and it is not reversible without this.
var rootsOf = func() map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for _, t := range check.StepTypes() {
		key, ok := lower.TypeKey(t)
		if !ok {
			continue
		}
		m[key] = words(check.Roots(t)...)
	}
	return m
}()

// reference is the Python name an identifier in a step of type stepType refers
// to.
//
// Roots first, then bindings: exactly eval.Env.lookup's order, so a scenario
// with `var status` behaves the same way under the interpreter and under the
// generated test -- the root wins inside an api step and the var wins everywhere
// else.
//
// stepType is "" for an identifier outside any step, which is a `var`'s own
// value: a var sees the vars above it and no roots at all.
func reference(name, stepType string) string {
	if rootsOf[stepType][name] {
		return name
	}
	return local(name)
}

// testName is the Python function name for a scenario called name.
//
// pytest collects a function whose name starts with `test_`, so that prefix is
// not decoration. The rest is the scenario's name slugged, which keeps the
// mapping between a line in the report and a line in the file readable: scenario
// "the order is there" becomes test_the_order_is_there.
func testName(name string) string {
	s := slug(name)
	if s == "" {
		return "test_scenario"
	}
	return "test_" + s
}

// fileName is the generated file's name for a .art file at path.
//
// test_<stem>.py, so pytest's default discovery finds it with no configuration
// -- which was the answer to Q3 in this issue's plan.
func fileName(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	s := slug(stem)
	if s == "" {
		s = "artemis"
	}
	return "test_" + s + ".py"
}

// slug is a Python-identifier-safe rendering of a scenario or file name.
//
// Lower-cased, every run of anything that is not a letter or a digit collapsed
// to one underscore, and a leading digit prefixed -- because `test_2fa` is a
// name and a bare `2fa` is not, which the `test_` prefix already provides.
// Non-ASCII letters
// are kept: Python 3 identifiers are Unicode, and transliterating a scenario
// name would be a guess.
func slug(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			gap = false
			b.WriteRune(r)
		default:
			gap = true
		}
	}
	return b.String()
}

// unique makes a name that is not already in taken, by suffixing _2, _3 and so
// on.
//
// Two scenarios named "sign in" and "sign-in" slug alike, and two test functions
// with one name would mean pytest silently running the second and never the
// first -- the one failure mode a generated suite must not have.
func unique(name string, taken map[string]bool) string {
	if !taken[name] {
		taken[name] = true
		return name
	}
	for n := 2; ; n++ {
		candidate := name + "_" + strconv.Itoa(n)
		if !taken[candidate] {
			taken[candidate] = true
			return candidate
		}
	}
}

func words(list ...string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, w := range list {
		m[w] = true
	}
	return m
}

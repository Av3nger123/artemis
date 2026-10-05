package eval

import (
	"fmt"
	"os"
	"strings"
)

// Env is what an expression can see: the step's live observation, the
// scenario's scope, the process environment, and -- in a browser step -- the
// page.
//
// It holds no step type and does no scope policing. pkg/dsl/check already
// rejected `status` in a browser step, with a diagnostic naming the roots that
// would have worked, and repeating the rule here would mean two tables that can
// disagree. What the evaluator resolves is exactly what the maps hold.
//
// An Env is read-only, so evaluating the same expression twice against the same
// Env gives the same answer. That is what `expect ... within "10s"` needs: the
// waiting loop rebuilds the observation and re-asks, and the evaluator holds
// nothing between attempts.
type Env struct {
	// Roots is the step's observation, by the name its type binds it under:
	// status, body, raw and headers for an api step; exit_code, stdout and
	// stderr for a terminal one; page for a browser one.
	Roots map[string]any

	// Vars is the scenario's vars and the captures of the steps before this
	// one, in one map because the language gives them one namespace.
	Vars map[string]any

	// Lookup resolves env("NAME"): the value, and whether the name is set at
	// all. Nil means os.LookupEnv.
	//
	// It is LookupEnv-shaped rather than Getenv-shaped because os.Getenv
	// cannot tell an absent variable from an empty one, and this package now
	// has to report both. The reason is ART-25: the empty string an unset
	// variable used to give went into a URL, and the run then reported a
	// refused connection instead of the true fault.
	Lookup func(string) (string, bool)

	// Elements is the browser page's element functions, and nil in an api or
	// terminal step -- where calling one is an errored assertion rather than a
	// panic, even though the checker rejects it first.
	Elements Elements
}

// Elements is the browser page a browser step's element functions read.
//
// ART-36 ships the dispatch, the argument handling and the error when there is
// no page; ART-45 and ART-46 ship the implementation over playwright-go. Each
// method returns the value the expression yields -- a string, a number, a
// boolean -- or an error that becomes the assertion's reason, which is how a
// selector that matches nothing reports itself.
type Elements interface {
	Text(selector string) (any, error)
	Value(selector string) (any, error)
	Attr(selector, name string) (any, error)
	Count(selector string) (any, error)
	Visible(selector string) (any, error)
}

// lookup resolves a name.
//
// Roots come before vars, which is the candidate order pkg/dsl/check's scope
// view already documents, so a capture named `body` cannot shadow an api step's
// body.
func (e *Env) lookup(name string) (any, bool) {
	if e == nil {
		return nil, false
	}
	if v, ok := e.Roots[name]; ok {
		return v, true
	}
	v, ok := e.Vars[name]
	return v, ok
}

// HasValue reports whether value counts as a value.
//
// A value of only space characters does not. A half-complete .env file --
// the key added, the value not yet pasted in -- is the usual way an empty
// value reaches a run, and `API_URL="  "` fails in the same confusing way as
// `API_URL=`. The test trims; the value itself never does.
func HasValue(value string) bool {
	return strings.TrimSpace(value) != ""
}

// Absent is the error for a variable that a scenario needs and the
// environment does not supply.
//
// One text, used by the check before the run and by a name the check could
// not read, because two texts for one fault is a defect of its own.
func Absent(name string) error {
	return fmt.Errorf("the environment variable %s has no value", name)
}

// getenv reads name. fallback is env()'s second argument when the call has
// one, and nil when it does not.
//
// It is a pointer rather than a string because the presence of the argument
// and its value are different questions: env("FLAG", "") asks for an empty
// value, and a plain string could not tell that call from env("FLAG").
//
// A name with no value and no fallback is an error rather than the empty
// string. Returning "" was this package's documented behaviour until ART-25,
// and it is what let `get "${url}/orders"` request "/orders".
func (e *Env) getenv(name string, fallback *string) (string, error) {
	lookup := os.LookupEnv
	if e != nil && e.Lookup != nil {
		lookup = e.Lookup
	}
	value, found := lookup(name)
	if found && HasValue(value) {
		return value, nil
	}
	if fallback != nil {
		return *fallback, nil
	}
	return "", Absent(name)
}

// elements is the page, or nil.
func (e *Env) elements() Elements {
	if e == nil {
		return nil
	}
	return e.Elements
}

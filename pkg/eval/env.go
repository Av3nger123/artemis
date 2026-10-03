package eval

import "os"

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

	// Getenv resolves env("NAME"). Nil means os.Getenv. An unset name is the
	// empty string, which is today's documented behaviour: an absent variable
	// is how a scenario says "no token".
	Getenv func(string) string

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

// getenv reads a process environment variable, "" when it is unset.
func (e *Env) getenv(name string) string {
	if e != nil && e.Getenv != nil {
		return e.Getenv(name)
	}
	return os.Getenv(name)
}

// elements is the page, or nil.
func (e *Env) elements() Elements {
	if e == nil {
		return nil
	}
	return e.Elements
}

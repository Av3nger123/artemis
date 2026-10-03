// Package models is the runtime step: the one shape a step has by the time
// anything executes it.
//
// It is deliberately small, and it is deliberately not a file format. A step
// reaches here one way only -- pkg/dsl/lower evaluates a checked `.art` tree
// into one of these -- so every value in it is already resolved: a URL is a
// URL and not a template, a timeout is a string pkg/dsl/check has already
// parsed once, an argument that contains a brace is an argument. Nothing
// downstream may re-interpret what it is handed.
//
// What a step *expects* is not here. In the YAML surface this struct carried
// the checks as data -- a status code, a list of body checks, a map of
// captures -- and the executor that ran the step was the only thing that could
// evaluate them. In the DSL an `expect` is an expression, held by
// pkg/dsl/lower and evaluated by pkg/eval against what pkg/executor observed,
// so the only thing a step owes an executor is what to do and how long it has
// (ART-40).
//
// The YAML scenario model that used to live here -- Config, Variable, Response,
// BodyCheck, Expect, TextCheck, Capture, and their decoders -- moved to
// pkg/shared/migrate, which is the only thing left that reads a YAML file.
package models

import (
	"fmt"
	"sort"
	"time"
)

// Step is one step of a scenario, resolved and ready to run.
//
// Which action a step reads -- Request, Exec or Browser -- is decided by Type,
// which is the registry key pkg/executor dispatches on. They sit beside each
// other rather than behind an interface because a Step is one struct for every
// step type, which is what lets the runner own retries, timeouts and the result
// tree without knowing what any of the types are.
type Step struct {
	Name string
	Type string

	// Request is the `api` step's action: one HTTP request, fully rendered.
	Request Request
	// Exec is the `terminal` step's action: one command, fully rendered.
	Exec Exec
	// Browser is the `browser` step's action: the statements to perform in the
	// scenario's page, in source order and fully rendered.
	Browser Browser

	// Retry is how many times this step may be attempted and how long to wait
	// between attempts. The runner owns the loop; this is the policy.
	Retry Retry

	// Timeout is how long one attempt of this step may take, as a Go duration
	// string ("5s", "1m30s"). It is per attempt, not per step: a step with
	// three attempts and a five-second timeout may take fifteen seconds.
	// Empty, a default applies -- see AttemptTimeout -- because no deadline at
	// all is how a run hangs until someone kills it.
	Timeout string

	// Line is the line of the source file the step was written on, which is
	// where a step that could not run at all points (ART-12).
	Line int
}

// Request is what an `api` step sends, with every value already resolved.
type Request struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
}

// AttemptTimeout is how long one attempt of the step may take, falling back to
// def when the step does not say. A duration that will not parse, or one that is
// negative, is an error: it is the scenario's mistake and silently running
// without a deadline is the one outcome worth refusing.
//
// Zero -- `timeout = "0s"` -- is also def rather than "no deadline". There is no
// spelling of "wait forever", by design.
func (s Step) AttemptTimeout(def time.Duration) (time.Duration, error) {
	if s.Timeout == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return 0, fmt.Errorf("timeout %q is not a duration (want something like \"500ms\" or \"5s\")", s.Timeout)
	}
	if d < 0 {
		return 0, fmt.Errorf("timeout %q is negative", s.Timeout)
	}
	if d == 0 {
		return def, nil
	}
	return d, nil
}

// CheckTimeout reports whether Timeout is a value an executor can use.
//
// It is what the runner asks before the first attempt, so a `timeout = "soon"`
// costs no requests to discover. The runner has no business naming a default --
// that belongs to the step type -- so the one passed here is inert: only the
// error is read.
func (s Step) CheckTimeout() error {
	_, err := s.AttemptTimeout(time.Second)
	return err
}

// Retry is how many times a step may be attempted and how long to wait between
// attempts.
//
// Times is the total number of attempts, not the number of retries after the
// first: `times = 3` sends at most three requests. An absent, zero or negative
// Times means one attempt -- a step is never attempted zero times. Delay is a
// Go duration string ("500ms", "2s", "1m30s"); absent, there is no sleep at
// all, and whatever its value nothing is slept before the first attempt or
// after the last.
type Retry struct {
	Times int
	Delay string
}

// Attempts is how many times the step may be tried: always at least one.
func (r Retry) Attempts() int {
	if r.Times < 1 {
		return 1
	}
	return r.Times
}

// Wait is how long to sleep between two attempts. An empty Delay is no wait; a
// delay that will not parse, or one that is negative, is an error.
func (r Retry) Wait() (time.Duration, error) {
	if r.Delay == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(r.Delay)
	if err != nil {
		return 0, fmt.Errorf("retry delay %q is not a duration (want something like \"500ms\" or \"2s\")", r.Delay)
	}
	if d < 0 {
		return 0, fmt.Errorf("retry delay %q is negative", r.Delay)
	}
	return d, nil
}

// sortedKeys is the keys of m, sorted. A map has no order, so sorting is what
// keeps anything derived from one -- the environment a command is given --
// the same on every run.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

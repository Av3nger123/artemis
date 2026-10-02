package lower

import (
	"fmt"
	"strings"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/shared/models"
)

// registryKeys maps the step type the checker inferred to the type string an
// executor is registered under.
//
// These are two namespaces, and the gap between them is deliberate. The checker,
// the diagnostics and `artemis grammar` call a `run` step a *terminal* step,
// which is the design document's word; pkg/steps/execstep registers itself as
// "exec", which is the spelling every YAML scenario ever written uses. Renaming
// the registration is ART-40's, paired with YAML leaving the run path. Until
// then this table is where the two meet, and it is the only place in the front
// end that knows a registry key at all.
var registryKeys = map[check.StepType]string{
	check.API:      "api",
	check.Terminal: "exec",
	check.Browser:  "browser",
}

// TypeKey is the registry key a step of type t dispatches on, and whether there
// is one. A step whose action is missing or did not parse has no key: there is
// nothing to run it with, and the parser has already said so.
func TypeKey(t check.StepType) (string, bool) {
	k, ok := registryKeys[t]
	return k, ok
}

// Step is one lowered `step`: the node the registry dispatches on.
//
// Exactly one of Request, Run and Acts is set, and which one is decided by Type,
// so a consumer switches on one thing rather than three. The expects and the
// captures are expressions evaluated against what the step observed; the action
// and the policy are expressions evaluated against the scenario's scope, which
// is what Model does.
type Step struct {
	// Name is the step's name, which is what ART-1's result tree records and
	// what the console report prints a line for.
	Name string

	// Type is the registry key: "api", "exec" or "browser". See registryKeys.
	Type string

	// Line is the line the `step` keyword sits on -- what a step that could not
	// run at all points at, and the fallback for an assertion with no line of
	// its own (ART-12).
	Line int

	// Timeout is `timeout = "5s"`, nil when the step wrote none. It is rendered
	// onto models.Step.Timeout and read by ART-16's AttemptTimeout, so the
	// default belongs to the step type and not to anything here.
	Timeout ast.Expr

	// Retry is `retry { times, delay }`, read through models.Retry's Attempts()
	// and Wait() once Model has evaluated it.
	Retry Retry

	// Request is the action of an api step, nil otherwise.
	Request *Request

	// Run is the action of a terminal step, nil otherwise.
	Run *Run

	// Acts are the actions of a browser step, nil otherwise. A browser step
	// fills no models field: no executor is registered for "browser" until
	// ART-47, and until then executor.Run reports it as an unknown step type,
	// which is an error rather than a quiet pass.
	Acts []*Act

	// Expects are the step's `expect` statements in source order, one assertion
	// each.
	Expects []*Expect

	// Captures are the step's `capture` statements in source order.
	Captures []*Capture
}

// Retry is `retry { times = 3, delay = "2s" }` with both values still
// expressions. It lowers onto models.Retry, which already owns what a retry
// means: Attempts() is at least one however the number came out, and Wait() is
// the gap between two attempts.
type Retry struct {
	Times ast.Expr
	Delay ast.Expr
	Line  int
}

// Model is the models.Step an executor.Executor takes.
//
// This is the whole of the contract between this package and pkg/executor, and
// it is one function on purpose: everything the runtime needs that models.Step
// has a field for is filled in here, and everything it does not -- the expects,
// the captures -- stays on the Step and is asked for separately.
//
// env is the scenario's scope with no roots on it: a URL, a command or a retry
// count may read a var or an earlier step's capture, and cannot read this step's
// own status. Call it once per step, before the first attempt: the policy has to
// be known before anything runs, and nothing it evaluates changes between
// attempts.
func (s *Step) Model(env *eval.Env) (models.Step, error) {
	out := models.Step{
		Name: s.Name,
		Type: s.Type,
		Line: s.Line,
	}

	timeout, err := duration(s.Timeout, env)
	if err != nil {
		return out, fmt.Errorf("step %q: timeout: %w", s.Name, err)
	}
	out.Timeout = timeout

	if out.Retry, err = s.Retry.Model(env); err != nil {
		return out, fmt.Errorf("step %q: %w", s.Name, err)
	}

	switch {
	case s.Request != nil:
		if out.Request, err = s.Request.Model(env); err != nil {
			return out, fmt.Errorf("step %q: %w", s.Name, err)
		}
	case s.Run != nil:
		if out.Exec, err = s.Run.Model(env); err != nil {
			return out, fmt.Errorf("step %q: %w", s.Name, err)
		}
	}
	// A browser step fills neither: what it does lives in Acts, and the
	// executor that reads them does not exist yet.
	return out, nil
}

// Model evaluates the retry policy.
//
// An absent `times` is one attempt and an absent `delay` is no wait, which is
// models.Retry's own reading of its zero value -- so a step with no `retry` and
// a step with `retry { times = 1 }` behave identically, as they should.
func (r Retry) Model(env *eval.Env) (models.Retry, error) {
	out := models.Retry{}

	if r.Times != nil {
		v, err := eval.Eval(r.Times, env)
		if err != nil {
			return out, fmt.Errorf("retry times: %w", err)
		}
		n, ok := wholeNumber(v)
		if !ok {
			return out, fmt.Errorf("retry times is %s, want a whole number", eval.Render(v))
		}
		out.Times = n
	}

	delay, err := duration(r.Delay, env)
	if err != nil {
		return out, fmt.Errorf("retry delay: %w", err)
	}
	out.Delay = delay

	// Wait() is asked here rather than left to the runner so that a delay that
	// came out of an expression and is not a duration stops the step before it
	// makes a request, which is what the checker does for a literal one.
	if _, err := out.Wait(); err != nil {
		return out, err
	}
	return out, nil
}

// wholeNumber reads v as a count. A float with a fraction is not one: `times =
// 2.5` is a mistake, and rounding it would be a quiet reinterpretation of what
// the scenario asked for.
func wholeNumber(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	n := int(f)
	if float64(n) != f {
		return 0, false
	}
	return n, true
}

// duration evaluates a duration position -- `timeout`, `retry`'s `delay`, an
// `expect`'s `within` -- to the string models.Step and models.Retry hold.
//
// It is kept as a string rather than parsed into a time.Duration because that is
// the field those two have, and because AttemptTimeout and Wait are the one
// place that decides what an unparseable or negative duration means. Parsing is
// still done here, so a bad duration stops the step rather than the attempt.
func duration(x ast.Expr, env *eval.Env) (string, error) {
	if x == nil {
		return "", nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a duration (want something like \"500ms\" or \"5s\")", eval.Render(v))
	}
	if _, err := time.ParseDuration(s); err != nil {
		return "", fmt.Errorf("%q is not a duration (want something like \"500ms\" or \"5s\")", s)
	}
	return s, nil
}

// step lowers one step: its type, its action, its statements.
//
// The statements are walked with ast.Children, which is the action and the
// statements in *source* order, so the expects come out in the order they were
// written whether or not they were written after the action block.
func step(d *ast.StepDecl, info *check.Info) (*Step, error) {
	// check.TypeOf, not a fourth switch on the action's Go type: the checker
	// exports it pure precisely so that the lowerer and the encoder cannot
	// disagree with it about what a step is.
	t := check.TypeOf(d.Action)
	key, ok := TypeKey(t)
	if !ok {
		// A step with no action, or one whose action did not parse. The parser
		// reported it; reaching here means the tree was not checked.
		return nil, fmt.Errorf("step %q has no action to run", d.Name.Value)
	}

	out := &Step{
		Name: d.Name.Value,
		Type: key,
		Line: d.Keyword.Span.Line,
	}

	for _, it := range ast.Children(d) {
		switch it := it.(type) {
		case *ast.Request:
			out.Request = request(it)
		case *ast.Run:
			out.Run = runAction(it)
		case *ast.Browser:
			out.Acts = acts(it)
		case *ast.Expect:
			out.Expects = append(out.Expects, expect(it, t, info))
		case *ast.Capture:
			if it.Name.Kind != token.Ident {
				continue // the parser reported a capture with no name.
			}
			out.Captures = append(out.Captures, &Capture{
				Name:  it.Name.Value,
				Value: it.Value,
				Line:  it.Name.Span.Line,
			})
		case *ast.Field:
			stepField(out, it)
		}
	}
	return out, nil
}

// stepField lowers the two statements that are fields of the step itself:
// `timeout = "5s"` and `retry { ... }`.
func stepField(out *Step, f *ast.Field) {
	if f.Name.Kind != token.Ident {
		return
	}
	switch f.Name.Value {
	case "timeout":
		out.Timeout = f.Value
	case "retry":
		out.Retry.Line = f.Name.Span.Line
		for _, rf := range fields(f.Block) {
			if rf.Name.Kind != token.Ident {
				continue
			}
			switch rf.Name.Value {
			case "times":
				out.Retry.Times = rf.Value
			case "delay":
				out.Retry.Delay = rf.Value
			}
		}
	}
	// Any other name is an unknown field the checker reported.
}

// text evaluates an expression to the string a models field holds.
//
// eval.Render is what makes it ART-6's rules unchanged: a number renders as
// written rather than as 42.000000, an object renders as compact JSON, and so a
// URL and a failure message agree about what a captured value looks like.
func text(x ast.Expr, env *eval.Env) (string, error) {
	if x == nil {
		return "", nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return "", err
	}
	return eval.Render(v), nil
}

// trimmed is text with surrounding space removed, for the positions where a
// stray space is certainly a mistake: a command's name, a header's name.
func trimmed(x ast.Expr, env *eval.Env) (string, error) {
	s, err := text(x, env)
	return strings.TrimSpace(s), err
}

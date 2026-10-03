package lower

import (
	"fmt"
	"strings"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/result"
)

// Kinds the assertions a lowered step makes are recorded under, which is what
// pkg/report prints before the path: `expect body.data.count >`, `capture token`.
const (
	// KindExpect is every assertion an `expect` makes, whatever its expression
	// does. There is no per-operator kind, because the operator is already on the
	// assertion and a second copy of it in the kind would read as a repetition.
	KindExpect = "expect"

	// KindCapture is a capture that could not be read. It is capture.Kind's
	// spelling, so a failed capture reads the same whether the scenario was
	// written in YAML or in the DSL.
	KindCapture = "capture"
)

// BrowserWithin is the default `within` budget for an assertion in a browser
// step: a page is already loading when the step's actions finish, and an
// assertion that is asked once is an assertion that is asked too early. An api or
// terminal step has no default -- its observation is complete when the step
// returns, and waiting would only make a failure slower.
const BrowserWithin = 5 * time.Second

// Expect is one `expect`, which is exactly one assertion.
//
// That is the rule this type exists to make structural rather than
// remembered. An `and` chain inside one `expect` is still one assertion -- the
// evaluator attributes it to the operand that decided it, so the reported line
// names the half that broke -- and two assertions means two `expect` lines. So
// source line to reported assertion stays one to one, which is what ART-12's
// diagnostics and the console report are built on.
type Expect struct {
	// Value is the whole expression, handed to eval.Assert in one call.
	Value ast.Expr

	// Budget is the `within` clause's expression, nil when there is none. It is
	// an expression because every value position is: `within "${t}"` parses, and
	// its text is not known until the run.
	Budget ast.Expr

	// Default is the budget to use when Budget is nil: BrowserWithin in a
	// browser step, zero -- evaluate once -- elsewhere.
	Default time.Duration

	// Class is the checker's simple/complex label, carried rather than
	// re-derived so that a UI and the runtime cannot disagree about whether a
	// form can render this assertion.
	Class check.Class

	// Line is the line the `expect` keyword sits on.
	Line int
}

// Capture is one `capture name = expr`.
//
// It adopts ART-18's semantics with none of its YAML: the value keeps the type
// the evaluator gave it, a capture that cannot be read writes nothing and
// records one errored assertion, and the captures after it are still read.
type Capture struct {
	Name  string
	Value ast.Expr

	// Line is the line the captured *name* sits on, not the expression's: the
	// name is what a scenario goes and fixes.
	Line int
}

// Within is how long this assertion may be re-evaluated for, zero meaning it is
// evaluated once.
//
// The waiting loop itself is not here: it is a loop around Assert, and it
// belongs with the browser step that needs one (ART-46). What is settled here is
// the number, because the default depends on the step's type and only the
// lowerer knows that.
func (e *Expect) Within(env *eval.Env) (time.Duration, error) {
	if e.Budget == nil {
		return e.Default, nil
	}
	s, err := duration(e.Budget, env)
	if err != nil {
		return 0, fmt.Errorf("within: %w", err)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// Unreachable: duration parsed it already. Kept so that a later change
		// to either cannot make this silently return zero, which would turn a
		// waiting assertion into a one-shot one.
		return 0, fmt.Errorf("within: %q is not a duration", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("within: %q is negative", s)
	}
	return d, nil
}

// Assert evaluates the expect and returns the one assertion it is.
//
// step is the step's name, which the result tree records; env is what the step
// observed plus the scenario's scope. One call to eval.Assert and one
// AssertionResult out of it: the one-to-one rule is the shape of this function.
func (e *Expect) Assert(step string, env *eval.Env) result.AssertionResult {
	return eval.Assert(e.Value, env).Record(step, KindExpect, e.Line)
}

// Assert evaluates every expect in the step, in source order, and returns one
// assertion each.
//
// Source order is the order they are printed in, which is the order they were
// written in. Nothing stops at the first failure: a step with a wrong status and
// a wrong body should take one run to diagnose, and the status assertion is no
// longer special -- in a .art file it is an `expect` like any other.
func (s *Step) Assert(env *eval.Env) []result.AssertionResult {
	if len(s.Expects) == 0 {
		return nil
	}
	out := make([]result.AssertionResult, 0, len(s.Expects))
	for _, e := range s.Expects {
		out = append(out, e.Assert(s.Name, env))
	}
	return out
}

// Apply reads every value the step captures and writes it into scope.
//
// This is pkg/shared/capture.Apply's contract with the expression language in
// place of a JSON path:
//
//   - the captures are read in **source order**, which is deterministic because
//     the DSL writes them as statements. ART-18 sorted its keys because
//     `capture:` was a YAML map and the order the scenario wrote was already
//     lost; sorting here would reorder what the author wrote, for nothing;
//   - the value keeps its type. The evaluator's domain is JSON's, so a number
//     stays a number and an object stays an object, and eval.Render is what
//     makes putting it back into a URL safe;
//   - a capture that succeeds records no assertion. A capture is plumbing, not a
//     check, and a run that printed a line per captured token would bury the
//     checks that matter;
//   - a capture that fails writes nothing and records exactly one errored
//     assertion naming it, and the captures after it are still read. Two
//     mistyped paths should take one run to find. Writing nothing is what makes
//     a later step fail on an unknown name rather than on a value that is
//     quietly wrong.
func (s *Step) Apply(env *eval.Env, scope executor.Scope) []result.AssertionResult {
	var out []result.AssertionResult
	for _, c := range s.Captures {
		v, err := eval.Eval(c.Value, env)
		if err != nil {
			out = append(out, c.errored(s.Name, err))
			continue
		}
		scope.Set(c.Name, v)
	}
	return out
}

// errored describes a capture that could not be read: the name in Path, because
// that is what a scenario goes and fixes, the expression it was read with as the
// operator, and the line the name sits on.
//
// There is no Expected and no Actual. A capture compared nothing -- it tried to
// read a value and there was none -- so filling either would describe an
// assertion that was never made.
func (c *Capture) errored(step string, err error) result.AssertionResult {
	return result.Assertion{
		Step:     step,
		Kind:     KindCapture,
		Path:     c.Name,
		Operator: source(c.Value),
		Line:     c.Line,
	}.Errored(err)
}

// source is an expression's source text on one line, for a failure message.
//
// ast.Source is exact -- it is every byte the node was built from, trivia
// included -- so a line break or a comment inside an expression would reach a
// report as it stands. Collapsing runs of whitespace is what makes
// `body.data.access_token` read as a path wherever the author happened to break
// the line, which is the same reason pkg/eval normalises the subject of an
// assertion.
func source(x ast.Expr) string {
	if x == nil {
		return ""
	}
	return strings.Join(strings.Fields(ast.Source(x)), " ")
}

// expect lowers one `expect`, taking its class from the checker and its default
// budget from the step's type.
func expect(e *ast.Expect, t check.StepType, info *check.Info) *Expect {
	out := &Expect{
		Value:  e.Value,
		Budget: e.Budget,
		Class:  info.Class(e),
		Line:   e.Keyword.Span.Line,
	}
	if t == check.Browser {
		out.Default = BrowserWithin
	}
	return out
}

// Package lower turns a checked .art tree into the structures pkg/executor
// consumes.
//
// It is the join between the front end and the runtime, and it is where the
// design's three mappings are made real: a `scenario` is a run unit, a `step` is
// the node the registry dispatches on, and an `expect` is exactly one assertion
// in ART-1's result tree.
//
// # What a lowered thing is
//
//	scenarios, err := lower.File(tree, info)
//
// A *Scenario holds its `config`, its vars in source order and its steps in
// source order. A *Step holds the registry key its action implies, the per-step
// policy (`retry`, `timeout`), the action itself, its expects and its captures.
//
// # Why this is not just models.Step
//
// An executor takes a models.Step and that does not change here: ART-15's seam
// and ART-16's HTTP step are downstream of the front end, and keeping their
// input shape is the whole reason this package exists rather than a second
// runtime. But a models.Step is what to *do* and nothing about what is
// expected of it, so the half of a step that is expressions lives on a *Step
// and reaches the executor through one bridge:
//
//	model, err := step.Model(env)   // the models.Step an Executor takes
//
// models.Step used to carry the expectations too -- a status code, a
// `Response.Body []BodyCheck` of operator, path and value -- because a YAML
// step had nowhere else to put them. ART-40 deleted that half along with the
// YAML front end, which is why Model now fills in only the action and the
// per-step policy.
//
// # Expressions are kept, not evaluated
//
// A URL may read a capture from an earlier step, and a capture does not exist
// until the run. So every value position is carried as an ast.Expr and evaluated
// against an *eval.Env at run time, once per step: nothing in a step's own scope
// changes between its attempts -- a capture is in scope for the steps *after*
// the one that writes it -- so evaluating a URL per attempt would give the same
// answer twice and read env() three times for a `retry { times = 3 }`.
//
// # It is handed a tree the checker accepted
//
// Lowering is not a diagnostics stage. Every name has been resolved, every
// duration parsed and every arity checked by pkg/dsl/check before this runs, so
// an error from File means the tree was not checked, or was built in Go, or that
// something here is wrong -- and it names the step it gave up on. Nothing
// panics: a nil Action, a nil Expr and an ast.Bad in any position are all
// reported, because the parser recovers and a caller may hand on a half-built
// tree.
package lower

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/taint"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
)

// Scenario is one lowered `scenario`: the run unit ART-1's result tree reports
// a ScenarioResult for.
type Scenario struct {
	// Name is the scenario's name, which is what the console report prints and
	// what a JUnit testsuite is named after.
	Name string

	// Line is the line the `scenario` keyword sits on.
	Line int

	// Browser is the scenario's `config browser`, or nil when it wrote none.
	Browser *BrowserConfig

	// Vars are the scenario's `var` declarations in source order. The order is
	// load-bearing: a var sees the vars above it and no others, which is only
	// meaningful if they are bound in reading order.
	Vars []*Var

	// Steps are the scenario's steps in source order.
	Steps []*Step
}

// Var is one `var name = expr`.
type Var struct {
	Name  string
	Value ast.Expr
	Line  int
}

// BrowserConfig is `config browser { headless = true, viewport = "1280x720" }`.
//
// Both settings are expressions, because every value position in the language
// is one: `headless = env("HEADLESS") == "1"` needs no grammar change. They are
// nil when the block did not write them, which is how "the scenario said
// nothing" stays different from "the scenario said false".
type BrowserConfig struct {
	Headless ast.Expr
	Viewport ast.Expr
	Line     int
}

// Browser is the resolved form of a BrowserConfig: what pkg/session (ART-45) is
// handed when it opens a scenario's browser.
type Browser struct {
	// Headless is whether the browser runs with no window. The default is true:
	// a scenario that says nothing is a scenario running in CI.
	Headless bool

	// Viewport is the window size as the scenario wrote it ("1280x720"), empty
	// when it wrote none. It is not parsed here -- what a viewport string means
	// belongs to whatever opens the window.
	Viewport string
}

// Resolve evaluates the config against env.
//
// A missing setting keeps its default rather than erroring, and a setting that
// evaluated to the wrong kind of value is an error naming it: the checker
// already rejected `headless = 3`, so reaching this means the value came out of
// an expression and only the run could have known.
func (c *BrowserConfig) Resolve(env *eval.Env) (Browser, error) {
	out := Browser{Headless: true}
	if c == nil {
		return out, nil
	}
	if c.Headless != nil {
		v, err := eval.Eval(c.Headless, env)
		if err != nil {
			return out, fmt.Errorf("config browser headless: %w", err)
		}
		b, ok := v.(bool)
		if !ok {
			return out, fmt.Errorf("config browser headless is %s, want true or false", eval.Render(v))
		}
		out.Headless = b
	}
	if c.Viewport != nil {
		v, err := eval.Eval(c.Viewport, env)
		if err != nil {
			return out, fmt.Errorf("config browser viewport: %w", err)
		}
		out.Viewport = eval.Render(v)
	}
	return out, nil
}

// Bind evaluates the scenario's vars into scope, in source order.
//
// This is the run-time half of the checker's ordering rule: each var is
// evaluated against the vars already bound, so `var b = a` sees `a` only when
// `a` is above it, and the first var that cannot be evaluated stops the
// scenario. A scenario whose variables do not resolve has nothing worth running.
func (s *Scenario) Bind(scope executor.Scope) error {
	if s == nil {
		return nil
	}
	env := &eval.Env{Vars: scope.Vars()}
	for _, v := range s.Vars {
		val, err := eval.Eval(v.Value, env)
		if err != nil {
			return fmt.Errorf("var %s: %w", v.Name, err)
		}
		scope.Set(v.Name, val)
	}
	return nil
}

// File lowers every scenario in tree, in source order.
//
// info is the checker's, and the one thing read off it is each expect's
// simple/complex class, which is carried rather than re-derived so that no
// second client can disagree about it. A step's *type* is check.TypeOf, which is
// exported and pure for exactly this reason. A nil Info is accepted and gives
// the conservative class, so a caller holding a tree it built itself gets a
// lowered scenario rather than a panic.
//
// A top-level declaration that is not a scenario is skipped: the parser puts an
// ast.Bad there for a line that did not parse and has already reported it.
func File(tree *ast.File, info *check.Info) ([]*Scenario, error) {
	if tree == nil {
		return nil, nil
	}
	out := make([]*Scenario, 0, len(tree.Scenarios))
	for _, d := range tree.Scenarios {
		sc, ok := d.(*ast.Scenario)
		if !ok || sc == nil {
			continue
		}
		lowered, err := scenario(sc, info)
		if err != nil {
			return nil, err
		}
		out = append(out, lowered)
	}
	return out, nil
}

// scenario lowers one scenario's body in source order.
//
// The source order is what makes the secret set correct. A `secret var` is in
// scope for every step, so all of them are collected before the first step is
// lowered. A secret `capture` is in scope for the steps *after* its own, as
// SPEC.md's scoping rule says, so it joins the set only once its step is done --
// which is why the set is built here and not in a pass of its own.
func scenario(sc *ast.Scenario, info *check.Info) (*Scenario, error) {
	out := &Scenario{
		Name: sc.Name.Value,
		Line: sc.Keyword.Span.Line,
	}
	secrets := secretVars(sc)
	for _, d := range sc.Body {
		switch d := d.(type) {
		case *ast.ConfigDecl:
			cfg, err := config(d)
			if err != nil {
				return nil, err
			}
			if cfg != nil {
				// A second `config browser` replaces the first. The checker has
				// no rule against writing two, and the last one written is the
				// one a reader of the file would expect to apply.
				out.Browser = cfg
			}
		case *ast.VarDecl:
			if d.Name.Kind != token.Ident {
				continue // the parser reported a var with no name.
			}
			out.Vars = append(out.Vars, &Var{
				Name:  d.Name.Value,
				Value: d.Value,
				Line:  d.Name.Span.Line,
			})
		case *ast.StepDecl:
			st, err := step(d, info, secrets)
			if err != nil {
				return nil, fmt.Errorf("scenario %q: %w", out.Name, err)
			}
			st.relativeTo(sc.Keyword.Span.File)
			out.Steps = append(out.Steps, st)
			// After the step, never before it: a capture is not in scope for
			// the step that writes it.
			addSecretCaptures(secrets, st)
		}
		// *ast.Bad and anything else: already reported by the parser.
	}
	return out, nil
}

// secretVars is the names of the scenario's `secret var` declarations.
//
// Every one of them is in scope for every step, so this runs before the first
// step is lowered. A var with no name was reported by the parser and is skipped,
// as the var lowering below skips it.
func secretVars(sc *ast.Scenario) map[string]bool {
	out := map[string]bool{}
	for _, d := range sc.Body {
		v, ok := d.(*ast.VarDecl)
		if !ok || v.Secret.Text == "" || v.Name.Kind != token.Ident {
			continue
		}
		out[v.Name.Value] = true
	}
	return out
}

// addSecretCaptures adds the names st captured secretly, for the steps below it.
//
// A capture is secret two ways, and both are needed. The author wrote `secret
// capture`, which is Declared. Or the captured expression itself read a secret
// binding -- `capture part = match(pw, /x(.)/)` -- which taint.Secret reports,
// and which has to count: a value derived from a credential is still the
// credential's to leak.
func addSecretCaptures(secrets map[string]bool, st *Step) {
	for _, c := range st.Captures {
		if c.Declared || taint.Secret(c.Value, secrets) {
			secrets[c.Name] = true
		}
	}
}

// config lowers `config browser { ... }`. A subject that is not `browser` is
// nil: the checker reported it, and there is nothing else to configure.
func config(d *ast.ConfigDecl) (*BrowserConfig, error) {
	if d.Subject.Kind != token.Ident || d.Subject.Value != "browser" {
		return nil, nil
	}
	out := &BrowserConfig{Line: d.Keyword.Span.Line}
	for _, f := range fields(d.Block) {
		if f.Name.Kind != token.Ident {
			continue
		}
		switch f.Name.Value {
		case "headless":
			out.Headless = f.Value
		case "viewport":
			out.Viewport = f.Value
		}
		// Any other name is an unknown field the checker reported.
	}
	return out, nil
}

// fields is the *ast.Field items of a block, skipping the ast.Bad a line that
// did not parse leaves behind. Every block in the grammar is read through this,
// so "a bad line costs only itself" is written once.
func fields(b *ast.Block) []*ast.Field {
	if b == nil {
		return nil
	}
	out := make([]*ast.Field, 0, len(b.Fields))
	for _, s := range b.Fields {
		if f, ok := s.(*ast.Field); ok && f != nil {
			out = append(out, f)
		}
	}
	return out
}

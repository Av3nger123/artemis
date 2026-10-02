package executor

import (
	"context"
	"errors"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// Func is the adapter every test in this package builds its fake executors
// with, so it had better satisfy the interface.
var _ Executor = Func(nil)

// recorder is an executor that reports what it was handed, for the dispatch
// tests.
type recorder struct {
	step  models.Step
	scope Scope
	calls int
}

func (r *recorder) Execute(_ context.Context, step models.Step, scope Scope) (*result.StepResult, error) {
	r.step, r.scope, r.calls = step, scope, r.calls+1
	res := &result.StepResult{}
	res.Assert(result.Assertion{Kind: "recorded", Operator: "equals"}.Pass())
	return res, nil
}

var _ Executor = (*recorder)(nil)

func TestFuncIsAnExecutor(t *testing.T) {
	var got models.Step
	e := Func(func(_ context.Context, step models.Step, _ Scope) (*result.StepResult, error) {
		got = step
		return &result.StepResult{}, nil
	})

	if _, err := e.Execute(context.Background(), models.Step{Name: "login"}, NewScope()); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got.Name != "login" {
		t.Errorf("executor saw step %q, want %q", got.Name, "login")
	}
}

// What the executor recorded is what the caller gets back: the runner copies
// these assertions onto the tree, so nothing may be lost in between.
func TestExecutorAssertionsComeBack(t *testing.T) {
	e := Func(func(context.Context, models.Step, Scope) (*result.StepResult, error) {
		res := &result.StepResult{}
		res.Assert(result.Assertion{Step: "login", Kind: "status_code", Operator: "equals", Expected: 200, Actual: 200}.Pass())
		res.Assert(result.Assertion{Step: "login", Kind: "body", Path: "$.id", Operator: "exists"}.Fail())
		return res, nil
	})

	res, err := e.Execute(context.Background(), models.Step{Name: "login"}, NewScope())
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if len(res.Assertions) != 2 {
		t.Fatalf("got %d assertions, want 2", len(res.Assertions))
	}
	if res.Assertions[0].Status != result.StatusPass || res.Assertions[1].Status != result.StatusFail {
		t.Errorf("statuses = %v, %v; want pass, fail", res.Assertions[0].Status, res.Assertions[1].Status)
	}
}

// An executor writes a capture into the scope it was handed, and the caller --
// which is to say the next step -- sees it.
func TestExecutorCapturesIntoScope(t *testing.T) {
	e := Func(func(_ context.Context, _ models.Step, scope Scope) (*result.StepResult, error) {
		scope.Set("token", "abc")
		return &result.StepResult{}, nil
	})

	scope := NewScope()
	if _, err := e.Execute(context.Background(), models.Step{Name: "login"}, scope); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got, ok := scope.Get("token"); !ok || got != "abc" {
		t.Errorf("scope[token] = %v, %v; want \"abc\", true", got, ok)
	}
}

// "Could not run" is an error; the result may be nil with it, and the runner
// must not need one.
func TestExecutorMayErrorWithNoResult(t *testing.T) {
	want := errors.New("connection refused")
	e := Func(func(context.Context, models.Step, Scope) (*result.StepResult, error) {
		return nil, want
	})

	res, err := e.Execute(context.Background(), models.Step{Name: "login"}, NewScope())
	if !errors.Is(err, want) {
		t.Errorf("Execute() error = %v, want %v", err, want)
	}
	if res != nil {
		t.Errorf("Execute() result = %#v, want nil alongside the error", res)
	}
}

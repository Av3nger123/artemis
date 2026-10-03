package executor

import (
	"context"
	"errors"
	"testing"

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

func (r *recorder) Observe(_ context.Context, step models.Step, scope Scope) (map[string]any, error) {
	r.step, r.scope, r.calls = step, scope, r.calls+1
	return map[string]any{"recorded": true}, nil
}

var _ Executor = (*recorder)(nil)

func TestFuncIsAnExecutor(t *testing.T) {
	var got models.Step
	e := Func(func(_ context.Context, step models.Step, _ Scope) (map[string]any, error) {
		got = step
		return nil, nil
	})

	if _, err := e.Observe(context.Background(), models.Step{Name: "login"}, NewScope()); err != nil {
		t.Fatalf("Observe() error = %v, want nil", err)
	}
	if got.Name != "login" {
		t.Errorf("executor saw step %q, want %q", got.Name, "login")
	}
}

// What the executor observed is what the caller gets back: the runner
// evaluates the step's expects against this map, so nothing may be lost in
// between.
func TestExecutorRootsComeBack(t *testing.T) {
	e := Func(func(context.Context, models.Step, Scope) (map[string]any, error) {
		return map[string]any{"status": float64(200), "raw": "ok"}, nil
	})

	roots, err := e.Observe(context.Background(), models.Step{Name: "login"}, NewScope())
	if err != nil {
		t.Fatalf("Observe() error = %v, want nil", err)
	}
	if roots["status"] != float64(200) || roots["raw"] != "ok" {
		t.Errorf("roots = %v, want status 200 and raw \"ok\"", roots)
	}
}

// The scope is handed in rather than returned, so an executor that needs to
// read a variable -- a browser step reaching for its session name -- can.
func TestExecutorSeesTheScope(t *testing.T) {
	var got any
	e := Func(func(_ context.Context, _ models.Step, scope Scope) (map[string]any, error) {
		got, _ = scope.Get("token")
		return nil, nil
	})

	scope := NewScope()
	scope.Set("token", "abc")
	if _, err := e.Observe(context.Background(), models.Step{Name: "login"}, scope); err != nil {
		t.Fatalf("Observe() error = %v, want nil", err)
	}
	if got != "abc" {
		t.Errorf("executor read token = %v, want \"abc\"", got)
	}
}

// "Could not run" is an error; the roots may be nil with it, and the runner
// must not need them.
func TestExecutorMayErrorWithNoRoots(t *testing.T) {
	want := errors.New("connection refused")
	e := Func(func(context.Context, models.Step, Scope) (map[string]any, error) {
		return nil, want
	})

	roots, err := e.Observe(context.Background(), models.Step{Name: "login"}, NewScope())
	if !errors.Is(err, want) {
		t.Errorf("Observe() error = %v, want %v", err, want)
	}
	if roots != nil {
		t.Errorf("Observe() roots = %#v, want nil alongside the error", roots)
	}
}

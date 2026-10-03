package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// observer is an executor that can be observed, for the dispatch tests.
type observer struct {
	roots map[string]any
	step  models.Step
	err   error
}

func (o *observer) Execute(context.Context, models.Step, Scope) (*result.StepResult, error) {
	return &result.StepResult{}, nil
}

func (o *observer) Observe(_ context.Context, step models.Step, _ Scope) (map[string]any, error) {
	o.step = step
	return o.roots, o.err
}

var (
	_ Executor = (*observer)(nil)
	_ Observer = (*observer)(nil)
)

func TestObserveDispatchesOnStepType(t *testing.T) {
	api := &observer{roots: map[string]any{"status": float64(200)}}
	reg := NewRegistry()
	reg.Register("api", api)

	got, err := Observe(context.Background(), reg, models.Step{Name: "ping", Type: "api"}, NewScope())
	if err != nil {
		t.Fatalf("Observe() error = %v, want nil", err)
	}
	if api.step.Name != "ping" {
		t.Errorf("observer saw step %q, want %q", api.step.Name, "ping")
	}
	if got["status"] != float64(200) {
		t.Errorf("roots[status] = %v, want 200", got["status"])
	}
}

// A type nothing is registered for reports it the way Run does, so the same
// mistake reads the same whichever way the step was dispatched.
func TestObserveUnknownStepType(t *testing.T) {
	reg := NewRegistry()
	reg.Register("api", &observer{})

	_, err := Observe(context.Background(), reg, models.Step{Type: "graphql"}, NewScope())
	if !errors.Is(err, ErrUnknownStepType) {
		t.Fatalf("Observe() error = %v, want ErrUnknownStepType", err)
	}
	if !strings.Contains(err.Error(), "known types: api") {
		t.Errorf("error = %q, want it to name the types that do exist", err)
	}
}

// A registered executor with no Observe is an error naming the types that can
// be observed -- which is how "browser" reports itself until ART-47, rather
// than quietly passing.
func TestObserveExecutorThatCannotBeObserved(t *testing.T) {
	reg := NewRegistry()
	reg.Register("api", &observer{})
	reg.Register("browser", Func(func(context.Context, models.Step, Scope) (*result.StepResult, error) {
		return &result.StepResult{}, nil
	}))

	_, err := Observe(context.Background(), reg, models.Step{Type: "browser"}, NewScope())
	if !errors.Is(err, ErrNoObservation) {
		t.Fatalf("Observe() error = %v, want ErrNoObservation", err)
	}
	if !strings.Contains(err.Error(), `"browser"`) || !strings.Contains(err.Error(), "these can: api") {
		t.Errorf("error = %q, want it to name the type and the ones that can", err)
	}
}

func TestObserveNoObservableTypesAtAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register("browser", Func(func(context.Context, models.Step, Scope) (*result.StepResult, error) {
		return &result.StepResult{}, nil
	}))

	_, err := Observe(context.Background(), reg, models.Step{Type: "browser"}, NewScope())
	if !errors.Is(err, ErrNoObservation) {
		t.Fatalf("Observe() error = %v, want ErrNoObservation", err)
	}
	if !strings.Contains(err.Error(), "no step type can") {
		t.Errorf("error = %q, want it to say no type can be observed", err)
	}
}

// An error from the executor comes back as it is: a step that could not run is
// the runner's to fail, not this dispatch's to interpret.
func TestObserveErrorComesBack(t *testing.T) {
	want := errors.New("connection refused")
	reg := NewRegistry()
	reg.Register("api", &observer{err: want})

	roots, err := Observe(context.Background(), reg, models.Step{Type: "api"}, NewScope())
	if !errors.Is(err, want) {
		t.Errorf("Observe() error = %v, want %v", err, want)
	}
	if roots != nil {
		t.Errorf("Observe() roots = %v, want nil alongside the error", roots)
	}
}

func TestObservableIsSortedAndOnlyTheObservable(t *testing.T) {
	reg := NewRegistry()
	reg.Register("exec", &observer{})
	reg.Register("api", &observer{})
	reg.Register("browser", Func(func(context.Context, models.Step, Scope) (*result.StepResult, error) {
		return nil, nil
	}))

	got := reg.Observable()
	want := []string{"api", "exec"}
	if len(got) != len(want) {
		t.Fatalf("Observable() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Observable() = %v, want %v", got, want)
		}
	}
}

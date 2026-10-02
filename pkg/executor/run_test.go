package executor

import (
	"errors"
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// Dispatch is on Step.Type, and each type goes to its own executor and no other.
func TestRunDispatchesOnStepType(t *testing.T) {
	http, exec := &recorder{}, &recorder{}
	reg := NewRegistry()
	reg.Register("http", http)
	reg.Register("exec", exec)

	step := models.Step{Name: "login", Type: "http"}
	scope := NewScope()
	scope.Set("base", "http://localhost")

	res, err := Run(reg, step, scope)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if http.calls != 1 || exec.calls != 0 {
		t.Fatalf("calls: http=%d exec=%d; want 1 and 0", http.calls, exec.calls)
	}
	if http.step.Name != "login" {
		t.Errorf("executor saw step %q, want %q", http.step.Name, "login")
	}
	if got, _ := http.scope.Get("base"); got != "http://localhost" {
		t.Errorf("executor saw scope[base] = %v, want the scope it was called with", got)
	}
	if len(res.Assertions) != 1 || res.Assertions[0].Kind != "recorded" {
		t.Errorf("result = %#v, want the executor's own assertions", res.Assertions)
	}
}

// ART-7: a type nothing can execute is an error, never a pass and never a skip.
func TestRunRejectsAnUnregisteredType(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", noop())
	reg.Register("exec", noop())

	res, err := Run(reg, models.Step{Name: "query", Type: "db"}, NewScope())
	if err == nil {
		t.Fatal("Run() = nil error for a step type nothing is registered for")
	}
	if !errors.Is(err, ErrUnknownStepType) {
		t.Errorf("errors.Is(err, ErrUnknownStepType) = false for %v", err)
	}
	if res != nil {
		t.Errorf("Run() result = %#v, want nil", res)
	}
	// The bad type, and the way out of it.
	for _, want := range []string{`"db"`, "known types: exec, http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// A step with no type at all is unknown, not a match on the empty string.
func TestRunRejectsAnEmptyType(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", noop())

	_, err := Run(reg, models.Step{Name: "nameless"}, NewScope())
	if !errors.Is(err, ErrUnknownStepType) {
		t.Fatalf("Run() error = %v, want ErrUnknownStepType", err)
	}
}

func TestRunRejectsATypeThatDiffersOnlyInCase(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", noop())

	_, err := Run(reg, models.Step{Name: "login", Type: "HTTP"}, NewScope())
	if !errors.Is(err, ErrUnknownStepType) {
		t.Fatalf("Run() error = %v, want ErrUnknownStepType for a near-match", err)
	}
}

// The message has to survive an empty registry -- which is what the default one
// is until ART-16 -- without claiming the known types are "".
func TestRunOnAnEmptyRegistrySaysSo(t *testing.T) {
	_, err := Run(NewRegistry(), models.Step{Name: "login", Type: "http"}, NewScope())
	if !errors.Is(err, ErrUnknownStepType) {
		t.Fatalf("Run() error = %v, want ErrUnknownStepType", err)
	}
	if !strings.Contains(err.Error(), "no step types are registered") {
		t.Errorf("error = %q, want it to say nothing is registered", err)
	}
}

// An executor's "could not run" error reaches the runner as it stands, so the
// reason the step failed is the executor's own and not a wrapper's.
func TestRunPassesTheExecutorsErrorThrough(t *testing.T) {
	want := errors.New("connection refused")
	reg := NewRegistry()
	reg.Register("http", Func(func(_ models.Step, _ Scope) (*result.StepResult, error) {
		return nil, want
	}))

	_, err := Run(reg, models.Step{Name: "login", Type: "http"}, NewScope())
	if !errors.Is(err, want) {
		t.Errorf("Run() error = %v, want %v", err, want)
	}
	if errors.Is(err, ErrUnknownStepType) {
		t.Error("an executor's error was reported as an unknown step type")
	}
}

// Run calls the executor once. Retry is the runner's, not the dispatch's.
func TestRunMakesOneAttempt(t *testing.T) {
	http := &recorder{}
	reg := NewRegistry()
	reg.Register("http", http)

	step := models.Step{Name: "login", Type: "http", Retry: models.Retry{Times: 5}}
	if _, err := Run(reg, step, NewScope()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if http.calls != 1 {
		t.Errorf("executor called %d times, want 1: Run makes one attempt", http.calls)
	}
}

// Captures land in the caller's scope, which is how one step's token reaches the
// next step.
func TestRunCapturesReachTheCaller(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", Func(func(_ models.Step, scope Scope) (*result.StepResult, error) {
		scope.Set("token", "abc")
		return &result.StepResult{}, nil
	}))

	scope := NewScope()
	if _, err := Run(reg, models.Step{Name: "login", Type: "http"}, scope); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got, _ := scope.Get("token"); got != "abc" {
		t.Errorf("scope[token] = %v, want \"abc\"", got)
	}
}

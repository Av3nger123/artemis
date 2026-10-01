package models

import (
	"strings"
	"testing"
)

func apiStep(name string) Step {
	return Step{Name: name, Type: "api"}
}

func TestValidateAcceptsKnownStepTypes(t *testing.T) {
	config := Config{Name: "orders", Steps: []Step{apiStep("login"), apiStep("fetch")}}

	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// A scenario with nothing in it is not what this issue is about: an empty
// steps list is a run with nothing to get wrong, and ART-2 decides what that
// exit code is worth.
func TestValidateAcceptsAScenarioWithNoSteps(t *testing.T) {
	if err := (Config{Name: "empty"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsUnknownStepType(t *testing.T) {
	config := Config{Steps: []Step{apiStep("login"), {Name: "query", Type: "db"}}}

	err := config.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for a step type nothing executes")
	}
	// Position, name, the bad type and the way out of it.
	for _, want := range []string{"step 2", `"query"`, `"db"`, "known types: api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestValidateRejectsAStepWithNoType(t *testing.T) {
	err := Config{Steps: []Step{{Name: "ping"}}}.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for a step with no type")
	}
	if !strings.Contains(err.Error(), "missing type") {
		t.Errorf("error = %q, want it to say the type is missing", err)
	}
}

// The match is exact, so the set of valid spellings equals the set of
// documented ones.
func TestValidateRejectsAMisCasedStepType(t *testing.T) {
	if err := (Config{Steps: []Step{{Name: "ping", Type: "API"}}}).Validate(); err == nil {
		t.Fatal(`Validate() = nil, want an error: "API" is not "api"`)
	}
}

// One run should be enough to see everything that needs fixing.
func TestValidateReportsEveryBadStep(t *testing.T) {
	config := Config{Steps: []Step{
		{Name: "a", Type: "db"},
		apiStep("b"),
		{Name: "c", Type: "exec"},
		{Name: "d"},
	}}

	err := config.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want errors")
	}
	for _, want := range []string{"step 1", "step 3", "step 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to report %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "step 2") {
		t.Errorf("error = %q, want it to leave the good step alone", err)
	}
}

func TestIsKnownStepType(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		want bool
	}{{"api", true}, {"", false}, {"API", false}, {"db", false}, {" api", false}} {
		if got := IsKnownStepType(tc.typ); got != tc.want {
			t.Errorf("IsKnownStepType(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

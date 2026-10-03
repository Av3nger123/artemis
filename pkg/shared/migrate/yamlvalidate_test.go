package migrate

import (
	"strings"
	"testing"
)

func namedAPIStep(name string) Step {
	return Step{Name: name, Type: "api"}
}

// known is what the registry hands Validate in a real run: today, one type.
var known = []string{"api"}

func TestValidateAcceptsKnownStepTypes(t *testing.T) {
	config := Config{Name: "orders", Steps: []Step{namedAPIStep("login"), namedAPIStep("fetch")}}

	if err := config.Validate(known); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// A scenario with nothing in it is not what this issue is about: an empty
// steps list is a run with nothing to get wrong, and ART-2 decides what that
// exit code is worth.
func TestValidateAcceptsAScenarioWithNoSteps(t *testing.T) {
	if err := (Config{Name: "empty"}).Validate(known); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsUnknownStepType(t *testing.T) {
	config := Config{Steps: []Step{namedAPIStep("login"), {Name: "query", Type: "db"}}}

	err := config.Validate(known)
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
	err := Config{Steps: []Step{{Name: "ping"}}}.Validate(known)
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
	if err := (Config{Steps: []Step{{Name: "ping", Type: "API"}}}).Validate(known); err == nil {
		t.Fatal(`Validate() = nil, want an error: "API" is not "api"`)
	}
}

// One run should be enough to see everything that needs fixing.
func TestValidateReportsEveryBadStep(t *testing.T) {
	config := Config{Steps: []Step{
		{Name: "a", Type: "db"},
		namedAPIStep("b"),
		{Name: "c", Type: "exec"},
		{Name: "d"},
	}}

	err := config.Validate(known)
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

// Validate reports against the list it was given, not a list of its own: this
// is what makes the registry the single place a step type is declared.
func TestValidateAcceptsWhateverTheRegistryRegistered(t *testing.T) {
	config := Config{Steps: []Step{{Name: "run", Type: "exec"}}}

	if err := config.Validate([]string{"api", "exec"}); err != nil {
		t.Fatalf("Validate() = %v, want nil once exec is registered", err)
	}
	if err := config.Validate([]string{"api"}); err == nil {
		t.Fatal("Validate() = nil, want an error while exec is not registered")
	}
}

// The spelling is exact in both directions.
func TestValidateMatchesTypesExactly(t *testing.T) {
	for _, typ := range []string{"API", " api", "api ", ""} {
		if err := (Config{Steps: []Step{{Name: "ping", Type: typ}}}).Validate(known); err == nil {
			t.Errorf("Validate() = nil for type %q, want an error", typ)
		}
	}
}

// An empty list is a wiring mistake, not a scenario mistake, but a scenario must
// still not pass validation against it -- and the message must not read
// "known types: ".
func TestValidateWithNothingRegisteredSaysSo(t *testing.T) {
	err := Config{Steps: []Step{namedAPIStep("login")}}.Validate(nil)
	if err == nil {
		t.Fatal("Validate(nil) = nil, want an error -- nothing can be executed")
	}
	if !strings.Contains(err.Error(), "none are registered") {
		t.Errorf("error = %q, want it to say nothing is registered", err)
	}
}

func TestValidateRejectsACaptureWithNoName(t *testing.T) {
	c := Config{Steps: []Step{{
		Name:    "login",
		Type:    "api",
		Capture: map[string]Capture{"": {JSON: "$.token"}},
	}}}
	err := c.Validate([]string{"api"})
	if err == nil {
		t.Fatal("Validate() = nil, want an error for a capture with no name")
	}
	if !strings.Contains(err.Error(), "has no name") {
		t.Errorf("Validate() = %q, want it to say the capture has no name", err)
	}
}

// `token:` with nothing after it is a null node, and yaml.v3 never calls an
// UnmarshalYAML method for one -- so the only place that mistake can be caught
// is here.
func TestValidateRejectsACaptureWithNoSource(t *testing.T) {
	c := Config{Steps: []Step{{
		Name:    "login",
		Type:    "api",
		Capture: map[string]Capture{"token": {}},
	}}}
	err := c.Validate([]string{"api"})
	if err == nil {
		t.Fatal("Validate() = nil, want an error for a capture with no source")
	}
	if !strings.Contains(err.Error(), `capture "token"`) {
		t.Errorf("Validate() = %q, want it to name the capture", err)
	}
}

func TestValidateReportsEveryBadCaptureInSortedOrder(t *testing.T) {
	c := Config{Steps: []Step{{
		Name: "login",
		Type: "api",
		Capture: map[string]Capture{
			"zebra": {},
			"apple": {},
			"ok":    {JSON: "$.fine"},
		},
	}}}
	err := c.Validate([]string{"api"})
	if err == nil {
		t.Fatal("Validate() = nil, want errors")
	}
	got := err.Error()
	apple, zebra := strings.Index(got, "apple"), strings.Index(got, "zebra")
	if apple < 0 || zebra < 0 {
		t.Fatalf("Validate() = %q, want both bad captures in it", got)
	}
	if apple > zebra {
		t.Errorf("Validate() = %q, want the captures in sorted order", got)
	}
	if strings.Contains(got, "$.fine") || strings.Contains(got, `"ok"`) {
		t.Errorf("Validate() = %q, want the good capture left out", got)
	}
}

func TestValidateAcceptsAStepWithGoodCaptures(t *testing.T) {
	c := Config{Steps: []Step{{
		Name: "login",
		Type: "api",
		Capture: map[string]Capture{
			"token": {JSON: "$.token"},
			"id":    {Regex: "id=([0-9]+)"},
		},
	}}}
	if err := c.Validate([]string{"api"}); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCaptureKeysAreSorted(t *testing.T) {
	s := Step{Capture: map[string]Capture{"c": {}, "a": {}, "b": {}}}
	got := s.CaptureKeys()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("CaptureKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CaptureKeys() = %v, want %v", got, want)
		}
	}
	if (Step{}).CaptureKeys() != nil {
		t.Error("CaptureKeys() on a step with no captures is not nil")
	}
}

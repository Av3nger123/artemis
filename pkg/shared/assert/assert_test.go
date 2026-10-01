package assert

import (
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"

	"gopkg.in/yaml.v2"
)

// body is the response every engine test asserts against.
const body = `{
  "status": "ok",
  "code": 200,
  "ratio": 1.5,
  "active": true,
  "missingValue": null,
  "message": "user created successfully",
  "items": [{"name": "a"}, {"name": "b"}],
  "empty": [],
  "user": {"id": 7, "email": "a@b.com"}
}`

// check runs one body check against the fixture.
func check(t *testing.T, c models.BodyCheck) result.AssertionResult {
	t.Helper()
	return Check("step", c, jsonOf(t, body))
}

// wantStatus asserts the outcome and, for an error, that the reason mentions
// what the reader needs to find the problem.
func wantStatus(t *testing.T, got result.AssertionResult, want result.Status, reasonContains ...string) {
	t.Helper()
	if got.Status != want {
		t.Fatalf("status = %q (error %q), want %q -- %s", got.Status, got.Error, want, got.Describe())
	}
	for _, s := range reasonContains {
		if !strings.Contains(got.Error, s) {
			t.Errorf("error = %q, want it to mention %q", got.Error, s)
		}
	}
	if want == result.StatusError && got.Error == "" {
		t.Error("an errored assertion carries no reason")
	}
}

func TestCheckEqualsPassesForEveryScalarType(t *testing.T) {
	tests := []struct {
		name  string
		check models.BodyCheck
	}{
		{"string", models.BodyCheck{Path: "$.status", Value: "ok"}},
		{"number", models.BodyCheck{Path: "$.code", Value: 200}},
		{"number written as a string", models.BodyCheck{Path: "$.code", Value: "200"}},
		{"float", models.BodyCheck{Path: "$.ratio", Value: 1.5}},
		{"boolean", models.BodyCheck{Path: "$.active", Value: true}},
		{"boolean written as a string", models.BodyCheck{Path: "$.active", Value: "true"}},
		{"null", models.BodyCheck{Path: "$.missingValue", Value: nil}},
		{"nested", models.BodyCheck{Path: "$.user.id", Value: 7}},
		{"explicit operator", models.BodyCheck{Path: "$.status", Operator: OpEquals, Value: "ok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(t, tt.check)
			wantStatus(t, got, result.StatusPass)
			if got.Operator != OpEquals {
				t.Errorf("Operator = %q, want %q", got.Operator, OpEquals)
			}
			if got.Path != tt.check.Path {
				t.Errorf("Path = %q, want %q", got.Path, tt.check.Path)
			}
		})
	}
}

func TestCheckEqualsFailsAndSaysWhatItFound(t *testing.T) {
	got := check(t, models.BodyCheck{Path: "$.code", Value: 201})

	wantStatus(t, got, result.StatusFail)
	if got.Actual != float64(200) {
		t.Errorf("Actual = %#v, want 200", got.Actual)
	}
	if !strings.Contains(got.Describe(), "$.code") {
		t.Errorf("Describe() = %q, want it to name the path", got.Describe())
	}
}

func TestCheckEqualsOnAComposite(t *testing.T) {
	wantStatus(t, check(t, models.BodyCheck{Path: "$.empty", Value: []any{}}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.user", Value: map[any]any{"id": 7, "email": "a@b.com"}}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.user", Value: map[any]any{"id": 8}}), result.StatusFail)
}

func TestCheckContains(t *testing.T) {
	tests := []struct {
		name  string
		check models.BodyCheck
		want  result.Status
	}{
		{"substring of a string", models.BodyCheck{Path: "$.message", Operator: OpContains, Value: "created"}, result.StatusPass},
		{"substring that is not there", models.BodyCheck{Path: "$.message", Operator: OpContains, Value: "deleted"}, result.StatusFail},
		{"element of an array", models.BodyCheck{Path: "$.items[*].name", Operator: OpContains, Value: "b"}, result.StatusPass},
		{"element not in an array", models.BodyCheck{Path: "$.items[*].name", Operator: OpContains, Value: "z"}, result.StatusFail},
		{"nothing is in an empty array", models.BodyCheck{Path: "$.empty", Operator: OpContains, Value: "a"}, result.StatusFail},
		{"key of an object", models.BodyCheck{Path: "$.user", Operator: OpContains, Value: "email"}, result.StatusPass},
		{"key not in an object", models.BodyCheck{Path: "$.user", Operator: OpContains, Value: "phone"}, result.StatusFail},
		{"a number contains nothing", models.BodyCheck{Path: "$.code", Operator: OpContains, Value: "2"}, result.StatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantStatus(t, check(t, tt.check), tt.want)
		})
	}
}

func TestCheckMatches(t *testing.T) {
	wantStatus(t, check(t, models.BodyCheck{Path: "$.message", Operator: OpMatches, Value: "^user .* successfully$"}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.message", Operator: OpMatches, Value: "^deleted"}), result.StatusFail)
	// A number is rendered before matching, so a regex over an id still works.
	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Operator: OpMatches, Value: `^\d{3}$`}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.message", Operator: OpMatches, Value: "([a-z"}), result.StatusError, "regular expression")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.user", Operator: OpMatches, Value: "a"}), result.StatusError, "matches needs a string")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.message", Operator: OpMatches, Value: 7}), result.StatusError, "regular expression")
}

func TestCheckExists(t *testing.T) {
	tests := []struct {
		name  string
		check models.BodyCheck
		want  result.Status
	}{
		{"a key that is there", models.BodyCheck{Path: "$.status", Operator: OpExists}, result.StatusPass},
		{"a key that is not there", models.BodyCheck{Path: "$.nope", Operator: OpExists}, result.StatusFail},
		{"a null key does not count as present", models.BodyCheck{Path: "$.missingValue", Operator: OpExists}, result.StatusFail},
		{"asking for absence", models.BodyCheck{Path: "$.nope", Operator: OpExists, Value: false}, result.StatusPass},
		{"asking for absence of something present", models.BodyCheck{Path: "$.status", Operator: OpExists, Value: false}, result.StatusFail},
		{"an empty array still exists", models.BodyCheck{Path: "$.empty", Operator: OpExists}, result.StatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantStatus(t, check(t, tt.check), tt.want)
		})
	}
}

func TestCheckType(t *testing.T) {
	tests := []struct {
		path     string
		typeName string
		want     result.Status
	}{
		{"$.status", TypeString, result.StatusPass},
		{"$.code", TypeNumber, result.StatusPass},
		{"$.active", TypeBoolean, result.StatusPass},
		{"$.user", TypeObject, result.StatusPass},
		{"$.items", TypeArray, result.StatusPass},
		{"$.empty", TypeArray, result.StatusPass},
		{"$.missingValue", TypeNull, result.StatusPass},
		{"$.code", TypeString, result.StatusFail},
		{"$.status", TypeNumber, result.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.path+" is a "+tt.typeName, func(t *testing.T) {
			wantStatus(t, check(t, models.BodyCheck{Path: tt.path, Operator: OpType, Value: tt.typeName}), tt.want)
		})
	}

	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Operator: OpType, Value: "integer"}), result.StatusError, "unknown type", "integer")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Operator: OpType, Value: 7}), result.StatusError, "type name")
}

func TestCheckOrderingOperators(t *testing.T) {
	tests := []struct {
		op    string
		value any
		want  result.Status
	}{
		{OpGt, 199, result.StatusPass},
		{OpGt, 200, result.StatusFail},
		{OpGte, 200, result.StatusPass},
		{OpGte, 201, result.StatusFail},
		{OpLt, 201, result.StatusPass},
		{OpLt, 200, result.StatusFail},
		{OpLte, 200, result.StatusPass},
		{OpLte, 199, result.StatusFail},
		{OpGt, "199", result.StatusPass},
		{OpGt, "later", result.StatusError},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Operator: tt.op, Value: tt.value}), tt.want)
		})
	}

	wantStatus(t, check(t, models.BodyCheck{Path: "$.user", Operator: OpGt, Value: 1}), result.StatusError, "needs a number", "object")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.missingValue", Operator: OpLt, Value: 1}), result.StatusError, "needs a number", "null")
}

func TestCheckTypeHintIsHonoured(t *testing.T) {
	// `type: number` lets a quoted value still compare as a number...
	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Value: "200", Type: TypeNumber}), result.StatusPass)
	// ...and makes the JSON type itself part of the check.
	wantStatus(t, check(t, models.BodyCheck{Path: "$.status", Value: "ok", Type: TypeNumber}), result.StatusError, "is a string", "number")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.active", Value: "true", Type: TypeBoolean}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.status", Value: "ok", Type: TypeString}), result.StatusPass)
	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Value: "later", Type: TypeNumber}), result.StatusError, "not a number")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.code", Value: 200, Type: "integer"}), result.StatusError, "unknown type")
}

func TestCheckBadPathIsAnErrorWithAReason(t *testing.T) {
	wantStatus(t, check(t, models.BodyCheck{Path: "nonsense", Value: "ok"}), result.StatusError, "malformed path")
	wantStatus(t, check(t, models.BodyCheck{Path: "$.no.such.key", Value: "ok"}), result.StatusError, "did not resolve")
	wantStatus(t, check(t, models.BodyCheck{Path: "", Value: "ok"}), result.StatusError, "no path")
	wantStatus(t, check(t, models.BodyCheck{Path: "  ", Value: "ok"}), result.StatusError, "no path")
}

func TestCheckUnknownOperatorNamesIt(t *testing.T) {
	got := check(t, models.BodyCheck{Path: "$.status", Operator: "sorta_equals", Value: "ok"})

	wantStatus(t, got, result.StatusError, "unknown operator", "sorta_equals", "equals")
	if got.Operator != "sorta_equals" {
		t.Errorf("Operator = %q, want the operator the scenario asked for", got.Operator)
	}
}

// The old engine took slice[0] of a jsonpath result and panicked when the list
// was empty. Nothing here may panic.
func TestCheckNeverPanics(t *testing.T) {
	checks := []models.BodyCheck{
		{Path: "$.empty[0]", Value: "a"},
		{Path: "$.empty[*].name", Operator: OpContains, Value: "a"},
		{Path: "$.empty", Operator: OpGt, Value: 1},
		{Path: "$.items[*]", Operator: OpContains, Value: map[any]any{"name": "a"}},
		{Path: "$..name", Operator: OpContains, Value: "a"},
		{Path: "$.status", Operator: OpEquals, Value: map[any]any{"a": "b"}},
		{Path: "$.*", Operator: OpExists},
	}
	for _, c := range checks {
		got := Check("step", c, jsonOf(t, body))
		if got.Status == "" {
			t.Errorf("check %+v returned no status", c)
		}
	}

	// A response that never decoded is a nil map, not a crash.
	for _, c := range checks {
		if got := Check("step", c, nil); got.Status == "" {
			t.Errorf("check %+v against a nil response returned no status", c)
		}
	}
}

func TestBodyReturnsOneResultPerCheckInOrder(t *testing.T) {
	step := models.Step{Name: "login", Response: models.Response{Body: []models.BodyCheck{
		{Path: "$.status", Value: "ok"},
		{Path: "$.code", Value: 999},
		{Path: "$.nope", Value: "x"},
	}}}

	got := Body(step, jsonOf(t, body))

	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	want := []result.Status{result.StatusPass, result.StatusFail, result.StatusError}
	for i, w := range want {
		if got[i].Status != w {
			t.Errorf("result %d status = %q, want %q", i, got[i].Status, w)
		}
		if got[i].Step != "login" {
			t.Errorf("result %d step = %q, want %q", i, got[i].Step, "login")
		}
		if got[i].Kind != "body" {
			t.Errorf("result %d kind = %q, want %q", i, got[i].Kind, "body")
		}
	}
}

func TestBodyWithNoChecksMakesNoAssertions(t *testing.T) {
	if got := Body(models.Step{Name: "ping"}, jsonOf(t, body)); len(got) != 0 {
		t.Errorf("Body = %+v, want no assertions", got)
	}
}

// The point of `value: any` is what yaml.v2 hands over: an unquoted 200 is an
// int and must still match the float64 that encoding/json produced. This is
// the bug the old engine had -- `float64(200) == "200"` was always false.
func TestChecksDecodedFromYAMLCompareProperly(t *testing.T) {
	var step models.Step
	src := `name: "login"
type: api
response:
  status_code: 200
  body:
    - path: "$.code"
      value: 200
    - path: "$.active"
      value: true
    - path: "$.ratio"
      value: 1.5
    - path: "$.status"
      value: "ok"
    - path: "$.code"
      operator: gte
      value: 200
    - path: "$.items[*].name"
      operator: contains
      value: "b"
    - path: "$.user.email"
      operator: matches
      value: ".+@.+"
    - path: "$.nope"
      operator: exists
      value: false
    - path: "$.user"
      operator: type
      value: object
`
	if err := yaml.Unmarshal([]byte(src), &step); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got := step.Response.Body[0].Value; got != 200 {
		t.Fatalf("an unquoted 200 decoded to %#v, want the int 200", got)
	}

	for _, a := range Body(step, jsonOf(t, body)) {
		if !a.Passed() || a.Status != result.StatusPass {
			t.Errorf("%s: status %q, error %q", a.Describe(), a.Status, a.Error)
		}
	}
}

package migrate

import (
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/assert"
	"artemis/pkg/shared/models"
)

// One table over every operator the YAML format has, because the operator
// mapping is what this issue is. Each row's expectation is the whole list of
// expect lines the check becomes, so a mapping that invents or loses an
// assertion shows up as a length mismatch rather than as a passing test on the
// first line.
func TestBodyExpects(t *testing.T) {
	cases := []struct {
		name  string
		check models.BodyCheck
		want  []string
	}{
		{"an absent operator is equals", models.BodyCheck{Path: "$.name", Value: "widget"},
			[]string{`expect body.name == "widget"`}},
		{"equals", models.BodyCheck{Path: "$.name", Operator: "equals", Value: "widget"},
			[]string{`expect body.name == "widget"`}},
		{"contains", models.BodyCheck{Path: "$.name", Operator: "contains", Value: "widg"},
			[]string{`expect body.name contains "widg"`}},
		{"matches", models.BodyCheck{Path: "$.id", Operator: "matches", Value: "^[0-9]+$"},
			[]string{"expect body.id matches /^[0-9]+$/"}},
		{"exists", models.BodyCheck{Path: "$.id", Operator: "exists"},
			[]string{"expect body.id exists"}},
		{"exists with value true", models.BodyCheck{Path: "$.id", Operator: "exists", Value: true},
			[]string{"expect body.id exists"}},
		{"exists with value false", models.BodyCheck{Path: "$.id", Operator: "exists", Value: false},
			[]string{"expect not body.id exists"}},
		{`exists with value "false"`, models.BodyCheck{Path: "$.id", Operator: "exists", Value: "false"},
			[]string{"expect not body.id exists"}},
		{"type takes its name from value", models.BodyCheck{Path: "$.id", Operator: "type", Value: "number"},
			[]string{"expect body.id is number"}},
		{"gt", models.BodyCheck{Path: "$.total", Operator: "gt", Value: 0},
			[]string{"expect body.total > 0"}},
		{"gte", models.BodyCheck{Path: "$.total", Operator: "gte", Value: 0},
			[]string{"expect body.total >= 0"}},
		{"lt", models.BodyCheck{Path: "$.count", Operator: "lt", Value: 2},
			[]string{"expect body.count < 2"}},
		{"lte", models.BodyCheck{Path: "$.count", Operator: "lte", Value: 2},
			[]string{"expect body.count <= 2"}},

		// `type:` beside any operator but exists is a second YAML assertion in
		// one check -- assert.Check tests the JSON type *and* compares -- so it
		// is a second expect line, written first because it is the one that
		// errors when it fails.
		{"a type guard is its own expect", models.BodyCheck{Path: "$.total", Operator: "gt", Value: 0, Type: "number"},
			[]string{"expect body.total is number", "expect body.total > 0"}},
		{"a type guard beside equals", models.BodyCheck{Path: "$.name", Value: "widget", Type: "string"},
			[]string{"expect body.name is string", `expect body.name == "widget"`}},

		// With exists, assert.Check returns before it ever reads Type, so the
		// type is not asserted and migration must not assert it either.
		{"exists ignores a type guard, as the runtime does",
			models.BodyCheck{Path: "$.id", Operator: "exists", Type: "number"},
			[]string{"expect body.id exists"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := bodyExpects(c.check)
			if err != nil {
				t.Fatalf("bodyExpects = error %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("bodyExpects = %q, want %q", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], c.want[i])
				}
			}
			parses(t, "scenario \"t\" {\n  step \"s\" {\n    get \"u\"\n    "+strings.Join(got, "\n    ")+"\n  }\n}\n")
		})
	}
}

// wantsPresent is a copy of assert's unexported truthy, so it is pinned against
// the real thing: checkExists is reached through assert.Check with a body that
// has the path in it, and a check that passes there is one that asked for
// presence.
func TestWantsPresentAgreesWithAssert(t *testing.T) {
	body := map[string]any{"id": "x"}
	for _, value := range []any{nil, true, false, "true", "false", "yes", 0, 1, "", []any{}} {
		check := models.BodyCheck{Path: "$.id", Operator: assert.OpExists, Value: value}
		res := assert.Check("s", check, body)
		// The path is present, so assert passes exactly when the check asked
		// for presence.
		asked := res.Status == result.StatusPass
		if got := wantsPresent(value); got != asked {
			t.Errorf("wantsPresent(%#v) = %v, but assert.Check on a present path %s", value, got, res.Status)
		}
	}
}

func TestBodyExpectsRefusals(t *testing.T) {
	cases := []struct {
		name     string
		check    models.BodyCheck
		mentions string
	}{
		{"an unknown operator", models.BodyCheck{Path: "$.id", Operator: "equalz"}, "unknown operator"},
		{"a type the DSL has no name for", models.BodyCheck{Path: "$.id", Operator: "type", Value: "integer"}, "integer"},
		{"a type guard the DSL has no name for", models.BodyCheck{Path: "$.id", Type: "integer", Value: 1}, "integer"},
		{"type with no name in value", models.BodyCheck{Path: "$.id", Operator: "type", Value: 3}, "needs a type name"},
		{"matches with no pattern in value", models.BodyCheck{Path: "$.id", Operator: "matches", Value: 3}, "regular expression"},
		{"no path at all", models.BodyCheck{Operator: "exists"}, "empty"},
		{"a path that matches many", models.BodyCheck{Path: "$.items[*].sku", Value: 1}, "wildcard"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := bodyExpects(c.check)
			if err == nil {
				t.Fatal("bodyExpects = nil error, want one")
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("error = %q, want it to mention %q", err, c.mentions)
			}
		})
	}
}

func TestTextExpect(t *testing.T) {
	cases := []struct {
		name   string
		stream string
		check  models.TextCheck
		want   string
	}{
		{"an absent operator is contains, as assert.Text has it", "stdout",
			models.TextCheck{Value: "items"}, `expect stdout contains "items"`},
		{"equals", "stdout", models.TextCheck{Operator: "equals", Value: "items"}, `expect stdout == "items"`},
		{"contains", "stderr", models.TextCheck{Operator: "contains", Value: "nothing"}, `expect stderr contains "nothing"`},
		{"matches", "stderr", models.TextCheck{Operator: "matches", Value: "disk (full|busy)"}, "expect stderr matches /disk (full|busy)/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := textExpect(c.stream, c.check)
			if err != nil {
				t.Fatalf("textExpect = error %v", err)
			}
			if got != c.want {
				t.Errorf("textExpect = %q, want %q", got, c.want)
			}
			parses(t, "scenario \"t\" {\n  step \"s\" {\n    run \"sh\"\n    "+got+"\n  }\n}\n")
		})
	}
}

// `empty` is true of a stream holding only whitespace, which no DSL expression
// says, so it is named rather than approximated by `== ""`.
func TestTextExpectRefusesEmptyAndTheUnknown(t *testing.T) {
	for _, op := range []string{assert.OpEmpty, "blank"} {
		_, err := textExpect("stderr", models.TextCheck{Operator: op})
		if err == nil {
			t.Errorf("textExpect(%q) = nil error, want one", op)
			continue
		}
		if !strings.Contains(err.Error(), op) {
			t.Errorf("error = %q, want it to name %q", err, op)
		}
	}
}

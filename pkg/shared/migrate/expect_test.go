package migrate

import (
	"strings"
	"testing"
)

// One table over every operator the YAML format has, because the operator
// mapping is what this issue is. Each row's expectation is the whole list of
// expect lines the check becomes, so a mapping that invents or loses an
// assertion shows up as a length mismatch rather than as a passing test on the
// first line.
func TestBodyExpects(t *testing.T) {
	cases := []struct {
		name  string
		check BodyCheck
		want  []string
	}{
		{"an absent operator is equals", BodyCheck{Path: "$.name", Value: "widget"},
			[]string{`expect body.name == "widget"`}},
		{"equals", BodyCheck{Path: "$.name", Operator: "equals", Value: "widget"},
			[]string{`expect body.name == "widget"`}},
		{"contains", BodyCheck{Path: "$.name", Operator: "contains", Value: "widg"},
			[]string{`expect body.name contains "widg"`}},
		{"matches", BodyCheck{Path: "$.id", Operator: "matches", Value: "^[0-9]+$"},
			[]string{"expect body.id matches /^[0-9]+$/"}},
		{"exists", BodyCheck{Path: "$.id", Operator: "exists"},
			[]string{"expect body.id exists"}},
		{"exists with value true", BodyCheck{Path: "$.id", Operator: "exists", Value: true},
			[]string{"expect body.id exists"}},
		{"exists with value false", BodyCheck{Path: "$.id", Operator: "exists", Value: false},
			[]string{"expect not body.id exists"}},
		{`exists with value "false"`, BodyCheck{Path: "$.id", Operator: "exists", Value: "false"},
			[]string{"expect not body.id exists"}},
		{"type takes its name from value", BodyCheck{Path: "$.id", Operator: "type", Value: "number"},
			[]string{"expect body.id is number"}},
		{"gt", BodyCheck{Path: "$.total", Operator: "gt", Value: 0},
			[]string{"expect body.total > 0"}},
		{"gte", BodyCheck{Path: "$.total", Operator: "gte", Value: 0},
			[]string{"expect body.total >= 0"}},
		{"lt", BodyCheck{Path: "$.count", Operator: "lt", Value: 2},
			[]string{"expect body.count < 2"}},
		{"lte", BodyCheck{Path: "$.count", Operator: "lte", Value: 2},
			[]string{"expect body.count <= 2"}},

		// `type:` beside any operator but exists is a second YAML assertion in
		// one check -- Check tests the JSON type *and* compares -- so it
		// is a second expect line, written first because it is the one that
		// errors when it fails.
		{"a type guard is its own expect", BodyCheck{Path: "$.total", Operator: "gt", Value: 0, Type: "number"},
			[]string{"expect body.total is number", "expect body.total > 0"}},
		{"a type guard beside equals", BodyCheck{Path: "$.name", Value: "widget", Type: "string"},
			[]string{"expect body.name is string", `expect body.name == "widget"`}},

		// With exists, Check returns before it ever reads Type, so the
		// type is not asserted and migration must not assert it either.
		{"exists ignores a type guard, as the runtime does",
			BodyCheck{Path: "$.id", Operator: "exists", Type: "number"},
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

// TestWantsPresent states the rule `operator: exists` had in pkg/shared/assert,
// which ART-40 deleted. It used to be pinned by calling assert.Check on a body
// that had the path in it and reading whether the check passed; with the engine
// gone the rule has to be written down, so it is written down here -- this test
// is now the only record of what a migrated `exists` check means.
func TestWantsPresent(t *testing.T) {
	cases := []struct {
		value any
		want  bool
		why   string
	}{
		{nil, true, "no value at all asks for presence"},
		{true, true, "a bool means itself"},
		{false, false, "a bool means itself"},
		{"true", true, "a string is read as a bool"},
		{"false", false, "a string is read as a bool"},
		{"yes", true, "a string that is not a bool asks for presence"},
		{"", true, "an empty string is not a bool either"},
		{0, true, "a number asks for presence, whatever it is"},
		{1, true, "a number asks for presence, whatever it is"},
		{[]any{}, true, "a collection asks for presence"},
	}
	for _, c := range cases {
		if got := wantsPresent(c.value); got != c.want {
			t.Errorf("wantsPresent(%#v) = %v, want %v: %s", c.value, got, c.want, c.why)
		}
	}
}

// Every value the table above covers has to reach a spelling the DSL can say,
// in both directions.
func TestExistsExpectSpellsBothDirections(t *testing.T) {
	if got := existsExpect("body.id", nil); got != "expect body.id exists" {
		t.Errorf("existsExpect(nil) = %q", got)
	}
	if got := existsExpect("body.id", false); got != "expect not body.id exists" {
		t.Errorf("existsExpect(false) = %q", got)
	}
}

func TestBodyExpectsRefusals(t *testing.T) {
	cases := []struct {
		name     string
		check    BodyCheck
		mentions string
	}{
		{"an unknown operator", BodyCheck{Path: "$.id", Operator: "equalz"}, "unknown operator"},
		{"a type the DSL has no name for", BodyCheck{Path: "$.id", Operator: "type", Value: "integer"}, "integer"},
		{"a type guard the DSL has no name for", BodyCheck{Path: "$.id", Type: "integer", Value: 1}, "integer"},
		{"type with no name in value", BodyCheck{Path: "$.id", Operator: "type", Value: 3}, "needs a type name"},
		{"matches with no pattern in value", BodyCheck{Path: "$.id", Operator: "matches", Value: 3}, "regular expression"},
		{"no path at all", BodyCheck{Operator: "exists"}, "empty"},
		{"a path that matches many", BodyCheck{Path: "$.items[*].sku", Value: 1}, "wildcard"},
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
		check  TextCheck
		want   string
	}{
		{"an absent operator is contains, as Text has it", "stdout",
			TextCheck{Value: "items"}, `expect stdout contains "items"`},
		{"equals", "stdout", TextCheck{Operator: "equals", Value: "items"}, `expect stdout == "items"`},
		{"contains", "stderr", TextCheck{Operator: "contains", Value: "nothing"}, `expect stderr contains "nothing"`},
		{"matches", "stderr", TextCheck{Operator: "matches", Value: "disk (full|busy)"}, "expect stderr matches /disk (full|busy)/"},
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
	for _, op := range []string{OpEmpty, "blank"} {
		_, err := textExpect("stderr", TextCheck{Operator: op})
		if err == nil {
			t.Errorf("textExpect(%q) = nil error, want one", op)
			continue
		}
		if !strings.Contains(err.Error(), op) {
			t.Errorf("error = %q, want it to name %q", err, op)
		}
	}
}

package eval

import (
	"encoding/json"
	"regexp"
	"testing"
)

// TestRenderKeepsART6sRules pins the rules pkg/shared/template.go's renderValue
// has today, value by value, using the same cases pkg/shared/template_test.go's
// cfg() pins. This package subsumes that function, so a divergence here is a
// scenario whose URL changes meaning.
func TestRenderKeepsART6sRules(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"string", "https://api.example.com", "https://api.example.com"},
		{"empty string", "", ""},
		{"integral float", float64(42), "42"},
		{"fractional float", 3.5, "3.5"},
		{"negative float", -7.25, "-7.25"},
		{"zero", float64(0), "0"},
		{"large float keeps its digits", 1e21, "1000000000000000000000"},
		{"int", 7, "7"},
		{"int64", int64(-9), "-9"},
		{"float32", float32(2.5), "2.5"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"nil is null", nil, "null"},
		{"json.Number keeps its digits", json.Number("1234567890123456789"), "1234567890123456789"},
		{"object is compact json", map[string]any{"name": "ada"}, `{"name":"ada"}`},
		{"array is compact json", []any{"a", float64(2)}, `["a",2]`},
		{"nested composite", map[string]any{"t": []any{float64(1)}}, `{"t":[1]}`},
		{"headers render as an object", Headers{"content-type": "application/json"}, `{"content-type":"application/json"}`},
		{"regex renders with its slashes", Regexp{regexp.MustCompile(`.+@.+`)}, `/.+@.+/`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Render(c.v); got != c.want {
				t.Errorf("Render(%#v) = %q, want %q", c.v, got, c.want)
			}
		})
	}
}

// A float that is an integer must not render through %f. This is the bug ART-6
// names in renderValue's comment and the one regression worth its own test.
func TestRenderNeverPrintsAFloatWithTrailingZeros(t *testing.T) {
	if got := Render(float64(42)); got == "42.000000" || got == "4.2e+01" {
		t.Fatalf("Render(float64(42)) = %q, want %q", got, "42")
	}
}

func TestTypeOfNamesTheJSONType(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{true, "boolean"},
		{"s", "string"},
		{float64(1), "number"},
		{7, "number"},
		{json.Number("1"), "number"},
		{[]any{}, "array"},
		{map[string]any{}, "object"},
		{Headers{}, "object"},
		{Regexp{regexp.MustCompile("x")}, "regex"},
		{[]string{"a"}, "array"},
		{map[string]string{"a": "b"}, "object"},
		{struct{}{}, "unknown"},
	}
	for _, c := range cases {
		if got := typeOf(c.v); got != c.want {
			t.Errorf("typeOf(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// Every type name `is` accepts is a name typeOf can produce, and nothing else:
// a type the checker lets an author ask for and the evaluator never answers
// true to would be an assertion that cannot pass.
func TestEveryAskableTypeNameIsOneTypeOfProduces(t *testing.T) {
	produced := map[string]bool{}
	for _, v := range []any{nil, true, "s", float64(1), []any{}, map[string]any{}} {
		produced[typeOf(v)] = true
	}
	for _, name := range []string{"string", "number", "boolean", "object", "array", "null"} {
		if !isTypeName(name) {
			t.Errorf("isTypeName(%q) = false, want true", name)
		}
		if !produced[name] {
			t.Errorf("typeOf never produces %q, which `is %s` can ask for", name, name)
		}
	}
	if isTypeName("regex") {
		t.Error(`isTypeName("regex") = true, want false -- the language has no way to name it`)
	}
}

// equal holds in both directions, which is where it parts company with
// pkg/shared/assert's equalValues.
func TestEqualIsSymmetric(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"two nulls", nil, nil, true},
		{"null and false", nil, false, false},
		{"null and zero", nil, float64(0), false},
		{"null and empty string", nil, "", false},
		{"same numbers", float64(200), float64(200), true},
		{"different numbers", float64(200), float64(201), false},
		{"int against float", 200, float64(200), true},
		{"json.Number against float", json.Number("200"), float64(200), true},
		{"numeric string against number", "200", float64(200), true},
		{"a differently written numeric string", "1.0", float64(1), true},
		{"non-numeric string against number", "abc", float64(0), false},
		{"two identical strings", "ok", "ok", true},
		{"two numeric strings compare exactly", "1.0", "1", false},
		{"two different strings", "ok", "pending", false},
		{"booleans", true, true, true},
		{"differing booleans", true, false, false},
		{"boolean against its rendering", true, "true", true},
		{"boolean against a number", true, float64(1), false},
		{"string against an object", "{}", map[string]any{}, false},
		{"string against an array", "[]", []any{}, false},
		{"arrays element by element", []any{float64(1), "a"}, []any{1, "a"}, true},
		{"arrays of different length", []any{float64(1)}, []any{float64(1), float64(2)}, false},
		{"arrays in a different order", []any{float64(1), float64(2)}, []any{float64(2), float64(1)}, false},
		{"objects key by key", map[string]any{"n": 1}, map[string]any{"n": float64(1)}, true},
		{"objects with an extra key", map[string]any{"n": 1}, map[string]any{"n": 1, "m": 2}, false},
		{"objects with a different key", map[string]any{"n": 1}, map[string]any{"m": 1}, false},
		{"headers against an object", Headers{"a": "b"}, map[string]any{"a": "b"}, true},
		{"nested composites", map[string]any{"t": []any{1}}, map[string]any{"t": []any{float64(1)}}, true},
		{"array against an object", []any{}, map[string]any{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := equal(c.a, c.b); got != c.want {
				t.Errorf("equal(%#v, %#v) = %v, want %v", c.a, c.b, got, c.want)
			}
			if got := equal(c.b, c.a); got != c.want {
				t.Errorf("equal(%#v, %#v) = %v, want %v -- equality must be symmetric", c.b, c.a, got, c.want)
			}
		})
	}
}

func TestNumberReadsEveryNumericShape(t *testing.T) {
	ok := []any{float64(1), float32(1), 1, int64(1), uint8(1), json.Number("1"), "1", " 1 ", "1e0"}
	for _, v := range ok {
		if f, got := number(v); !got || f != 1 {
			t.Errorf("number(%#v) = (%v, %v), want (1, true)", v, f, got)
		}
	}
	notNumbers := []any{nil, true, false, "abc", "", "NaN", "Inf", []any{}, map[string]any{}, Headers{}}
	for _, v := range notNumbers {
		if _, got := number(v); got {
			t.Errorf("number(%#v) reported a number, want false", v)
		}
	}
}

func TestTextRendersScalarsOnly(t *testing.T) {
	for v, want := range map[any]string{"s": "s", true: "true", float64(200): "200", 7: "7"} {
		if got, ok := text(v); !ok || got != want {
			t.Errorf("text(%#v) = (%q, %v), want (%q, true)", v, got, ok, want)
		}
	}
	for _, v := range []any{nil, []any{}, map[string]any{}, Headers{}, Regexp{regexp.MustCompile("x")}} {
		if _, ok := text(v); ok {
			t.Errorf("text(%#v) rendered, want false", v)
		}
	}
}

func TestHeadersLookupIgnoresCase(t *testing.T) {
	h := Headers{"content-type": "application/json"}
	for _, name := range []string{"content-type", "Content-Type", "CONTENT-TYPE"} {
		v, ok := h.lookup(name)
		if !ok || v != "application/json" {
			t.Errorf("Headers.lookup(%q) = (%v, %v), want the content type", name, v, ok)
		}
	}
	if _, ok := h.lookup("accept"); ok {
		t.Error(`Headers.lookup("accept") found something, want false`)
	}
}

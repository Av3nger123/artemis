package assert

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// jsonOf decodes a response body the way the runner does, so the tests compare
// against the types encoding/json really produces.
func jsonOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad test fixture %q: %v", body, err)
	}
	return out
}

func TestEqualValuesCoercesAcrossYAMLAndJSON(t *testing.T) {
	tests := []struct {
		name     string
		expected any
		actual   any
		want     bool
	}{
		{"int against json number", 200, float64(200), true},
		{"int against a different number", 200, float64(201), false},
		{"quoted number against json number", "200", float64(200), true},
		{"float against json number", 1.5, float64(1.5), true},
		{"bool against json bool", true, true, true},
		{"bool against the other bool", true, false, false},
		{"quoted bool against json bool", "true", true, true},
		{"bool against a string", true, "true", false},
		{"string against string", "ok", "ok", true},
		{"string against a different string", "ok", "pending", false},
		{"number against a quoted number in the response", 200, "200", true},
		{"nil against json null", nil, nil, true},
		{"nil against a value", nil, "ok", false},
		{"value against json null", "ok", nil, false},
		{"string against an object", "ok", map[string]any{"a": 1.0}, false},
		{"list against json array", []any{"a", "b"}, []any{"a", "b"}, true},
		{"list against a different array", []any{"a"}, []any{"a", "b"}, false},
		{"empty list against an empty array", []any{}, []any{}, true},
		{"object against json object", map[string]any{"a": "b"}, map[string]any{"a": "b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := equalValues(normalizeYAML(tt.expected), tt.actual); got != tt.want {
				t.Errorf("equalValues(%#v, %#v) = %v, want %v", tt.expected, tt.actual, got, tt.want)
			}
		})
	}
}

func TestNormalizeYAMLRewritesMapsForComparison(t *testing.T) {
	in := []any{map[any]any{"a": 1, 2: map[any]any{"b": "c"}}}

	got := normalizeYAML(in)

	want := []any{map[string]any{"a": 1, "2": map[string]any{"b": "c"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeYAML = %#v, want %#v", got, want)
	}
}

func TestJSONTypeOfNamesEveryJSONShape(t *testing.T) {
	body := jsonOf(t, `{"s":"x","n":1,"b":true,"nul":null,"arr":[],"obj":{}}`)
	want := map[string]string{"s": TypeString, "n": TypeNumber, "b": TypeBoolean, "nul": TypeNull, "arr": TypeArray, "obj": TypeObject}

	for key, wantType := range want {
		if got := jsonTypeOf(body[key]); got != wantType {
			t.Errorf("jsonTypeOf(%s) = %q, want %q", key, got, wantType)
		}
	}
	if got := jsonTypeOf(make(chan int)); got != "unknown" {
		t.Errorf("jsonTypeOf(chan) = %q, want %q", got, "unknown")
	}
}

func TestToStringRendersScalarsTheWayAScenarioWritesThem(t *testing.T) {
	tests := []struct {
		in   any
		want string
		ok   bool
	}{
		{float64(200), "200", true},
		{float64(1.5), "1.5", true},
		{int64(7), "7", true},
		{true, "true", true},
		{"ok", "ok", true},
		{nil, "", false},
		{[]any{1.0}, "", false},
		{map[string]any{}, "", false},
	}
	for _, tt := range tests {
		got, ok := toString(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("toString(%#v) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCoerceToTypeReadsAnExpectationAsTheNamedType(t *testing.T) {
	if got, err := coerceToType("200", TypeNumber); err != nil || got != float64(200) {
		t.Errorf(`coerceToType("200", number) = %v, %v; want 200, nil`, got, err)
	}
	if got, err := coerceToType("true", TypeBoolean); err != nil || got != true {
		t.Errorf(`coerceToType("true", boolean) = %v, %v; want true, nil`, got, err)
	}
	if got, err := coerceToType(200, TypeString); err != nil || got != "200" {
		t.Errorf(`coerceToType(200, string) = %v, %v; want "200", nil`, got, err)
	}
	if _, err := coerceToType("later", TypeNumber); err == nil {
		t.Error(`coerceToType("later", number) = nil error, want one`)
	}
	if _, err := coerceToType(1.0, TypeBoolean); err == nil {
		t.Error("coerceToType(1, boolean) = nil error, want one")
	}
}

func TestTruthyReadsAnExistsExpectation(t *testing.T) {
	for _, tt := range []struct {
		in   any
		want bool
	}{{nil, true}, {true, true}, {false, false}, {"false", false}, {"true", true}, {"yes", true}, {1, true}} {
		if got := truthy(tt.in); got != tt.want {
			t.Errorf("truthy(%#v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestToFloatReadsEveryNumberAResponseCanCarry(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{"json number", float64(200), 200, true},
		{"yaml int", 200, 200, true},
		{"int64", int64(-7), -7, true},
		{"int8", int8(7), 7, true},
		{"uint64", uint64(7), 7, true},
		{"float32", float32(1.5), 1.5, true},
		{"named int kind", time.Duration(3), 3, true},
		{"json.Number", json.Number("1.5"), 1.5, true},
		{"unparseable json.Number", json.Number("later"), 0, false},
		{"quoted number", "200", 200, true},
		{"quoted number with spaces", " 200 ", 200, true},
		{"quoted float", "1.5", 1.5, true},
		{"exponent", "2e3", 2000, true},
		{"word", "later", 0, false},
		{"empty string", "", 0, false},
		{"NaN is not a number to compare", "NaN", 0, false},
		{"infinity is not comparable either", "Inf", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
		{"array", []any{1.0}, 0, false},
		{"object", map[string]any{"a": 1.0}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat(tt.in)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("toFloat(%#v) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

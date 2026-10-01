package assert

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// JSON type names, as a scenario writes them in `type:` or as the value of a
// `type` operator.
const (
	TypeString  = "string"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	TypeObject  = "object"
	TypeArray   = "array"
	TypeNull    = "null"
)

// TypeNames are the type names a scenario may ask for, in the order they are
// worth listing in an error message.
var TypeNames = []string{TypeString, TypeNumber, TypeBoolean, TypeObject, TypeArray, TypeNull}

func knownType(name string) bool {
	for _, n := range TypeNames {
		if n == name {
			return true
		}
	}
	return false
}

// jsonTypeOf names v the way JSON would. Anything encoding/json can produce
// has a name; anything else -- a type only YAML can make -- is "unknown", which
// matches no asked-for type.
func jsonTypeOf(v any) string {
	switch t := v.(type) {
	case nil:
		return TypeNull
	case bool:
		return TypeBoolean
	case string:
		return TypeString
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return TypeNumber
	case []any:
		return TypeArray
	case map[string]any:
		return TypeObject
	default:
		switch reflect.ValueOf(t).Kind() {
		case reflect.Slice, reflect.Array:
			return TypeArray
		case reflect.Map:
			return TypeObject
		default:
			return "unknown"
		}
	}
}

// normalizeYAML rewrites a value decoded by yaml.v2 into the shapes
// encoding/json produces, so an expectation and a response can be compared.
// yaml.v2 decodes a mapping to map[interface{}]interface{}, which never equals
// a JSON object; everything else is passed through untouched.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = normalizeYAML(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	default:
		return v
	}
}

// toFloat reads v as a number.
//
// Every numeric kind a YAML or JSON decoder can hand over works -- float64 is
// what encoding/json gives, int is what yaml.v2 gives an unquoted scalar, and
// the reflect fallback covers the rest without a type switch that has to list
// them. A json.Number works too, for a decoder set to UseNumber. So does a
// string that parses as a finite number: a response that quotes its numbers
// should still be comparable. A bool is not a number, and neither is a
// non-finite string like "NaN" -- NaN compares false against everything, which
// would look like a silently failing assertion.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case nil, bool:
		return 0, false
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return 0, false
	}
}

// toString renders a scalar the way a scenario would write it: a float64 of
// 200 is "200", not "200.000000". Composites and nil have no sensible
// rendering and report false.
func toString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	default:
		if f, ok := toFloat(v); ok {
			return strconv.FormatFloat(f, 'f', -1, 64), true
		}
		return "", false
	}
}

// equalValues compares an expectation against a value out of a JSON response.
//
// A string expectation is compared against the value rendered as a string, so
// `value: "200"` matches a JSON 200 and `value: "true"` matches a JSON true.
// Two numbers compare as float64, whatever Go types they arrived as, and a
// numeric expectation also matches a response that quotes its numbers. Use
// `type:` where the JSON type itself matters. Anything else -- arrays, objects,
// nulls -- compares deeply, element by element, so a YAML int inside a list
// still matches the float64 a JSON decoder produced.
func equalValues(expected, actual any) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	if want, ok := expected.(string); ok {
		got, ok := toString(actual)
		return ok && want == got
	}
	if wantBool, ok := expected.(bool); ok {
		gotBool, ok := actual.(bool)
		return ok && wantBool == gotBool
	}
	if want, ok := toFloat(expected); ok {
		got, ok := toFloat(actual)
		return ok && want == got
	}
	switch want := expected.(type) {
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !equalValues(want[i], got[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for key, w := range want {
			g, found := got[key]
			if !found || !equalValues(w, g) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(expected, actual)
	}
}

// coerceToType reads an expectation as the named JSON type, so `type: number`
// with `value: "200"` is the number 200. Object, array and null expectations
// are left as they are: there is nothing to parse.
func coerceToType(v any, typeName string) (any, error) {
	switch typeName {
	case TypeNumber:
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("%v is not a number", v)
		}
		return f, nil
	case TypeBoolean:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%v is not a boolean", v)
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("%v is not a boolean", v)
		}
		return b, nil
	case TypeString:
		s, ok := toString(v)
		if !ok {
			return nil, fmt.Errorf("%v is not a string", v)
		}
		return s, nil
	default:
		return v, nil
	}
}

// truthy reads an expectation meant as a yes or no -- `exists` with
// `value: false`. An absent value means yes.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(t)
		return err != nil || b
	default:
		return true
	}
}

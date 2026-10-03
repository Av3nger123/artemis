package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"artemis/pkg/dsl/token"
)

// The value domain is JSON's: nil, bool, float64, string, []any and
// map[string]any. Every number is a float64 whatever it arrived as -- a number
// literal, a decoded response body, a captured value -- so comparing two of
// them needs no type table.
//
// Two things live alongside it. Headers is an object whose lookup ignores a
// name's case, because an HTTP header's case is not meaningful and
// `headers["Content-Type"]` is what an author writes. Regexp is a compiled
// regex literal, which is a value only so that `matches` can take one; using it
// anywhere else is an errored assertion rather than a silent coercion.

// Headers is the object an api step binds to `headers`. Its member and index
// lookup lower-cases the name, so `headers["Content-Type"]` and
// `headers["content-type"]` are the same field. A plain object is never looked
// up this way: `body.Data` must not quietly resolve to `body.data`.
type Headers map[string]any

// lookup reads name case-insensitively.
func (h Headers) lookup(name string) (any, bool) {
	if v, ok := h[name]; ok {
		return v, true
	}
	want := strings.ToLower(name)
	for k, v := range h {
		if strings.ToLower(k) == want {
			return v, true
		}
	}
	return nil, false
}

// Regexp is a compiled regex literal.
type Regexp struct{ *regexp.Regexp }

// typeOf names a value the way JSON would, using the type names `is` accepts so
// that `expect x is number` and this function cannot disagree. A value only Go
// can make -- a Regexp, a type no decoder produces -- is "unknown", which
// matches no asked-for type.
func typeOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any, Headers:
		return "object"
	case Regexp:
		return "regex"
	default:
		switch reflect.ValueOf(t).Kind() {
		case reflect.Slice, reflect.Array:
			return "array"
		case reflect.Map:
			return "object"
		default:
			return "unknown"
		}
	}
}

// isTypeName reports whether word is a type `is` can ask for. "regex" is not
// one: it is a value kind the language has no way to name.
func isTypeName(word string) bool { return token.IsTypeName(word) }

// Render renders a value the way a scenario would have written it, which is
// ART-6's renderValue rules unchanged: a number as written (42, not
// 42.000000), a boolean as true or false, null as null, and an object or array
// as compact JSON. It is what string interpolation emits, so a URL and an
// assertion message agree about what a number looks like.
func Render(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", t)
	case json.Number:
		return t.String()
	case Headers:
		return Render(map[string]any(t))
	case Regexp:
		return "/" + t.String() + "/"
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Nothing the evaluator produces reaches this, because every composite
		// it builds came out of a decoder or out of an object literal. A value
		// smuggled in through Env should still read as something.
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// number reads v as a number, for the ordering operators and unary minus.
//
// Every numeric kind a decoder can hand over works, and so does a string that
// parses as a finite number: a response that quotes its numbers is still
// comparable, which is what pkg/shared/assert's toFloat does today. A bool is
// not a number, and neither is a non-finite string like "NaN" -- NaN compares
// false against everything, which would look like a silently failing
// assertion.
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case nil, bool:
		return 0, false
	case float64:
		return finite(t)
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return finite(f)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return finite(f)
	case Headers, Regexp:
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return finite(rv.Float())
	default:
		return 0, false
	}
}

func finite(f float64) (float64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// text reads a scalar as a string, for `contains` and `matches`. A composite,
// a regex and nil have no sensible rendering here and report false: "does an
// object match /x/" has no answer worth printing.
func text(v any) (string, bool) {
	switch v.(type) {
	case string, bool:
		return Render(v), true
	case nil, []any, map[string]any, Headers, Regexp:
		return "", false
	}
	if _, ok := number(v); ok {
		return Render(v), true
	}
	return "", false
}

// equal compares two values, symmetrically.
//
// pkg/shared/assert's equalValues is asymmetric: it is written as expectation
// against response, so `value: 1` matches a body of "1.0" and `value: "1.0"`
// does not match a body of 1. That is defensible when one side is the
// expectation and indefensible when both sides are expressions, so the rules
// here hold in either direction:
//
//   - two strings compare exactly, so "1.0" == "1" is false;
//   - two numbers compare as numbers, whatever Go types they arrived as;
//   - a string against a number compares numerically when the string parses as
//     a finite number, so status == "200" holds against a JSON 200;
//   - a string against a boolean compares rendered, so flag == "true" holds;
//   - arrays and objects compare deeply with these same rules, so a number
//     inside a list still matches whatever the decoder produced;
//   - anything else -- a scalar against a composite, a boolean against a
//     number -- is false. Use `is <type>` where the JSON type itself matters.
func equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	if aIsStr && bIsStr {
		return as == bs
	}

	ab, aIsBool := a.(bool)
	bb, bIsBool := b.(bool)
	if aIsBool && bIsBool {
		return ab == bb
	}
	if aIsBool && bIsStr {
		return Render(ab) == bs
	}
	if bIsBool && aIsStr {
		return Render(bb) == as
	}
	// A boolean against anything else is not a number and not a string: false.
	if aIsBool || bIsBool {
		return false
	}

	af, aIsNum := number(a)
	bf, bIsNum := number(b)
	if aIsNum && bIsNum {
		return af == bf
	}
	// One side is a string the other side cannot be read as.
	if aIsStr || bIsStr {
		return false
	}

	switch want := a.(type) {
	case []any:
		got, ok := b.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !equal(want[i], got[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		return equalObjects(want, object(b))
	case Headers:
		return equalObjects(map[string]any(want), object(b))
	default:
		return reflect.DeepEqual(a, b)
	}
}

func equalObjects(want map[string]any, got map[string]any) bool {
	if got == nil || len(got) != len(want) {
		return false
	}
	for key, w := range want {
		g, found := got[key]
		if !found || !equal(w, g) {
			return false
		}
	}
	return true
}

// object reads v as an object, flattening Headers so the two spellings of an
// object behave alike everywhere but lookup.
func object(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case Headers:
		return map[string]any(t)
	default:
		return nil
	}
}

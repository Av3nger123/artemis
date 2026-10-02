package jsonpath

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

const body = `{
	"count": 3,
	"status": "ok",
	"nothing": null,
	"empty": [],
	"items": [{"name": "a"}, {"name": "b"}],
	"nested": {"name": "n"}
}`

func data(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("test body is not JSON: %v", err)
	}
	return out
}

// The paths a scenario actually writes return the value, with the type
// encoding/json gave it -- not a one-element slice around it.
func TestASinglePathReturnsTheValueItself(t *testing.T) {
	cases := []struct {
		path string
		want any
	}{
		{"$.count", float64(3)},
		{"$.status", "ok"},
		{"$.items[0].name", "a"},
		{"$.nested.name", "n"},
		{"$.items[1]", map[string]any{"name": "b"}},
	}
	for _, c := range cases {
		got, err := Lookup(c.path, data(t))
		if err != nil {
			t.Errorf("Lookup(%q) returned %v", c.path, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Lookup(%q) = %#v, want %#v", c.path, got, c.want)
		}
	}
}

// A path that is not there is ErrNotFound, matched as a sentinel so the caller
// can answer `exists` without reading a message.
func TestAnAbsentPathIsErrNotFound(t *testing.T) {
	for _, path := range []string{"$.nope", "$.no.such.key", "$.items[9].name", "$.empty[0]"} {
		_, err := Lookup(path, data(t))
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Lookup(%q) error = %v, want ErrNotFound", path, err)
		}
	}
}

// A null that is present is a value. oliveagle could not tell this from an
// absent path; a check on a field that exists and is null must not read as
// "that field is missing".
func TestAPresentNullIsAValueNotAMissingPath(t *testing.T) {
	got, err := Lookup("$.nothing", data(t))
	if err != nil {
		t.Fatalf("Lookup($.nothing) returned %v, want the null", err)
	}
	if got != nil {
		t.Errorf("Lookup($.nothing) = %#v, want nil", got)
	}
}

// A wildcard, a descent or a filter returns every match as a slice, which is
// what `contains` is checked against.
func TestAMultiMatchPathReturnsEveryMatch(t *testing.T) {
	cases := []struct {
		path string
		want []any
	}{
		{"$.items[*].name", []any{"a", "b"}},
		{"$.items[?(@.name == 'b')].name", []any{"b"}},
	}
	for _, c := range cases {
		got, err := Lookup(c.path, data(t))
		if err != nil {
			t.Errorf("Lookup(%q) returned %v", c.path, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Lookup(%q) = %#v, want %#v", c.path, got, c.want)
		}
	}
	// A descent finds the name at every depth; the order is the library's, so
	// only the set is asserted.
	got, err := Lookup("$..name", data(t))
	if err != nil {
		t.Fatalf("Lookup($..name) returned %v", err)
	}
	list, ok := got.([]any)
	if !ok || len(list) != 3 {
		t.Errorf("Lookup($..name) = %#v, want three names", got)
	}
}

// Nothing matched is an empty slice, not an error: `contains` should fail on it,
// and ranging over the result should need no nil check.
func TestAMultiMatchPathThatMatchesNothingIsAnEmptySlice(t *testing.T) {
	for _, path := range []string{"$.empty[*].name", "$.items[?(@.name == 'z')]"} {
		got, err := Lookup(path, data(t))
		if err != nil {
			t.Errorf("Lookup(%q) returned %v, want an empty slice", path, err)
			continue
		}
		if !reflect.DeepEqual(got, []any{}) {
			t.Errorf("Lookup(%q) = %#v, want an empty slice", path, got)
		}
	}
}

// Every one of these took the old library down with a panic; they must come back
// as compile errors instead.
func TestAMalformedPathIsACompileError(t *testing.T) {
	for _, path := range []string{"$.", "$[", "$.a[", "$[?(@.a", "nonsense", "a.b", "", "   "} {
		if _, err := Compile(path); err == nil {
			t.Errorf("Compile(%q) returned no error", path)
		}
	}
}

// A compiled path is reusable and remembers how it was written, which is the
// name an error message has to show.
func TestACompiledPathKeepsItsText(t *testing.T) {
	p, err := Compile(" $.count ")
	if err != nil {
		t.Fatalf("Compile returned %v", err)
	}
	if p.String() != "$.count" {
		t.Errorf("String() = %q, want the trimmed path", p.String())
	}
	for i := 0; i < 2; i++ {
		if got, err := p.Lookup(data(t)); err != nil || got != float64(3) {
			t.Errorf("lookup %d = %#v, %v", i, got, err)
		}
	}
}

// A body that never decoded is a nil map, not a crash.
func TestLookupAgainstNilData(t *testing.T) {
	if _, err := Lookup("$.count", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("Lookup against nil data = %v, want ErrNotFound", err)
	}
	if got, err := Lookup("$.items[*].name", nil); err != nil || !reflect.DeepEqual(got, []any{}) {
		t.Errorf("multi-match lookup against nil data = %#v, %v", got, err)
	}
}

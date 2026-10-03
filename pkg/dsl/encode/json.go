package encode

import "fmt"

// Reading a document back is reading untyped JSON, and the thing that makes the
// difference between a usable error and a shrug is saying *where*. So every
// accessor here carries the path it was reached by, and every error names it:
//
//	scenarios[0].body[1].value: missing "op"
//	scenarios[0].body[0].action: unknown node kind "frobnicate"
//
// A UI posting a tree gets told which node it got wrong, which is the only
// form of that message anyone can act on.

// value is one JSON value and the path that located it.
type value struct {
	v    any
	path string
}

// object is the value as a node object.
func (a value) object() (fields, error) {
	m, ok := a.v.(map[string]any)
	if !ok {
		return fields{}, fmt.Errorf("%s: expected an object, found %s", a.path, jsonType(a.v))
	}
	return fields{m: m, path: a.path}, nil
}

// fields is a JSON object being read, with the path it was reached by.
type fields struct {
	m    map[string]any
	path string
}

func (f fields) has(key string) bool {
	_, ok := f.m[key]
	return ok
}

// str is a required string field.
func (f fields) str(key string) (string, error) {
	v, ok := f.m[key]
	if !ok {
		return "", fmt.Errorf("%s: missing %q", f.path, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: %q should be a string, found %s", f.path, key, jsonType(v))
	}
	return s, nil
}

// optStr is a string field that may be absent, in which case it is "".
func (f fields) optStr(key string) (string, error) {
	if !f.has(key) {
		return "", nil
	}
	return f.str(key)
}

// flag is a boolean field that may be absent, in which case it is false --
// obj.set's rule on the way out, so a false flag never appears in a document.
func (f fields) flag(key string) (bool, error) {
	v, ok := f.m[key]
	if !ok {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: %q should be a boolean, found %s", f.path, key, jsonType(v))
	}
	return b, nil
}

// child is an object-valued field. present is false when the field is absent,
// which is how every optional child in the encoding reads.
func (f fields) child(key string) (fields, bool, error) {
	v, ok := f.m[key]
	if !ok {
		return fields{}, false, nil
	}
	o, err := value{v: v, path: f.path + "." + key}.object()
	return o, true, err
}

// list is an array-valued field, as values with their paths filled in. An
// absent field is an empty list, not an error: an empty list is absent from a
// document.
func (f fields) list(key string) ([]value, error) {
	v, ok := f.m[key]
	if !ok {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %q should be an array, found %s", f.path, key, jsonType(v))
	}
	out := make([]value, 0, len(items))
	for i, it := range items {
		out = append(out, value{v: it, path: fmt.Sprintf("%s.%s[%d]", f.path, key, i)})
	}
	return out, nil
}

// strList is an array of strings.
func (f fields) strList(key string) ([]string, error) {
	items, err := f.list(key)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: expected a string, found %s", it.path, jsonType(it.v))
		}
		out = append(out, s)
	}
	return out, nil
}

// kind is the node's kind, which every node object has.
func (f fields) kind() (string, error) { return f.str("kind") }

// at is the path of a field of this object, for an error about it.
func (f fields) at(key string) string { return f.path + "." + key }

// jsonType names what was found, in JSON's vocabulary rather than Go's: a
// client reading the error wrote JSON, not Go.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64, int:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "something else"
}

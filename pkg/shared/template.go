package shared

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The delimiters a placeholder is written with: {{name}}.
const (
	openDelim  = "{{"
	closeDelim = "}}"
)

// TransformText renders templateStr, replacing every {{name}} with the value
// config holds under name.
//
// Text outside a placeholder is copied through byte for byte, so a lone or
// trailing brace is just a brace. A placeholder that is never closed, one with
// no name in it, and one naming something config does not hold are all errors:
// a typo'd variable used to be re-emitted as literal "{{tokn}}" and sent to the
// server, which fails later and somewhere else. Substitution is single-pass --
// a value that itself contains {{x}} is emitted as it is, never re-expanded.
//
// There is no escape syntax: {{ always opens a placeholder. A scenario that
// needs literal braces can put them in a variable's value, which is not
// re-scanned.
func TransformText(templateStr string, config map[string]interface{}) (string, error) {
	var out strings.Builder
	rest := templateStr
	// Offset of rest within templateStr, so an error can name a real position.
	offset := 0

	for {
		open := strings.Index(rest, openDelim)
		if open < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:open])

		body := rest[open+len(openDelim):]
		end := strings.Index(body, closeDelim)
		if end < 0 {
			return "", fmt.Errorf("unclosed %q at offset %d: %q is missing its %q",
				openDelim, offset+open, rest[open:], closeDelim)
		}

		name := strings.TrimSpace(body[:end])
		if name == "" {
			return "", fmt.Errorf("empty placeholder %q at offset %d", rest[open:open+len(openDelim)+end+len(closeDelim)], offset+open)
		}
		val, ok := config[name]
		if !ok {
			return "", fmt.Errorf("unknown variable %q at offset %d: declare it under variables: or capture it with scripts:", name, offset+open)
		}
		rendered, err := renderValue(val)
		if err != nil {
			return "", fmt.Errorf("variable %q: %w", name, err)
		}
		out.WriteString(rendered)

		consumed := open + len(openDelim) + end + len(closeDelim)
		rest = rest[consumed:]
		offset += consumed
	}
}

// renderValue renders a value the way a scenario would have written it.
//
// Values reach the config map from two places: scenario variables, which are
// strings, and scripts: captures, which come out of encoding/json -- where
// every number is a float64. So an id of 42 has to render as "42" and not
// "42.000000" or "4.2e+01". Objects, arrays and nil render as compact JSON, for
// a body that templates a captured object into itself.
func renderValue(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32), nil
	case int:
		return strconv.Itoa(t), nil
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", t), nil
	case json.Number:
		return t.String(), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("cannot be rendered into a template: %w", err)
	}
	return string(b), nil
}

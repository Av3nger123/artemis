package migrate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/token"
)

// This file is the three translations every other part of migration is built
// out of: a JSON path to a DSL expression, a YAML value to a DSL literal, and
// a templated string to a DSL string.
//
// None of them guesses. A construct with no DSL spelling is an error naming
// what it was, because a migrator that half-translates a file produces a
// scenario that compiles and asserts something else -- the one outcome worse
// than refusing to convert at all.

// pathExpr turns a JSON path into a DSL expression rooted at root.
//
//	$              -> body
//	$.data.token   -> body.data.token
//	$.items[0].sku -> body.items[0].sku
//	$['a-b']       -> body["a-b"]
//
// Every path artemis documents is one of those shapes: root, child and index
// steps, which is exactly jsonpath.Path's `single` class. A wildcard, a
// descent, a filter, a union or a slice can match many values and the DSL has
// no spelling for any of them, so each is an error rather than a path that
// quietly reads the first match.
//
// A segment that is not an identifier -- a dash in it, a leading digit, a
// reserved word -- becomes an index with a string in it, which is the DSL's own
// way of writing a member it cannot write as a name.
func pathExpr(root, path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("the path is empty")
	}
	if !strings.HasPrefix(p, "$") {
		return "", fmt.Errorf("path %q does not start with $", path)
	}

	var out strings.Builder
	out.WriteString(root)
	rest := p[len("$"):]

	for rest != "" {
		switch {
		case strings.HasPrefix(rest, ".."):
			return "", unspellable(path, "a recursive descent (..)")
		case strings.HasPrefix(rest, "."):
			name, tail, err := childSegment(path, rest[1:])
			if err != nil {
				return "", err
			}
			out.WriteString(member(name))
			rest = tail
		case strings.HasPrefix(rest, "["):
			seg, tail, err := bracketSegment(path, rest)
			if err != nil {
				return "", err
			}
			out.WriteString(seg)
			rest = tail
		default:
			return "", fmt.Errorf("path %q has %q where a . or a [ should be", path, rest)
		}
	}
	return out.String(), nil
}

// childSegment reads the name after a dot and returns it with what follows.
func childSegment(path, rest string) (name, tail string, err error) {
	if rest == "" {
		return "", "", fmt.Errorf("path %q ends in a .", path)
	}
	if rest[0] == '*' {
		return "", "", unspellable(path, "a wildcard (*)")
	}
	end := strings.IndexAny(rest, ".[")
	if end < 0 {
		end = len(rest)
	}
	name = rest[:end]
	if name == "" {
		return "", "", fmt.Errorf("path %q has an empty segment", path)
	}
	return name, rest[end:], nil
}

// bracketSegment reads one [...] step: an array index, or a quoted member name.
func bracketSegment(path, rest string) (seg, tail string, err error) {
	end := strings.Index(rest, "]")
	if end < 0 {
		return "", "", fmt.Errorf("path %q has a [ with no ]", path)
	}
	inner := strings.TrimSpace(rest[1:end])
	tail = rest[end+1:]

	switch {
	case inner == "":
		return "", "", fmt.Errorf("path %q has an empty []", path)
	case inner == "*":
		return "", "", unspellable(path, "a wildcard ([*])")
	case strings.HasPrefix(inner, "?"):
		return "", "", unspellable(path, "a filter")
	case strings.Contains(inner, ":"):
		return "", "", unspellable(path, "a slice")
	case strings.Contains(inner, ","):
		return "", "", unspellable(path, "a union")
	}

	if n, err := strconv.Atoi(inner); err == nil {
		return "[" + strconv.Itoa(n) + "]", tail, nil
	}
	if unquoted, ok := unquote(inner); ok {
		return member(unquoted), tail, nil
	}
	return "", "", fmt.Errorf("path %q has %q in brackets, which is neither an index nor a quoted name", path, inner)
}

// unquote strips one layer of single or double quotes.
func unquote(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if (q == '\'' || q == '"') && s[len(s)-1] == q {
		return s[1 : len(s)-1], true
	}
	return "", false
}

// member is `.name` when name can be written as one, and `["name"]` when it
// cannot.
func member(name string) string {
	if isIdent(name) && !token.IsReserved(name) {
		return "." + name
	}
	return "[" + quote(name) + "]"
}

// isIdent reports whether name is identifier-shaped: a letter or underscore,
// then letters, digits and underscores. It is the lexer's rule, written here
// rather than reached for, because pkg/dsl/lexer does not export it.
func isIdent(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// unspellable is the one wording for "this path shape has no DSL equivalent".
func unspellable(path, what string) error {
	return fmt.Errorf("path %q uses %s, which the DSL has no spelling for: "+
		"an expect reads one value, so rewrite the check against a single path", path, what)
}

// literal renders a value the way a .art file would write it.
//
// Values arrive from yaml.v3, so an integer is an int, a float is a float64 and
// a mapping is a map[string]any -- or a map[any]any for a key that is not a
// string, which the assertion engine already normalises and which is rendered
// here with the key as text.
//
// str is how a string leaf is rendered, and the two callers want different
// answers. An assertion's `value:` is never templated by the YAML runtime --
// httpstep renders the url, the request body and the header values, and nothing
// else -- so a check migrates with quote and a `{{x}}` in it stays the four
// characters it always compared against. A request body is templated, so it
// migrates with templated and its placeholders become interpolations.
//
// Keys are emitted in sorted order, because a YAML mapping has no order once it
// is decoded and migrating the same file twice has to produce the same bytes.
func literal(v any, str func(string) (string, error)) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(t), nil
	case json.Number:
		return jsonNumber(t)
	case string:
		return str(t)
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case []any:
		return list(t, str)
	case map[string]any:
		return object(t, str)
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = val
		}
		return object(out, str)
	default:
		return "", fmt.Errorf("value %v (%T) has no DSL spelling", v, v)
	}
}

// plain is literal with string leaves taken as text: the form an assertion's
// expected value takes.
func plain(v any) (string, error) {
	return literal(v, func(s string) (string, error) { return quote(s), nil })
}

func list(items []any, str func(string) (string, error)) (string, error) {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		s, err := literal(it, str)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

func object(m map[string]any, str func(string) (string, error)) (string, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		s, err := literal(m[k], str)
		if err != nil {
			return "", fmt.Errorf("key %q: %w", k, err)
		}
		parts = append(parts, quote(k)+": "+s)
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

// templated is a YAML string that the runtime renders, as a DSL expression.
//
// A string that is nothing but {{env.NAME}} becomes the bare call env("NAME"),
// which is the design's mapping and the form a `var` wants. Anything else is a
// string literal, with every placeholder turned into an interpolation -- so the
// value keeps being a string, which is what the YAML templater produced.
func templated(s string) (string, error) {
	if name, ok := soleEnvPlaceholder(s); ok {
		return `env(` + quote(name) + `)`, nil
	}
	return interpString(s)
}

// soleEnvPlaceholder reports whether s is exactly one {{env.NAME}} and nothing
// else, and if so the NAME.
func soleEnvPlaceholder(s string) (string, bool) {
	const prefix = "{{env."
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, closeDelim) {
		return "", false
	}
	name := s[len(prefix) : len(s)-len(closeDelim)]
	if name == "" || strings.Contains(name, openDelim) || strings.Contains(name, closeDelim) {
		return "", false
	}
	return name, true
}

// The delimiters a YAML placeholder is written with, as shared.TransformText
// spells them. They are repeated rather than imported because pkg/shared does
// not export them and this package is the only other reader of the syntax.
const (
	openDelim  = "{{"
	closeDelim = "}}"
)

// interpString renders s as a DSL string literal, turning each {{name}} into
// an ${name} interpolation.
//
// The scan is forward-only and single-pass, like TransformText's, so the two
// agree on what is a placeholder and what is a brace: a lone or trailing brace
// is copied through as text. An unclosed {{ and an empty {{}} are errors here
// because they are errors there -- the file never ran -- and a {{a.b}} naming
// something that is not an identifier has no interpolation to become.
func interpString(s string) (string, error) {
	var out strings.Builder
	out.WriteByte('"')

	rest := s
	for {
		open := strings.Index(rest, openDelim)
		if open < 0 {
			writeEscaped(&out, rest)
			out.WriteByte('"')
			return out.String(), nil
		}
		writeEscaped(&out, rest[:open])

		body := rest[open+len(openDelim):]
		end := strings.Index(body, closeDelim)
		if end < 0 {
			return "", fmt.Errorf("unclosed %q in %q", openDelim, s)
		}
		name := strings.TrimSpace(body[:end])
		expr, err := placeholderExpr(name, s)
		if err != nil {
			return "", err
		}
		out.WriteString("${")
		out.WriteString(expr)
		out.WriteByte('}')
		rest = body[end+len(closeDelim):]
	}
}

// placeholderExpr is what one {{...}} becomes inside an interpolation.
func placeholderExpr(name, whole string) (string, error) {
	switch {
	case name == "":
		return "", fmt.Errorf("empty placeholder %q in %q", openDelim+closeDelim, whole)
	case strings.HasPrefix(name, "env."):
		env := name[len("env."):]
		if env == "" {
			return "", fmt.Errorf("placeholder {{%s}} in %q names no environment variable", name, whole)
		}
		return `env(` + quote(env) + `)`, nil
	case !isIdent(name):
		return "", fmt.Errorf("placeholder {{%s}} in %q is not a name the DSL can resolve", name, whole)
	case token.IsReserved(name):
		return "", fmt.Errorf("placeholder {{%s}} in %q names a word the DSL reserves", name, whole)
	}
	return name, nil
}

// quote is a DSL string literal holding exactly s, with no interpolation in it.
func quote(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	writeEscaped(&out, s)
	out.WriteByte('"')
	return out.String()
}

// writeEscaped writes s as the body of a DSL string literal.
//
// `${` is escaped as `\${`, which is the escape the DSL added and the YAML
// templater never had, so text that happens to look like an interpolation stays
// text. A control character becomes \uXXXX rather than a raw byte: a string in
// a .art file may span lines, but a tab or a vertical feed written literally is
// a file nobody can read.
func writeEscaped(out *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			out.WriteString(`\"`)
		case c == '\\':
			out.WriteString(`\\`)
		case c == '$' && i+1 < len(s) && s[i+1] == '{':
			out.WriteString(`\$`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(out, `\u%04x`, c)
		default:
			out.WriteByte(c)
		}
	}
}

// regexLiteral is a pattern as a /.../ literal.
//
// A slash is escaped, because that is how the lexer's regex scanner reads a
// literal one. A pattern holding a line break has no literal form at all -- the
// scanner refuses to cross a newline -- so it is an error naming the pattern.
func regexLiteral(pattern string) (string, error) {
	if strings.ContainsAny(pattern, "\n\r") {
		return "", fmt.Errorf("regex %q holds a line break, which a /.../ literal cannot", pattern)
	}
	var out strings.Builder
	out.WriteByte('/')
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '/':
			out.WriteString(`\/`)
		case '\\':
			// An escape belongs to the pattern: copy both bytes so `\d` stays
			// `\d` and `\/` is not doubled into `\\/`.
			out.WriteByte(c)
			if i+1 < len(pattern) {
				i++
				out.WriteByte(pattern[i])
			}
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('/')
	return out.String(), nil
}

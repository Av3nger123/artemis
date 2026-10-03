package migrate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// writer accumulates .art source a line at a time.
//
// It indents, and that is all it does. Layout is pkg/dsl/print's: blank lines
// between declarations, whether a brace block fits on one line, and where a
// comment sits are all decided by canonical mode after this text is parsed. So
// there is no blank-line logic here and no measuring -- what this writes only
// has to parse, and what it indents is for the error message on the one path
// where it does not.
type writer struct {
	b      strings.Builder
	indent int
}

func (w *writer) line(s string) {
	w.b.WriteString(strings.Repeat("  ", w.indent))
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

// block writes `head { field, field }` across as many lines as there are
// fields, or just head when there are none -- `get "${base}/items"` with no
// braces, which is the common case and legal.
func (w *writer) block(head string, fields []string) {
	if len(fields) == 0 {
		w.line(head)
		return
	}
	w.line(head + " {")
	w.indent++
	for _, f := range fields {
		w.line(f)
	}
	w.indent--
	w.line("}")
}

// comment writes a YAML comment block at the current indentation.
//
// yaml.v3 hands back the block as its lines joined by newlines, each still
// carrying its own "#". A line that somehow has none gets one, so that text
// which was a comment in the YAML file cannot become source in the .art one.
func (w *writer) comment(block string) {
	if block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			line = "# " + line
		}
		w.line(strings.TrimRight(line, " \t"))
	}
}

func (w *writer) String() string { return w.b.String() }

// parseJSON decodes body as JSON, and reports whether it was an object or an
// array.
//
// Only those two count. A body of `42` or `"hi"` is valid JSON and is still
// text a server was sent, so turning it into a DSL number or re-quoting it buys
// nothing and risks changing the bytes on the wire.
//
// Numbers are decoded as json.Number, not float64, so a body holding an id too
// large for a float survives migration as the digits it was written with.
func parseJSON(body string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	// Trailing content means this was not one JSON document, whatever the first
	// token decoded to.
	if _, err := dec.Token(); err == nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return v, true
	}
	return nil, false
}

// jsonNumber is a json.Number as a DSL number literal, with the digits as they
// were written -- so an id too large for a float survives migration exactly.
// A number the DSL's lexer would not read back whole is reported rather than
// emitted, because `0x1f` written into a .art file lexes as two tokens and
// becomes a syntax error in a file nobody edited.
func jsonNumber(n json.Number) (string, error) {
	s := n.String()
	if !lexesAsNumber(s) {
		return "", fmt.Errorf("number %s is not one the DSL's grammar can write", s)
	}
	return s, nil
}

// lexesAsNumber reports whether s is a number the DSL's scanNumber reads whole:
// an optional minus, digits, an optional fraction, an optional exponent.
func lexesAsNumber(s string) bool {
	rest := strings.TrimPrefix(s, "-")
	mantissa, exponent, hasExp := cut(rest, "eE")
	intPart, frac, hasFrac := cut(mantissa, ".")

	if !allDigits(intPart) {
		return false
	}
	if hasFrac && !allDigits(frac) {
		return false
	}
	if hasExp {
		digits := strings.TrimLeft(exponent, "+-")
		if len(exponent)-len(digits) > 1 || !allDigits(digits) {
			return false
		}
	}
	return true
}

// cut splits s at the first byte in seps.
func cut(s, seps string) (before, after string, found bool) {
	if i := strings.IndexAny(s, seps); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

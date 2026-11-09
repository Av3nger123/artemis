package token

import (
	"strings"
	"unicode"
)

// MaxSnakeID is how long an id SnakeFrom makes may be.
const MaxSnakeID = 40

// IsSnakeID reports whether s is a snake_case id: ^[a-z][a-z0-9]*(_[a-z0-9]+)*$.
// It is what every collection, item, flow, step and scenario is named by.
func IsSnakeID(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '_':
			if prev == '_' || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
		prev = c
	}
	return true
}

// SnakeFrom makes an id of any text: "Get a token" is get_a_token and SignIn
// is sign_in. Every run of other characters becomes one underscore, a leading
// digit gets an s_ prefix, nothing left is "unnamed", and an id longer than
// MaxSnakeID is cut at the last whole word that fits. It is the hint for a
// name that is not an id, and what artemis migrate names a quoted name by.
func SnakeFrom(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
			b.WriteByte('_')
		}
		prev = r
		r = unicode.ToLower(r)
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	words := strings.FieldsFunc(b.String(), func(r rune) bool { return r == '_' })
	if len(words) == 0 {
		return "unnamed"
	}
	if c := words[0][0]; c >= '0' && c <= '9' {
		words = append([]string{"s"}, words...)
	}
	out := words[0]
	if len(out) > MaxSnakeID {
		return out[:MaxSnakeID]
	}
	for _, w := range words[1:] {
		if len(out)+1+len(w) > MaxSnakeID {
			break
		}
		out += "_" + w
	}
	return out
}

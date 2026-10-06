package trace

import (
	"encoding/json"
	"unicode/utf8"
)

// decodeJSON reads a request body back into the value domain, so a pointer can
// address a field of it.
//
// The body reached this package as the text that was sent, because that is what
// models.Request carries -- the trace reports what went out, not what the
// expression looked like. Withholding one field of it therefore means decoding
// it again. ok is false for a body that is not JSON, and the caller withholds
// the whole thing rather than guessing.
func decodeJSON(raw string) (any, bool) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, false
	}
	return v, true
}

// utf8ValidEnd reports whether b ends on a rune boundary, which is what keeps a
// truncated value valid UTF-8 and so valid JSON.
func utf8ValidEnd(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	r, size := utf8.DecodeLastRune(b)
	return r != utf8.RuneError || size > 1
}

package lexer

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"artemis/pkg/dsl/token"
)

// Interpolation is a lexical concern, not a substitution pass over a finished
// string. "${ Expr }" holds a whole expression, so `"${base}/users/${body.id}"`
// is lexed into pieces and the parser builds a concatenation out of them:
//
//	"a${x}b"  ->  StringStart(`"a${`)  Ident(x)  StringEnd(`}b"`)
//
// Each delimiter belongs to exactly one piece, which is what keeps the round
// trip exact, and the expression between them is scanned by the ordinary token
// loop, which is what makes `"${ env("X") }"` work for free.
//
// A literal newline inside a string is legal, so strings are multi-line and
// their spans cross lines. The cost is that a dropped closing quote runs to end
// of file; the error is reported at the opening quote, which is the character
// the author has to go and fix.

// scanString reads a double-quoted string starting at the cursor. It emits one
// String token, or a StringStart and pushes an interpolation frame.
func (l *lexer) scanString() {
	m := l.here()
	l.next() // the opening "
	l.scanStringBody(m, token.String, token.StringStart)
}

// scanStringResume reads the literal text after an interpolation's closing `}`.
// It emits a StringMid and leaves the frame open for the next expression, or a
// StringEnd and pops it.
func (l *lexer) scanStringResume() {
	m := l.here()
	l.next() // the closing } of the interpolation
	l.interp = l.interp[:len(l.interp)-1]
	l.scanStringBody(m, token.StringEnd, token.StringMid)
}

// scanStringBody scans literal string content from just after an opening
// delimiter to the next one.
//
// closed is the kind to emit when the content runs into the string's closing
// quote; opened is the kind to emit when it runs into a `${`, in which case a
// frame is pushed. The two call sites differ only in those kinds, because the
// content of `"a${` and of `}a${` is scanned by identical rules.
func (l *lexer) scanStringBody(m mark, closed, opened token.Kind) {
	var value strings.Builder

	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; c {
		case '"':
			l.next()
			l.emit(closed, m, l.src[m.pos:l.pos], value.String())
			return

		case '$':
			if l.peek(1) != '{' {
				// A lone dollar is just a dollar; only "${" opens an
				// interpolation, so `"$5.00"` needs no escaping.
				value.WriteByte(c)
				l.next()
				continue
			}
			l.next()
			l.next()
			open := l.span(mark{pos: l.pos - 2, line: l.line, col: l.col - 2})
			l.emit(opened, m, l.src[m.pos:l.pos], value.String())
			l.interp = append(l.interp, frame{open: open})
			return

		case '\\':
			l.escape(&value)

		default:
			value.WriteByte(c)
			l.next()
		}
	}

	// End of input with the string still open. The span covers everything that
	// was read, so the caret starts at the opening quote.
	l.invalid(m, "unterminated-string", `unterminated string: missing the closing '"'`)
}

// escape consumes a backslash escape and writes what it stands for into value.
//
// \$ is the one this language needed and the YAML templater never had: it is
// how a string says a literal dollar before a brace. The rest are the escapes
// anyone coming from JSON will try.
//
// An unknown escape emits a marker -- an error token with no text, positioned
// on the two bytes -- and the string carries on being scanned with the escape
// passed through verbatim. Reporting the escape where it is written, rather than
// failing the whole string literal, is what gives the caret something exact to
// point at, and the file is not going to compile either way.
func (l *lexer) escape(value *strings.Builder) {
	m := l.here()
	l.next() // the backslash

	if l.pos >= len(l.src) {
		l.marker(l.span(m), "invalid-escape", `a "\" at end of file escapes nothing`)
		value.WriteByte('\\')
		return
	}

	switch l.src[l.pos] {
	case '"', '\\', '/', '$':
		c := l.src[l.pos]
		value.WriteByte(c)
		l.next()
	case 'n':
		value.WriteByte('\n')
		l.next()
	case 'r':
		value.WriteByte('\r')
		l.next()
	case 't':
		value.WriteByte('\t')
		l.next()
	case 'u':
		l.next()
		l.unicodeEscape(value, m)
	default:
		// Consume the whole rune, not one byte, so a mistyped non-ASCII
		// character is named rather than printed as half of itself.
		_, size := utf8.DecodeRuneInString(l.src[l.pos:])
		for i := 0; i < size; i++ {
			l.next()
		}
		escape := l.src[m.pos:l.pos]
		l.marker(l.span(m), "invalid-escape",
			"unknown escape "+strconv.Quote(escape)+`; the escapes are \" \\ \/ \$ \n \r \t and \uXXXX`)
		value.WriteString(escape)
	}
}

// unicodeEscape consumes the four hex digits of a \uXXXX and writes the rune.
//
// A code point that is not a legal rune on its own -- half of a surrogate pair,
// which JSON would use for an astral character -- decodes to U+FFFD rather than
// to invalid UTF-8, because every Value in the tree has to be a Go string that
// survives being printed and re-read.
func (l *lexer) unicodeEscape(value *strings.Builder, m mark) {
	code := 0
	for i := 0; i < 4; i++ {
		d, ok := hexDigit(l.peek(0))
		if !ok {
			l.marker(l.span(m), "invalid-escape",
				`"\u" needs four hex digits, as in \u00e9`)
			value.WriteString(l.src[m.pos:l.pos])
			return
		}
		code = code<<4 | d
		l.next()
	}
	r := rune(code)
	if !utf8.ValidRune(r) {
		r = utf8.RuneError
	}
	value.WriteRune(r)
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

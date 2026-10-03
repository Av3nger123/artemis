// Package lexer turns .art source into tokens, keeping every byte.
//
// The invariant that matters more than any other here:
//
//	token.Source(Lex(file, src)) == src
//
// byte for byte, for every input -- valid, invalid, empty, or arbitrary bytes.
// Comments, blank lines and spacing survive as trivia attached to the tokens
// around them, which is what makes a lossless printer, and so subsystem D's UI,
// possible. A lexer that discarded them could not be made lossless later
// without being rewritten.
//
// Nothing here returns an error and nothing here panics. A malformed file
// produces token.Invalid tokens carrying a stable code and a message, lexing
// continues past them, and the stream still ends in exactly one EOF.
package lexer

import (
	"strings"
	"unicode/utf8"

	"artemis/pkg/dsl/token"
)

// Lex scans src and returns its tokens. file names the source in every span.
func Lex(file, src string) []token.Token {
	l := &lexer{file: file, src: src, line: 1, col: 1}
	l.run()
	return l.toks
}

type lexer struct {
	file string
	src  string

	// pos is a byte offset into src; line and col are the 1-based position of
	// that byte, with col counted in bytes.
	pos  int
	line int
	col  int

	toks []token.Token

	// pending holds trivia read but not yet attached to a token, because which
	// of two tokens it belongs to is only known once the next one starts. See
	// flush.
	pending []token.Trivia

	// interp is the stack of open string interpolations. It is a stack because
	// a string can appear inside a ${...} which is inside a string. Only the
	// innermost frame's brace depth is consulted, and it exists so that the `}`
	// closing an object literal inside an interpolation is told apart from the
	// `}` closing the interpolation itself.
	interp []frame
}

// frame is one open ${...}. depth counts the object-literal braces open inside
// it; open locates the `${` so an unclosed one can be reported where it was
// written rather than at end of file.
type frame struct {
	depth int
	open  token.Span
}

// mark is a position in the source, enough to span a token once its end is
// known.
type mark struct {
	pos  int
	line int
	col  int
}

func (l *lexer) run() {
	for {
		l.trivia()

		if l.pos >= len(l.src) {
			// An interpolation still open at end of file means the string
			// never closed either, but "unclosed ${" is the more useful of the
			// two messages and its position is the one worth pointing at.
			for i := len(l.interp) - 1; i >= 0; i-- {
				l.marker(l.interp[i].open, "unclosed-interpolation",
					`unclosed "${": the interpolation is missing its "}"`)
			}
			l.interp = nil
			l.emit(token.EOF, l.here(), "", "")
			return
		}

		// Inside an interpolation with no object literal open, a `}` ends the
		// expression and the string resumes. This is the one place a byte
		// means something other than itself, so it is tested before the
		// ordinary dispatch.
		if n := len(l.interp); n > 0 && l.interp[n-1].depth == 0 && l.src[l.pos] == '}' {
			l.scanStringResume()
			continue
		}

		l.scan()
	}
}

// scan reads one token, dispatching on its first byte.
func (l *lexer) scan() {
	c := l.src[l.pos]
	switch {
	case isWordStart(c):
		l.scanWord()
	case isDigit(c):
		l.scanNumber()
	case c == '"':
		l.scanString()
	case c == '/':
		// Unambiguously a regex: the grammar has no division operator, so the
		// lexer needs no "regex or divide" heuristic and a `/` in an
		// expression position is never anything else.
		l.scanRegex()
	default:
		l.scanOperator()
	}
}

// scanWord reads an identifier. true, false and null are the only words given
// kinds of their own; see token/tables.go for why everything else is an Ident.
func (l *lexer) scanWord() {
	m := l.here()
	for l.pos < len(l.src) && isWordPart(l.src[l.pos]) {
		l.next()
	}
	text := l.src[m.pos:l.pos]

	kind := token.Ident
	switch text {
	case "true", "false":
		kind = token.Bool
	case "null":
		kind = token.Null
	}
	l.emit(kind, m, text, text)
}

// scanNumber reads an integer or float: digits, an optional fractional part and
// an optional exponent.
//
// It never produces an error. A `.` is only taken when a digit follows it, so
// `1.` lexes as Number then Dot; an `e` is only taken when the exponent has at
// least one digit, so `1e` lexes as Number then Ident. Both are then the
// parser's problem, reported where a reader expects, and the lexer needs no
// invalid-number code.
func (l *lexer) scanNumber() {
	m := l.here()
	l.digits()
	if l.at('.') && isDigit(l.peek(1)) {
		l.next()
		l.digits()
	}
	if l.at('e') || l.at('E') {
		// Look past an optional sign for the first exponent digit before
		// committing to any of it.
		n := 1
		if l.peek(n) == '+' || l.peek(n) == '-' {
			n++
		}
		if isDigit(l.peek(n)) {
			for ; n > 0; n-- {
				l.next()
			}
			l.digits()
		}
	}
	text := l.src[m.pos:l.pos]
	l.emit(token.Number, m, text, text)
}

func (l *lexer) digits() {
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.next()
	}
}

// scanRegex reads a /.../ literal.
//
// It runs to the next unescaped `/` and does not cross a newline, so a dropped
// closing slash costs one line rather than the rest of the file. There are no
// flags and no character-class awareness: `/[/]/` terminates at the slash
// inside the class, and `\/` is the way to write a literal slash. Both are
// deliberate -- the lexer does not know regex syntax, and pretending to would
// mean maintaining a second regex parser alongside Go's.
func (l *lexer) scanRegex() {
	m := l.here()
	l.next() // the opening /

	var pattern strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n' || c == '\r':
			l.invalid(m, "unterminated-regex",
				`unterminated regex: a "/" literal cannot span lines`)
			return
		case c == '\\':
			l.next()
			if l.pos >= len(l.src) {
				continue
			}
			// \/ is how a pattern writes a literal slash; it is unescaped for
			// Value, because Go's regexp has no need of it. Every other escape
			// belongs to the pattern and is passed through untouched.
			if l.at('/') {
				pattern.WriteByte('/')
			} else {
				pattern.WriteByte('\\')
				pattern.WriteByte(l.src[l.pos])
			}
			l.next()
		case c == '/':
			l.next()
			l.emit(token.Regex, m, l.src[m.pos:l.pos], pattern.String())
			return
		default:
			pattern.WriteByte(c)
			l.next()
		}
	}
	l.invalid(m, "unterminated-regex", `unterminated regex: missing the closing "/"`)
}

// operators maps the punctuation of the grammar to kinds. Two-byte operators
// are tried first so that `==` is not a pair of `=`.
var twoByteOperators = map[string]token.Kind{
	"==": token.Eq,
	"!=": token.Ne,
	"<=": token.Le,
	">=": token.Ge,
}

var oneByteOperators = map[byte]token.Kind{
	'{': token.LBrace,
	'}': token.RBrace,
	'(': token.LParen,
	')': token.RParen,
	'[': token.LBracket,
	']': token.RBracket,
	',': token.Comma,
	'.': token.Dot,
	':': token.Colon,
	'=': token.Assign,
	'<': token.Lt,
	'>': token.Gt,
	'-': token.Minus,
}

func (l *lexer) scanOperator() {
	m := l.here()

	if l.pos+1 < len(l.src) {
		if kind, ok := twoByteOperators[l.src[l.pos:l.pos+2]]; ok {
			l.next()
			l.next()
			l.emit(kind, m, l.src[m.pos:l.pos], "")
			return
		}
	}

	c := l.src[l.pos]
	if kind, ok := oneByteOperators[c]; ok {
		l.next()
		// A brace inside an interpolation is an object literal's, and the
		// interpolation's own closing `}` is told from it by this count.
		if n := len(l.interp); n > 0 {
			switch kind {
			case token.LBrace:
				l.interp[n-1].depth++
			case token.RBrace:
				l.interp[n-1].depth--
			}
		}
		l.emit(kind, m, l.src[m.pos:l.pos], "")
		return
	}

	// Anything else is one bad rune. Consuming a whole rune rather than a byte
	// keeps the token's text valid UTF-8 and stops a multi-byte character
	// becoming three separate complaints.
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	for i := 0; i < size; i++ {
		l.next()
	}
	l.invalid(m, "unexpected-character",
		"unexpected character "+quoteRune(r)+" in a scenario file")
}

// trivia reads whitespace, newlines and comments into l.pending until the next
// byte starts a token.
func (l *lexer) trivia() {
	for l.pos < len(l.src) {
		m := l.here()
		switch c := l.src[l.pos]; {
		case c == '\n':
			l.next()
			l.addTrivia(token.Newline, m)
		case c == '\r':
			// \r\n is one terminator, so that a CRLF file has the same blank
			// lines as the same file with LF endings.
			l.next()
			if l.at('\n') {
				l.next()
			}
			l.addTrivia(token.Newline, m)
		case c == ' ' || c == '\t' || c == '\v' || c == '\f':
			for l.pos < len(l.src) && isBlank(l.src[l.pos]) {
				l.next()
			}
			l.addTrivia(token.Whitespace, m)
		case c == '#':
			// A comment is `#` to the end of the line, terminator excluded --
			// that is the newline trivium after it.
			for l.pos < len(l.src) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
				l.next()
			}
			l.addTrivia(token.Comment, m)
		case strings.HasPrefix(l.src[l.pos:], bom):
			// A byte-order mark is whitespace so that a file from an editor
			// that writes one still round-trips.
			for i := 0; i < len(bom); i++ {
				l.next()
			}
			l.addTrivia(token.Whitespace, m)
		default:
			return
		}
	}
}

func (l *lexer) addTrivia(kind token.TriviaKind, m mark) {
	l.pending = append(l.pending, token.Trivia{
		Kind: kind,
		Text: l.src[m.pos:l.pos],
		Span: l.span(m),
	})
}

// flush divides the pending trivia between the token just lexed and the one
// about to be: everything up to the first newline stays on the line it was
// written on and becomes the previous token's Trailing, and the newline and
// everything after it becomes the next token's Leading.
//
// One rule, stated once. It is why a trailing `# done` comment belongs to the
// `}` it follows, and why a blank line between two steps is two newline trivia
// on the second step's first token.
func (l *lexer) flush() []token.Trivia {
	pending := l.pending
	l.pending = nil

	// Markers are zero-width and must never own trivia, or a consumer that
	// skips them -- which every consumer does -- would lose it.
	prev := -1
	for i := len(l.toks) - 1; i >= 0; i-- {
		if !l.toks[i].IsMarker() {
			prev = i
			break
		}
	}
	if prev < 0 {
		// Nothing to trail: at the start of a file every trivium leads.
		return pending
	}

	split := len(pending)
	for i, tr := range pending {
		if tr.Kind == token.Newline {
			split = i
			break
		}
	}
	if split > 0 {
		l.toks[prev].Trailing = append(l.toks[prev].Trailing, pending[:split]...)
	}
	return pending[split:]
}

// emit appends a token spanning from m to the current position.
func (l *lexer) emit(kind token.Kind, m mark, text, value string) {
	leading := l.flush()
	l.toks = append(l.toks, token.Token{
		Kind:    kind,
		Text:    text,
		Value:   value,
		Span:    l.span(m),
		Leading: leading,
	})
}

// invalid emits a consuming error token: one that carries the source it read,
// and so takes part in the round trip like any other token.
func (l *lexer) invalid(m mark, code, message string) {
	leading := l.flush()
	l.toks = append(l.toks, token.Token{
		Kind:    token.Invalid,
		Text:    l.src[m.pos:l.pos],
		Code:    code,
		Message: message,
		Span:    l.span(m),
		Leading: leading,
	})
}

// marker emits a position-only error token for a fault found inside another
// token -- an unclosed ${, a bad escape in a string that is otherwise fine. It
// has no text, so it contributes nothing to the source, and it sits just before
// the token whose scan discovered it. See token.Token.IsMarker.
func (l *lexer) marker(span token.Span, code, message string) {
	l.toks = append(l.toks, token.Token{
		Kind:    token.Invalid,
		Code:    code,
		Message: message,
		Span:    span,
	})
}

// here is the current position, to be handed back to span once a token's end is
// known.
func (l *lexer) here() mark { return mark{pos: l.pos, line: l.line, col: l.col} }

// span runs from m to the current position, whose line and column are the
// exclusive end.
func (l *lexer) span(m mark) token.Span {
	return token.Span{
		File:    l.file,
		Line:    m.line,
		Col:     m.col,
		EndLine: l.line,
		EndCol:  l.col,
		Offset:  m.pos,
	}
}

// next consumes one byte, keeping line and column with it. Columns count bytes,
// so a multi-byte rune advances the column by its length -- the same convention
// go/token uses, and a caret's display width is the renderer's problem.
func (l *lexer) next() byte {
	c := l.src[l.pos]
	l.pos++
	// A lone \r is a line terminator too, so an old-Mac-ending file reports
	// the lines its author sees. In \r\n the \n does the incrementing, so the
	// pair counts once.
	if c == '\n' || (c == '\r' && (l.pos >= len(l.src) || l.src[l.pos] != '\n')) {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

// at reports whether the current byte is c.
func (l *lexer) at(c byte) bool { return l.pos < len(l.src) && l.src[l.pos] == c }

// peek is the byte n ahead of the cursor, or 0 past the end. Zero is safe to
// return because a NUL byte in source is not something the lexer accepts
// anywhere.
func (l *lexer) peek(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

// bom is a UTF-8 byte-order mark, written as an escape so that this file does
// not itself start with one.
const bom = "\ufeff"

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordPart(c byte) bool { return isWordStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\v' || c == '\f' }

// quoteRune names a rune in a message without the escaping strconv.QuoteRune
// applies to anything non-ASCII, which would turn a mistyped character into an
// unreadable \u escape.
func quoteRune(r rune) string {
	if r == utf8.RuneError {
		return "(invalid UTF-8)"
	}
	return `"` + string(r) + `"`
}

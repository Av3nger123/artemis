package token

// Kind is what a token is. The set is deliberately small: the Artemis grammar
// has no keywords in the lexical sense (see tables.go for why), so almost every
// word in a file arrives as Ident and the parser decides what it meant.
type Kind uint8

const (
	// EOF is the one token every stream ends with. It is zero-width and sits at
	// the position just past the last byte, so file-final trivia has somewhere
	// to live.
	EOF Kind = iota

	// Invalid is a lexical error. It never stops lexing -- see token.go for the
	// two shapes an Invalid token comes in, and Code/Message for what went
	// wrong.
	Invalid

	Ident  // url, status, body, get, scenario, step, contains -- any word
	Number // 42, 3.5, 1e9
	Bool   // true, false
	Null   // null
	Regex  // /.+@.+/

	// String is a double-quoted string with no interpolation in it, which is
	// the common case and stays a single token.
	String

	// StringStart, StringMid and StringEnd are the pieces an interpolated
	// string lexes into, with the interpolated expressions' own tokens in
	// between. "a${x}b${y}c" is StringStart(`"a${`) Ident StringMid(`}b${`)
	// Ident StringEnd(`}c"`), so each delimiter belongs to exactly one token
	// and concatenating Text is the source again.
	StringStart
	StringMid
	StringEnd

	LBrace   // {
	RBrace   // }
	LParen   // (
	RParen   // )
	LBracket // [
	RBracket // ]
	Comma    // ,
	Dot      // .
	Colon    // :
	Assign   // =
	Eq       // ==
	Ne       // !=
	Lt       // <
	Le       // <=
	Gt       // >
	Ge       // >=
	Minus    // -
)

// names are for test failures and diagnostics, not for round-tripping: a
// token's source text is in Text.
var names = map[Kind]string{
	EOF:         "EOF",
	Invalid:     "invalid",
	Ident:       "identifier",
	Number:      "number",
	Bool:        "boolean",
	Null:        "null",
	Regex:       "regex",
	String:      "string",
	StringStart: "string-start",
	StringMid:   "string-mid",
	StringEnd:   "string-end",
	LBrace:      "{",
	RBrace:      "}",
	LParen:      "(",
	RParen:      ")",
	LBracket:    "[",
	RBracket:    "]",
	Comma:       ",",
	Dot:         ".",
	Colon:       ":",
	Assign:      "=",
	Eq:          "==",
	Ne:          "!=",
	Lt:          "<",
	Le:          "<=",
	Gt:          ">",
	Ge:          ">=",
	Minus:       "-",
}

func (k Kind) String() string {
	if n, ok := names[k]; ok {
		return n
	}
	return "kind(" + itoa(int(k)) + ")"
}

// itoa avoids importing strconv for one unreachable branch.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// kinds is names reversed: the Kind a piece of punctuation spells. Only the
// operators and delimiters are reachable through it -- Ident's "identifier"
// and Number's "number" are the kind's own name and not source text -- which
// is exactly what tables.go wants when it asks which entries of Comparisons
// are symbols.
var kinds = func() map[string]Kind {
	m := make(map[string]Kind, len(names))
	for k, n := range names {
		m[n] = k
	}
	return m
}()

// kindOf is the Kind whose source spelling is s, and whether there is one.
func kindOf(s string) (Kind, bool) {
	k, ok := kinds[s]
	return k, ok
}

package token

import "strings"

// TriviaKind is what a piece of trivia is. There are only three, and the split
// matters: a printer in canonical mode normalises Whitespace and Newline freely
// but must never drop a Comment, and counting consecutive Newlines is how it
// knows the author left a blank line between two steps.
type TriviaKind uint8

const (
	// Whitespace is a run of spaces, tabs and other non-newline blanks. A
	// byte-order mark at the start of a file is whitespace too, so that a file
	// written by an editor that emits one still round-trips.
	Whitespace TriviaKind = iota

	// Newline is exactly one line terminator: "\n", "\r\n" or a lone "\r". One
	// terminator per trivium, never a run, so two of them in a row is
	// unambiguously a blank line.
	Newline

	// Comment is `#` to the end of the line, not including the terminator --
	// that is the Newline trivium after it.
	Comment
)

var triviaNames = map[TriviaKind]string{
	Whitespace: "whitespace",
	Newline:    "newline",
	Comment:    "comment",
}

func (k TriviaKind) String() string {
	if n, ok := triviaNames[k]; ok {
		return n
	}
	return "trivia(" + itoa(int(k)) + ")"
}

// Trivia is one piece of source that is not a token. Text is exact; Span
// locates it, because a diagnostic can point at a comment -- a `#` opening what
// the author meant as a step, say.
type Trivia struct {
	Kind TriviaKind
	Text string
	Span Span
}

// WriteTrivia appends every trivium's text, in order.
func WriteTrivia(b *strings.Builder, trivia []Trivia) {
	for _, t := range trivia {
		b.WriteString(t.Text)
	}
}
